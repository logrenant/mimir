package leadgen

import (
	"context"
	"fmt"
	"strings"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/refine"
	"github.com/logrenant/mimir/internal/settings"
	"github.com/logrenant/mimir/internal/store"
)

// MessageDrafter is the model tier for stage 4. *refine.Client satisfies it.
type MessageDrafter interface {
	DraftMessage(ctx context.Context, in refine.MessageInput) (refine.Output, error)
}

// MessageStore is the cache tier for stage 4. *store.Store satisfies it; a nil
// MessageStore is legal — every company then costs its tokens every time (SD-6).
type MessageStore interface {
	GetOutreachMessage(ctx context.Context, placeID, promptVersion string) (store.OutreachMessage, bool, error)
	PutOutreachMessage(ctx context.Context, placeID, channel, promptVersion, body string, truncated bool) error
}

// RuleSource hands the drafter the operator's rule file for a channel, plus the
// version that file hashes to. *settings.Store satisfies it.
//
// It returns no error on purpose: a rule file that cannot be read falls back to
// the shipped default inside the settings package, and there is no third
// outcome worth a note here — drafting with no rules at all would silently
// change what every message says.
type RuleSource interface {
	RuleBody(ch settings.Channel) (body string, version string)
}

// MessageResult is one company's outreach draft on one channel.
type MessageResult struct {
	PlaceID   string
	Channel   settings.Channel
	Body      string
	Status    string // store.OutreachStatus* — "draft" for a fresh generation
	Truncated bool
	Method    string // MethodCache | MethodModel | MethodUnresolved
}

// MessageRunner drafts one outreach message per company per channel through two
// tiers, cache first. A row a human has already marked "sent" or "skipped" is
// returned from the cache untouched — the point of the status column is that a
// region re-run never regenerates a decision someone made.
type MessageRunner struct {
	cfg     config.Config
	drafter MessageDrafter
	store   MessageStore
	rules   RuleSource
	// sel is the operator's model override for this run. Zero routes by class.
	sel llm.Selection
}

// With returns the same stage bound to one run's model selection. A copy, for
// the reason Categorizer.With gives: the runner is shared across runs.
func (r *MessageRunner) With(sel llm.Selection) *MessageRunner {
	if r == nil || sel.IsZero() {
		return r
	}
	cp := *r
	cp.sel = sel
	return &cp
}

// UseRules installs the operator's rule files. Set after construction, like the
// pipeline's ledger: a runner without one drafts from the shipped prompt alone,
// which is exactly what this stage did before the rule files existed.
func (r *MessageRunner) UseRules(rs RuleSource) {
	if r != nil {
		r.rules = rs
	}
}

// version namespaces the cache by everything that decides what the draft says:
// the prompt constant, the model that wrote it, the channel, and the rule file.
//
// The channel is in the key rather than in a second table because a WhatsApp
// line and an email to the same company are two different answers to the same
// question, and the row this table holds is "the message we have for this
// place". The rule hash is in it for the sharper reason: the rule file is part
// of the prompt, so a draft written under the old rules is not an answer to the
// new ones, and serving it would make editing the rules look like it did
// nothing.
//
// The cost is the same one the model selection already carries, and it is worth
// stating plainly: the *status* column is namespaced too, so a message the
// operator marked "sent" under one rule file is not replayed after they edit it.
// Editing the rules mid-campaign can therefore re-draft a message that has
// already gone out. That is the honest trade — the alternative is showing a
// draft the current rules never produced — and it is why the settings screen
// says so out loud.
func (r *MessageRunner) version(ch settings.Channel) string {
	v := r.cfg.LeadgenEmailVersion
	if key := r.sel.Key(); key != "" {
		v += "@" + key
	}
	v += "#" + string(ch)
	if _, ruleVersion := r.ruleBody(ch); ruleVersion != "" {
		v += ":" + ruleVersion
	}
	return v
}

func (r *MessageRunner) ruleBody(ch settings.Channel) (string, string) {
	if r == nil || r.rules == nil {
		return "", ""
	}
	return r.rules.RuleBody(ch)
}

