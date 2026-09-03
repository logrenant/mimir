package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/leadgen"
	"github.com/logrenant/mimir/internal/llm"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/store"
)

// nearArg is the optional location-bias circle, matching the maps_search tool's
// wire shape so a client can send the same object to either surface.
type nearArg struct {
	Latitude     float64 `json:"latitude"`
	Longitude    float64 `json:"longitude"`
	RadiusMeters float64 `json:"radius_meters"`
}

type leadgenRequest struct {
	Query        string   `json:"query"`
	Region       string   `json:"region"`
	Count        int      `json:"count"`
	LanguageCode string   `json:"language_code"`
	RegionCode   string   `json:"region_code"`
	Near         *nearArg `json:"near"`
	GapAnalysis  bool     `json:"gap_analysis"`
	Emails       bool     `json:"emails"`

	// Provider and Model route this run's model stages by hand. Both are
	// optional and both are checked against cfg.LLMProviders before they go
	// anywhere — see leadgenSelection.
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// handleLeadgen runs the full Maps lead-gen pipeline: region search →
// categorize → (optional) per-category gap analysis → (optional) per-company
// outreach email. Stages 1–2 always run; 3–4 are opt-in per request because
// they spend Claude tokens.
//
// The response is the pipeline's Report verbatim — this handler is a door, not
// a floor: internal/leadgen owns every decision about caching, cost, and
// degradation, and its Notes field already carries the diagnostic gaps.
// leadgenExportRequest is a run plus a file.
//
// It takes the same search a run takes rather than a report id, because the
// pipeline caches the region search: exporting right after a run re-reads the
// cache instead of re-searching, and exporting a region searched yesterday
// still produces a file without asking the operator to run it again.
type leadgenExportRequest struct {
	leadgenRequest
	// Enrich opens each company's website for a phone number and an email
	// address. Off by default: it is one page fetch per company.
	Enrich bool `json:"enrich"`
	// Dir overrides where the workbook is written. Empty is the configured
	// export directory, which is where the desktop app looks.
	Dir string `json:"dir"`
}

// handleLeadgenExport runs the pipeline and writes the workbook: a summary
// sheet, then one sheet per category.
func (s *Server) handleLeadgenExport(w http.ResponseWriter, r *http.Request) {
	var req leadgenExportRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	q, ok := s.leadgenQuery(w, req.leadgenRequest)
	if !ok {
		return
	}
	sel, ok := s.leadgenSelection(w, req.leadgenRequest)
	if !ok {
		return
	}

	report, err := s.deps.LeadGen.Run(r.Context(), leadgen.RunRequest{
		Query:           q,
		Region:          strings.TrimSpace(req.Region),
		WithGapAnalysis: req.GapAnalysis,
		WithEmails:      req.Emails,
		Selection:       sel,
	})
	if err != nil {
		writeLeadgenError(w, r, err)
		return
	}

	result, err := s.deps.LeadGen.Export(r.Context(), leadgen.ExportRequest{
		Report: report,
		Enrich: req.Enrich,
		Dir:    strings.TrimSpace(req.Dir),
	})
	if err != nil {
		writeLeadgenError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// leadgenQuery validates the shared search fields. One place, because the run
// route and the export route must not drift on what a region search means.
func (s *Server) leadgenQuery(w http.ResponseWriter, req leadgenRequest) (maps.Query, bool) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "query is required — pass what you would type into Google Maps")
		return maps.Query{}, false
	}

	q := maps.Query{
		Text:         query,
		LanguageCode: req.LanguageCode,
		RegionCode:   req.RegionCode,
		MaxResults:   req.Count,
	}
	if req.Near != nil {
		if req.Near.RadiusMeters <= 0 {
			writeError(w, http.StatusBadRequest, codeBadRequest, "near.radius_meters must be greater than 0")
			return maps.Query{}, false
		}
		q.Bias = &maps.Circle{
			Latitude:     req.Near.Latitude,
			Longitude:    req.Near.Longitude,
			RadiusMeters: req.Near.RadiusMeters,
		}
	}
	return q, true
}

// llmProviderListResponse publishes the provider/model allow-list.
type llmProviderListResponse struct {
	Providers []config.LLMProviderChoice `json:"providers"`
	// Routed names what a run gets when it sends no selection at all. The
	// picker needs it to say "varsayılan" against the right entry rather than
	// inventing one, and it is not derivable from the list: it comes from the
	// class routing, which is a different constant.
	Routed llmRoutedDefault `json:"routed"`
}

type llmRoutedDefault struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// handleListLLMProviders is the picker's source of truth, published rather
// than mirrored — a second copy in the desktop app would drift the first time
// a generation ships, and the daemon would then reject a combination the app
// had just offered.
//
// Nothing here is secret or per-operator: it is the same table
// leadgenSelection validates against.
func (s *Server) handleListLLMProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, llmProviderListResponse{
		Providers: s.cfg.LLMProviders,
		Routed: llmRoutedDefault{
			Provider: s.cfg.DistillProvider,
			Model:    s.cfg.DistillModel,
		},
	})
}

