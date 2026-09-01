package leadgen

import (
	"context"
	"fmt"
	"strings"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/maps"
	"github.com/logrenant/goat-mcp/internal/refine"
	"github.com/logrenant/goat-mcp/internal/store"
)

// EmailDrafter is the model tier for stage 4. *refine.Client satisfies it.
type EmailDrafter interface {
	DraftEmail(ctx context.Context, in refine.EmailInput) (refine.Output, error)
}

// EmailStore is the cache tier for stage 4. *store.Store satisfies it; a nil
// EmailStore is legal — every company then costs its tokens every time (SD-6).
type EmailStore interface {
	GetOutreachEmail(ctx context.Context, placeID, promptVersion string) (store.OutreachEmail, bool, error)
	PutOutreachEmail(ctx context.Context, placeID, promptVersion, email string, truncated bool) error
}

// EmailResult is one company's outreach draft.
type EmailResult struct {
	PlaceID   string
	Email     string
	Status    string // store.EmailStatus* — "draft" for a fresh generation
	Truncated bool
	Method    string // MethodCache | MethodModel | MethodUnresolved
}

// EmailRunner drafts one outreach email per company through two tiers, cache
// first. A row a human has already marked "sent" or "skipped" is returned from
// the cache untouched — the point of the status column is that a region re-run
// never regenerates a decision someone made.
type EmailRunner struct {
	cfg     config.Config
	drafter EmailDrafter
	store   EmailStore
}

// NewEmailRunner wires the runner. drafter or s may be nil.
func NewEmailRunner(cfg config.Config, drafter EmailDrafter, s EmailStore) *EmailRunner {
	return &EmailRunner{cfg: cfg, drafter: drafter, store: s}
}

// DraftFor produces (or replays) the outreach email for one company, using its
// category's stage-3 gap analysis as the reference the draft is written from.
//
// gaps names what could not be resolved and why; it is diagnostic, never a
// reason to fail. A company with no place_id, an empty gap analysis, a failing
// cache, a failing subprocess, or an unusable model answer all degrade to an
// unresolved EmailResult (SD-6). Only the caller's own cancellation returns an
// error.
func (r *EmailRunner) DraftFor(ctx context.Context, company maps.Company, cat Category, gapAnalysis string) (EmailResult, []string, error) {
	res := EmailResult{PlaceID: company.PlaceID, Method: MethodUnresolved}
	if err := ctx.Err(); err != nil {
		return EmailResult{}, nil, err
	}

	var gaps []string

	// An email is addressed and cached by place_id. A scrape-fallback row
	// without one can still be categorized and gap-analysed, but there is no
	// key to store its draft under, so it is skipped here rather than drafted
	// and lost.
	if company.PlaceID == "" {
		gaps = append(gaps, fmt.Sprintf("outreach email skipped for %q: no place_id to key it by", company.Name))
		return res, gaps, nil
	}
	if strings.TrimSpace(gapAnalysis) == "" {
		gaps = append(gaps, fmt.Sprintf("outreach email skipped for %s: no gap analysis for category %s", company.PlaceID, cat))
		return res, gaps, nil
	}

	version := r.cfg.LeadgenEmailVersion

	// Tier 1: the cache. A draft, a sent email and a skipped one are all
	// returned as-is — regenerating any of them either wastes tokens or
	// overrides a human.
	if r.store != nil {
		hit, ok, err := r.store.GetOutreachEmail(ctx, company.PlaceID, version)
		if err != nil {
			gaps = append(gaps, "outreach email cache unavailable: "+err.Error())
		} else if ok {
			res.Email = hit.Email
			res.Status = hit.Status
			res.Truncated = hit.Truncated
			res.Method = MethodCache
			return res, gaps, nil
		}
	}

	if r.drafter == nil {
		gaps = append(gaps, fmt.Sprintf("outreach email for %s left unresolved: no drafter configured", company.PlaceID))
		return res, gaps, nil
	}

	// Tier 2: the model. Fed the company's own facts plus the category gap
	// analysis — no raw page text.
	out, err := r.drafter.DraftEmail(ctx, refine.EmailInput{
		BusinessName: company.Name,
		Category:     string(cat),
		Region:       company.FormattedAddress,
		GapAnalysis:  gapAnalysis,
		HasWebsite:   strings.TrimSpace(company.Website) != "",
		Rating:       company.Rating,
		ReviewCount:  company.ReviewCount,
		MaxTokens:    r.cfg.LeadgenEmailMaxTokens,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return EmailResult{}, nil, ctxErr
		}
		gaps = append(gaps, fmt.Sprintf("outreach email for %s failed: %v", company.PlaceID, err))
		return res, gaps, nil
	}
	if !out.Refined || strings.TrimSpace(out.Text) == "" {
		gaps = append(gaps, fmt.Sprintf("outreach email for %s returned nothing usable", company.PlaceID))
		return res, gaps, nil
	}

	res.Email = out.Text
	res.Truncated = out.Truncated
	res.Status = store.EmailStatusDraft
	res.Method = MethodModel

	if r.store != nil {
		if err := r.store.PutOutreachEmail(ctx, company.PlaceID, version, res.Email, res.Truncated); err != nil {
			gaps = append(gaps, "caching outreach email failed: "+err.Error())
		}
	}

	return res, gaps, nil
}