// NewMessageRunner wires the runner. drafter or s may be nil.
func NewMessageRunner(cfg config.Config, drafter MessageDrafter, s MessageStore) *MessageRunner {
	return &MessageRunner{cfg: cfg, drafter: drafter, store: s}
}

// DraftFor produces (or replays) one outreach message for one company on one
// channel, using its category's stage-3 gap analysis as the reference the draft
// is written from.
//
// gaps names what could not be resolved and why; it is diagnostic, never a
// reason to fail. A company with no place_id, an empty gap analysis, a failing
// cache, a failing subprocess, or an unusable model answer all degrade to an
// unresolved MessageResult (SD-6). Only the caller's own cancellation returns an
// error.
func (r *MessageRunner) DraftFor(ctx context.Context, company maps.Company, cat Category, gapAnalysis string, ch settings.Channel) (MessageResult, []string, error) {
	res := MessageResult{PlaceID: company.PlaceID, Channel: ch, Method: MethodUnresolved}
	if err := ctx.Err(); err != nil {
		return MessageResult{}, nil, err
	}

	var gaps []string

	// A message is addressed and cached by place_id. A scrape-fallback row
	// without one can still be categorized and gap-analysed, but there is no
	// key to store its draft under, so it is skipped here rather than drafted
	// and lost.
	if company.PlaceID == "" {
		gaps = append(gaps, fmt.Sprintf("%s draft skipped for %q: no place_id to key it by", ch, company.Name))
		return res, gaps, nil
	}
	if strings.TrimSpace(gapAnalysis) == "" {
		gaps = append(gaps, fmt.Sprintf("%s draft skipped for %s: no gap analysis for category %s", ch, company.PlaceID, cat))
		return res, gaps, nil
	}

	version := r.version(ch)

	// Tier 1: the cache. A draft, a sent message and a skipped one are all
	// returned as-is — regenerating any of them either wastes tokens or
	// overrides a human.
	if r.store != nil {
		hit, ok, err := r.store.GetOutreachMessage(ctx, company.PlaceID, version)
		if err != nil {
			gaps = append(gaps, "outreach cache unavailable: "+err.Error())
		} else if ok {
			res.Body = hit.Body
			res.Status = hit.Status
			res.Truncated = hit.Truncated
			res.Method = MethodCache
			return res, gaps, nil
		}
	}

	if r.drafter == nil {
		gaps = append(gaps, fmt.Sprintf("%s draft for %s left unresolved: no drafter configured", ch, company.PlaceID))
		return res, gaps, nil
	}

	rules, _ := r.ruleBody(ch)

	// Tier 2: the model. Fed the company's own facts plus the category gap
	// analysis — no raw page text.
	out, err := r.drafter.DraftMessage(ctx, refine.MessageInput{
		Channel:      ch,
		BusinessName: company.Name,
		Category:     string(cat),
		Region:       company.FormattedAddress,
		GapAnalysis:  gapAnalysis,
		HasWebsite:   strings.TrimSpace(company.Website) != "",
		Rating:       company.Rating,
		ReviewCount:  company.ReviewCount,
		MaxTokens:    r.cfg.LeadgenEmailMaxTokens,
		Rules:        rules,
		Selection:    r.sel,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return MessageResult{}, nil, ctxErr
		}
		gaps = append(gaps, fmt.Sprintf("%s draft for %s failed: %v", ch, company.PlaceID, err))
		return res, gaps, nil
	}
	if !out.Refined || strings.TrimSpace(out.Text) == "" {
		gaps = append(gaps, fmt.Sprintf("%s draft for %s returned nothing usable", ch, company.PlaceID))
		return res, gaps, nil
	}

	res.Body = out.Text
	res.Truncated = out.Truncated
	res.Status = store.OutreachStatusDraft
	res.Method = MethodModel

	if r.store != nil {
		if err := r.store.PutOutreachMessage(ctx, company.PlaceID, string(ch), version, res.Body, res.Truncated); err != nil {
			gaps = append(gaps, "caching outreach draft failed: "+err.Error())
		}
	}

	return res, gaps, nil
}