// leadgenSelection validates the operator's provider/model override.
//
// Rejecting rather than falling back to the default is the point. Both strings
// become argv to a subprocess, so nothing that is not in cfg.LLMProviders may
// pass; and a client that asked for a model this daemon does not offer has
// asked for something specific, so quietly running a different one would spend
// a budget nobody chose. A model without a provider is equally a mistake worth
// naming — the same model id can be reachable through more than one CLI.
func (s *Server) leadgenSelection(w http.ResponseWriter, req leadgenRequest) (llm.Selection, bool) {
	provider := strings.TrimSpace(req.Provider)
	model := strings.TrimSpace(req.Model)

	if provider == "" && model == "" {
		return llm.Selection{}, true
	}
	if provider == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest,
			"model was given without a provider — send both, or neither to route by class")
		return llm.Selection{}, false
	}
	if !s.cfg.HasLLMModel(provider, model) {
		writeError(w, http.StatusBadRequest, codeBadRequest,
			"unknown provider/model combination — GET /llm/providers lists what this daemon will run")
		return llm.Selection{}, false
	}
	if model == "" {
		model = s.cfg.LLMDefaultModel(provider)
	}
	return llm.Selection{Provider: provider, Model: model}, true
}

func (s *Server) handleLeadgen(w http.ResponseWriter, r *http.Request) {
	var req leadgenRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	q, ok := s.leadgenQuery(w, req)
	if !ok {
		return
	}
	sel, ok := s.leadgenSelection(w, req)
	if !ok {
		return
	}

	report, err := s.deps.LeadGen.Run(r.Context(), leadgen.RunRequest{
		Query:           q,
		Region:          strings.TrimSpace(req.Region),
		WithGapAnalysis: req.GapAnalysis,
		WithEmails:      req.Emails,
		Selection:       sel,
	})
	if err != nil {
		writeLeadgenError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, report)
}

type emailStatusRequest struct {
	PlaceID string `json:"place_id"`
	Status  string `json:"status"`
}

// handleSetEmailStatus records a human's decision on a drafted outreach email.
// The prompt version is the server's constant, not a client field: a client
// marking "sent" means "the email I am looking at", which is the current
// version's draft.
func (s *Server) handleSetEmailStatus(w http.ResponseWriter, r *http.Request) {
	var req emailStatusRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.PlaceID) == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "place_id is required")
		return
	}
	if s.deps.Emails == nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "email status is not available on this daemon")
		return
	}

	err := s.deps.Emails.SetOutreachEmailStatus(r.Context(), req.PlaceID, s.cfg.LeadgenEmailVersion, req.Status)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, store.ErrEmailStatusInvalid):
		writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
	case errors.Is(err, store.ErrEmailNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, err.Error())
	default:
		writeDomainError(w, r, err)
	}
}

// writeLeadgenError maps the pipeline's only hard-failure sentinel onto a
// status. ErrNoData means no upstream source (Places or the scrape fallback)
// produced a company list — a 502, because the daemon itself is fine.
func writeLeadgenError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, leadgen.ErrNoData) {
		writeError(w, http.StatusBadGateway, codeInternal, err.Error())
		return
	}
	writeDomainError(w, r, err)
}

// savedLead is one ledger row on the wire. It repeats CompanyLead's field names
// deliberately: a client should not need two company shapes to render one
// table, and the only additions are the two timestamps a record has and a run
// result does not.
type savedLead struct {
	PlaceID        string  `json:"place_id"`
	Name           string  `json:"name"`
	Address        string  `json:"address,omitempty"`
	Latitude       float64 `json:"latitude,omitempty"`
	Longitude      float64 `json:"longitude,omitempty"`
	Rating         float64 `json:"rating,omitempty"`
	ReviewCount    int     `json:"review_count,omitempty"`
	Website        string  `json:"website,omitempty"`
	Phone          string  `json:"phone,omitempty"`
	PrimaryType    string  `json:"primary_type,omitempty"`
	BusinessStatus string  `json:"business_status,omitempty"`
	Source         string  `json:"source,omitempty"`
	Category       string  `json:"category"`
	CategoryMethod string  `json:"category_method,omitempty"`
	Email          string  `json:"email,omitempty"`
	EmailStatus    string  `json:"email_status,omitempty"`
	FirstSeenAt    int64   `json:"first_seen_at"`
	LastSeenAt     int64   `json:"last_seen_at"`
}

type savedLeadsResponse struct {
	Companies []savedLead `json:"companies"`
	Limit     int         `json:"limit"`
	Offset    int         `json:"offset"`
}

type leadCategoryCount struct {
	Category       string `json:"category"`
	Companies      int    `json:"company_count"`
	WithoutWebsite int    `json:"without_website"`
}

