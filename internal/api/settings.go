package api

import (
	"net/http"
	"strings"

	"github.com/logrenant/mimir/internal/settings"
)

// The settings routes.
//
// They are the one place in this package that serves *operator* configuration
// rather than machine state, and the distinction is worth keeping sharp:
// `GET /coding-models` and `GET /llm/providers` publish constants the binary
// ships, while these read and write files the operator owns. A route that let a
// client change a timeout or a token ceiling would be an SD-1 violation; a route
// that lets them choose which model their own outreach spends, and how it is
// written, is the opposite — those were never the machine's to decide.

// settingsView is the whole settings screen in one response, because it is one
// screen: a client that had to fan out to three routes to draw it would show the
// model picker before the rule files and look broken for the difference.
type settingsView struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Routed is what a run gets when no model is chosen — the class routing's
	// own answer. The picker needs it to label the empty option honestly.
	Routed llmRoutedDefault `json:"routed"`
	// Distill and Reason are the operator's standing preference for the two
	// classes the daemon routes on its own. Always present, empty when unset,
	// so a screen can tell "not chosen" from "field missing".
	Distill settings.Choice `json:"distill"`
	Reason  settings.Choice `json:"reason"`
	Rules   []ruleView      `json:"rules"`
}

type ruleView struct {
	Channel   string `json:"channel"`
	Label     string `json:"label"`
	Path      string `json:"path"`
	Body      string `json:"body"`
	IsDefault bool   `json:"is_default"`
	UpdatedAt int64  `json:"updated_at,omitempty"`
}

func viewOf(r settings.Rule) ruleView {
	v := ruleView{
		Channel:   string(r.Channel),
		Label:     r.Channel.Label(),
		Path:      r.Path,
		Body:      r.Body,
		IsDefault: r.IsDefault,
	}
	if !r.UpdatedAt.IsZero() {
		v.UpdatedAt = r.UpdatedAt.Unix()
	}
	return v
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	values, err := s.deps.Settings.Get()
	if err != nil {
		// A settings file that will not parse is worth saying out loud here,
		// unlike on the drafting path where it degrades to the defaults: this is
		// the screen whose whole job is to show the operator what is saved.
		writeError(w, http.StatusInternalServerError, codeInternal, err.Error())
		return
	}
	rules, err := s.deps.Settings.Rules()
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, err.Error())
		return
	}

	out := settingsView{
		Provider: values.Provider,
		Model:    values.Model,
		Routed: llmRoutedDefault{
			Provider: s.cfg.DistillProvider,
			Model:    s.cfg.DistillModel,
		},
		Distill: values.Distill,
		Reason:  values.Reason,
		Rules:   make([]ruleView, 0, len(rules)),
	}
	for _, rule := range rules {
		out.Rules = append(out.Rules, viewOf(rule))
	}
	writeJSON(w, http.StatusOK, out)
}

type saveSettingsRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Distill and Reason are the operator's standing preference for the two
	// classes the daemon routes on its own. Optional: a request that omits
	// them clears them, which is the same gesture as clearing the search bar's
	// own choice and keeps this a whole-document PUT rather than a patch.
	Distill *choiceRequest `json:"distill,omitempty"`
	Reason  *choiceRequest `json:"reason,omitempty"`
}

type choiceRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// handleSaveSettings stores the operator's default model choice.
//
// It runs the pair through the same allow-list a per-run selection goes
// through, and for the same reason: both strings become argv to a subprocess,
// and a saved value is more dangerous than a per-run one, not less — it is spent
// by every run afterwards without anybody re-reading it.
func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var req saveSettingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	sel, ok := s.llmSelection(w, req.Provider, req.Model)
	if !ok {
		return
	}
	// The class defaults go through the same gate. They are the most dangerous
	// values on this surface: a per-run selection is spent once and watched,
	// while these are spent by every Brain pass and every refine afterwards,
	// with nobody re-reading them.
	distill, ok := s.choice(w, req.Distill)
	if !ok {
		return
	}
	reason, ok := s.choice(w, req.Reason)
	if !ok {
		return
	}

	if err := s.deps.Settings.Put(settings.Values{
		Provider: sel.Provider,
		Model:    sel.Model,
		Distill:  distill,
		Reason:   reason,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, err.Error())
		return
	}
	s.handleGetSettings(w, r)
}

// choice validates one class default against the published table. A nil
// request is "no preference", which is valid and is how a default is cleared.
func (s *Server) choice(w http.ResponseWriter, req *choiceRequest) (settings.Choice, bool) {
	if req == nil || (req.Provider == "" && req.Model == "") {
		return settings.Choice{}, true
	}
	sel, ok := s.llmSelection(w, req.Provider, req.Model)
	if !ok {
		return settings.Choice{}, false
	}
	return settings.Choice{Provider: sel.Provider, Model: sel.Model}, true
}

type saveRuleRequest struct {
	Channel string `json:"channel"`
	Body    string `json:"body"`
}

func (s *Server) handleSaveRule(w http.ResponseWriter, r *http.Request) {
	var req saveRuleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ch, ok := s.channel(w, req.Channel)
	if !ok {
		return
	}

	rule, err := s.deps.Settings.PutRule(ch, req.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, viewOf(rule))
}

type resetRuleRequest struct {
	Channel string `json:"channel"`
}

// handleResetRule puts the shipped default back. A route rather than "send the
// default as a body": the client would then hold a copy of the default, which is
// exactly the drift `GET /coding-models` exists to avoid.
func (s *Server) handleResetRule(w http.ResponseWriter, r *http.Request) {
	var req resetRuleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ch, ok := s.channel(w, req.Channel)
	if !ok {
		return
	}

	rule, err := s.deps.Settings.ResetRule(ch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, viewOf(rule))
}

// channel parses a wire channel, answering 400 with the closed set rather than
// silently drafting for the wrong medium. The empty string is email — a client
// written before WhatsApp existed means the one channel it knew.
func (s *Server) channel(w http.ResponseWriter, raw string) (settings.Channel, bool) {
	ch, ok := settings.ParseChannel(raw)
	if !ok {
		names := make([]string, 0, len(settings.Channels()))
		for _, c := range settings.Channels() {
			names = append(names, string(c))
		}
		writeError(w, http.StatusBadRequest, codeBadRequest,
			"unknown channel "+raw+" — this daemon writes "+strings.Join(names, " and "))
		return "", false
	}
	return ch, true
}

// savedSelection is the operator's default model, as a selection.
//
// A saved pair that is no longer in the allow-list — a model retired by a later
// build — is dropped rather than passed on: it would fail at the subprocess with
// a message about argv, where here it can simply mean "route by class", which is
// what the operator had before they ever opened the settings screen.
func (s *Server) savedSelection() (provider, model string) {
	if s.deps.Settings == nil {
		return "", ""
	}
	v, err := s.deps.Settings.Get()
	if err != nil || v.IsZero() {
		return "", ""
	}
	if !s.cfg.HasLLMModel(v.Provider, v.Model) {
		return "", ""
	}
	return v.Provider, v.Model
}