type leadRunView struct {
	ID           string `json:"id"`
	Query        string `json:"query"`
	Region       string `json:"region,omitempty"`
	Source       string `json:"source,omitempty"`
	CompanyCount int    `json:"company_count"`
	WithGaps     bool   `json:"with_gaps,omitempty"`
	WithEmails   bool   `json:"with_emails,omitempty"`
	RanAt        int64  `json:"ran_at"`
}

// leadFilter reads the shared query string for the two ledger reads, so the
// listing and its category rail can never be filtered differently.
func (s *Server) leadFilter(r *http.Request) (store.LeadFilter, error) {
	q := r.URL.Query()

	limit, err := graphLimit(q.Get("limit"), s.cfg.LeadsPageDefault, s.cfg.LeadsPageMax)
	if err != nil {
		return store.LeadFilter{}, err
	}
	offset := 0
	if raw := strings.TrimSpace(q.Get("offset")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return store.LeadFilter{}, errBadLimit
		}
		offset = n
	}

	return store.LeadFilter{
		Category:       strings.TrimSpace(q.Get("category")),
		RunID:          strings.TrimSpace(q.Get("run_id")),
		Text:           strings.TrimSpace(q.Get("q")),
		WithoutWebsite: q.Get("without_website") == "1" || q.Get("without_website") == "true",
		Limit:          limit,
		Offset:         offset,
	}, nil
}

// handleListLeads serves the ledger: every business a lead-gen run has ever
// returned, with its category and the status of its outreach draft. Unlike
// POST /maps/leadgen this costs nothing and searches nothing — it reads rows.
func (s *Server) handleListLeads(w http.ResponseWriter, r *http.Request) {
	f, err := s.leadFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "limit and offset must be positive integers")
		return
	}

	rows, err := s.deps.Leads.ListLeads(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, err.Error())
		return
	}

	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.PlaceID)
	}
	// One query for every draft on the page rather than one per row: the
	// ledger's whole point is that reading it is cheap.
	drafts, err := s.deps.Leads.OutreachEmailsFor(r.Context(), ids, s.cfg.LeadgenEmailVersion)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, err.Error())
		return
	}

	out := savedLeadsResponse{
		Companies: make([]savedLead, 0, len(rows)),
		Limit:     f.Limit,
		Offset:    f.Offset,
	}
	for _, row := range rows {
		l := savedLead{
			PlaceID:        row.PlaceID,
			Name:           row.Name,
			Address:        row.Address,
			Latitude:       row.Latitude,
			Longitude:      row.Longitude,
			Rating:         row.Rating,
			ReviewCount:    row.ReviewCount,
			Website:        row.Website,
			Phone:          row.Phone,
			PrimaryType:    row.PrimaryType,
			BusinessStatus: row.BusinessStatus,
			Source:         row.Source,
			Category:       row.Category,
			CategoryMethod: row.CategoryMethod,
			FirstSeenAt:    row.FirstSeenAt.Unix(),
			LastSeenAt:     row.LastSeenAt.Unix(),
		}
		if d, ok := drafts[row.PlaceID]; ok {
			l.Email = d.Email
			l.EmailStatus = d.Status
		}
		out.Companies = append(out.Companies, l)
	}

	writeJSON(w, http.StatusOK, out)
}

// handleLeadCategories is the category rail.
func (s *Server) handleLeadCategories(w http.ResponseWriter, r *http.Request) {
	f, err := s.leadFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "limit and offset must be positive integers")
		return
	}

	// The one read that ignores the selected category. A rail that counted only
	// what is selected would show a single bar, and the operator could never
	// switch away from it. The rule is here rather than in the store because it
	// is a decision about the screen.
	f.Category = ""

	counts, err := s.deps.Leads.LeadCategoryCounts(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, err.Error())
		return
	}

	out := make([]leadCategoryCount, 0, len(counts))
	for _, c := range counts {
		out = append(out, leadCategoryCount{
			Category:       c.Category,
			Companies:      c.Companies,
			WithoutWebsite: c.WithoutWebsite,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": out})
}

// handleListLeadRuns is the run history: which search found what, and when.
func (s *Server) handleListLeadRuns(w http.ResponseWriter, r *http.Request) {
	limit, err := graphLimit(r.URL.Query().Get("limit"), s.cfg.LeadRunsMax, s.cfg.LeadRunsMax)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "limit must be a positive integer")
		return
	}

	runs, err := s.deps.Leads.ListLeadRuns(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, err.Error())
		return
	}

	out := make([]leadRunView, 0, len(runs))
	for _, run := range runs {
		out = append(out, leadRunView{
			ID:           run.ID,
			Query:        run.Query,
			Region:       run.RegionLabel,
			Source:       run.Source,
			CompanyCount: run.CompanyCount,
			WithGaps:     run.WithGaps,
			WithEmails:   run.WithEmails,
			RanAt:        run.RanAt.Unix(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": out})
}
