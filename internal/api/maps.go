package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/leadgen"
	"github.com/logrenant/mimir/internal/llm"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/settings"
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
	// SkipContacts turns stage 1b off. Phrased as an opt-*out* because the
	// useful default is on: a lead nobody can ring is not a lead, and the
	// enricher's answers are now kept in the ledger, so the fetch is paid once
	// per company rather than once per run.
	SkipContacts bool `json:"skip_contacts"`

	// Provider and Model route this run's model stages by hand. Both are
	// optional and both are checked against cfg.LLMProviders before they go
	// anywhere — see llmSelection.
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
	sel, ok := s.leadgenSelection(w, req.Provider, req.Model)
	if !ok {
		return
	}

	report, err := s.deps.LeadGen.Run(r.Context(), leadgen.RunRequest{
		Query:           q,
		Region:          strings.TrimSpace(req.Region),
		WithContacts:    !req.SkipContacts,
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
	// Available is what this machine can actually run, when a router is
	// wired. Omitted rather than empty when it is not, so a client can tell
	// "nothing is installed" from "nobody asked".
	Available []llm.Availability `json:"available,omitempty"`
}

// connectionView is one connection as a screen reads it. It has no field that
// could carry a secret or an executable path, and that is deliberate rather
// than incidental — see `internal/llm/connection.go`.
type connectionView struct {
	ID           string                     `json:"id"`
	Label        string                     `json:"label"`
	Vendor       string                     `json:"vendor"`
	Adapter      string                     `json:"adapter"`
	Transport    string                     `json:"transport"`
	DefaultModel string                     `json:"default_model"`
	Models       []config.CodingModelChoice `json:"models,omitempty"`
	Discovered   bool                       `json:"discovered,omitempty"`
	Enabled      bool                       `json:"enabled"`
	Builtin      bool                       `json:"builtin"`
	// Connect is what an operator does to sign this in. Carried per connection
	// because it is a property of the binary, not of the vendor.
	Connect llm.ConnectSpec `json:"connect"`
	// Availability is what this machine says about it, when a router is wired.
	Availability *llm.Availability `json:"availability,omitempty"`
}

type connectionListResponse struct {
	Connections []connectionView `json:"connections"`
	// Catalogue is every way to reach models this product knows about,
	// including the ones this build cannot run yet. Published so the picker is
	// the final one rather than a picker that grows a section later.
	Catalogue []llm.CatalogueEntry `json:"catalogue,omitempty"`
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
// llmSelection validates against.
// handleListConnections is the connection-shaped view of the same question
// `/llm/providers` answers, and it is the one a screen that manages connections
// reads.
//
// The older route keeps its shape — a superset, so an existing client is not
// broken by a refactor — and this one says what each connection *is*: its
// label, its vendor, whether it rides a CLI or an endpoint. Never a secret:
// this surface reports whether one is set, and nothing more.
func (s *Server) handleListConnections(w http.ResponseWriter, r *http.Request) {
	res := connectionListResponse{Connections: []connectionView{}}
	if s.deps.Connections == nil {
		writeJSON(w, http.StatusOK, res)
		return
	}

	available := map[string]llm.Availability{}
	if s.deps.LLM != nil {
		for _, a := range s.deps.LLM.Discover(r.Context(), r.URL.Query().Get("probe") == "1", s.cfg.LLMProbeTimeout) {
			available[a.Provider] = a
		}
	}

	res.Catalogue = s.deps.Connections.Catalogue()

	for _, spec := range s.deps.Connections.Specs() {
		view := connectionView{
			ID:           spec.ID,
			Label:        spec.Label,
			Vendor:       spec.Vendor,
			Adapter:      string(spec.Adapter),
			Transport:    string(spec.Transport()),
			DefaultModel: spec.DefaultModel,
			Discovered:   spec.Discovered,
			Enabled:      spec.Enabled,
			Builtin:      spec.Builtin,
			Connect:      spec.Connect(),
		}
		for _, m := range spec.Models {
			view.Models = append(view.Models, config.CodingModelChoice{ID: m.ID, Label: m.Label})
		}
		if a, ok := available[spec.ID]; ok {
			view.Availability = &a
		}
		res.Connections = append(res.Connections, view)
	}
	writeJSON(w, http.StatusOK, res)
}

// handleAddConnection is the extension point, and today it refuses everything.
//
// That is the shape working rather than a placeholder. Every provider an
// operator could add is an API one and none of those adapters is written yet
// (`internal/llm/catalogue.go` says why: an adapter written against
// documentation and never exercised fails at the first real call, or half-works
// and returns prose where a schema was asked for). So the route exists, takes
// the final body, resolves the catalogue entry, and answers with that entry's
// name and reason.
//
// When an adapter lands, this handler starts succeeding without its route, its
// body or the screen that calls it changing.
func (s *Server) handleAddConnection(w http.ResponseWriter, r *http.Request) {
	if s.deps.Connections == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable,
			"this daemon has no connection registry")
		return
	}
	var req addConnectionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	spec, err := s.deps.Connections.Add(strings.TrimSpace(req.Provider), strings.TrimSpace(req.Label))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, spec)
}

// handleProbeConnection asks whether one connection's login works.
//
// One row, because that is the question the screen asks. Probing everything at
// once was measured at four minutes — long enough that the operator concludes
// the button is broken — and it is also the wrong shape: each probe spends a
// model call, and spending four because somebody wanted to check one is not a
// thing to do quietly.
func (s *Server) handleProbeConnection(w http.ResponseWriter, r *http.Request) {
	if s.deps.LLM == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable,
			"this daemon has no model router")
		return
	}
	got, ok := s.deps.LLM.ProbeOne(r.Context(), r.PathValue("id"), s.cfg.LLMProbeTimeout)
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "no such connection")
		return
	}
	writeJSON(w, http.StatusOK, got)
}

// addConnectionRequest is the final body. `provider` names a catalogue entry,
// never an adapter and never a URL: an operator picks from what the daemon
// published, so nothing they type decides what runs.
type addConnectionRequest struct {
	Provider string `json:"provider"`
	Label    string `json:"label"`
}

// `?probe=1` asks whether the logins work rather than only whether the binaries
// are there. It is opt-in because it is not free: one completion per provider.
// A screen opening asks the cheap question; an operator pressing "test" asks
// the expensive one.
func (s *Server) handleListLLMProviders(w http.ResponseWriter, r *http.Request) {
	res := llmProviderListResponse{
		Providers: s.cfg.LLMProviders,
		Routed: llmRoutedDefault{
			Provider: s.cfg.DistillProvider,
			Model:    s.cfg.DistillModel,
		},
	}
	if s.deps.LLM != nil {
		res.Available = s.deps.LLM.Discover(r.Context(), r.URL.Query().Get("probe") == "1", s.cfg.LLMProbeTimeout)
	}
	writeJSON(w, http.StatusOK, res)
}

// llmSelection validates the operator's provider/model override.
//
// Rejecting rather than falling back to the default is the point. Both strings
// become argv to a subprocess, so nothing that is not in cfg.LLMProviders may
// pass; and a client that asked for a model this daemon does not offer has
// asked for something specific, so quietly running a different one would spend
// a budget nobody chose. A model without a provider is equally a mistake worth
// naming — the same model id can be reachable through more than one CLI.
//
// Shared by every endpoint that lets a client route a run — leadgen and the
// brain scan — because the allow-list argument is the same one twice, and a
// second copy is how one of them eventually stops rejecting something.
func (s *Server) llmSelection(w http.ResponseWriter, rawProvider, rawModel string) (llm.Selection, bool) {
	provider := strings.TrimSpace(rawProvider)
	model := strings.TrimSpace(rawModel)

	if provider == "" && model == "" {
		return llm.Selection{}, true
	}
	if provider == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest,
			"model was given without a provider — send both, or neither to route by class")
		return llm.Selection{}, false
	}
	if !s.allowsModel(provider, model) {
		writeError(w, http.StatusBadRequest, codeBadRequest,
			"unknown provider/model combination — GET /llm/providers lists what this daemon will run")
		return llm.Selection{}, false
	}
	if model == "" {
		model = s.defaultModel(provider)
	}
	return llm.Selection{Provider: provider, Model: model}, true
}

// allowsModel and defaultModel ask the connection registry when there is one,
// and the shipped table when there is not.
//
// The registry is where the allow-list lives once connections are the unit of
// routing — it knows the operator's connections as well as the built-ins, and
// the shipped table only knows half. A daemon built without one still answers
// correctly about the half it has, which is the same degradation `Deps.LLM`
// already has.
func (s *Server) allowsModel(provider, model string) bool {
	if s.deps.Connections != nil {
		return s.deps.Connections.Allows(provider, model)
	}
	return s.cfg.HasLLMModel(provider, model)
}

func (s *Server) defaultModel(provider string) string {
	if s.deps.Connections != nil {
		if m := s.deps.Connections.DefaultModel(provider); m != "" {
			return m
		}
	}
	return s.cfg.LLMDefaultModel(provider)
}

// leadgenSelection is llmSelection with the operator's saved default behind it.
//
// The lead-gen screen no longer carries a picker: the choice moved to the
// settings screen, because "which model writes my outreach" is a decision about
// a campaign and not about one search, and re-making it on every run is how it
// ends up different on two runs nobody meant to differ. A request that still
// names a provider wins — the per-run override is not gone, it is just no longer
// the only way to answer the question — and a request that names nothing gets
// what the operator saved, or class routing if they saved nothing.
func (s *Server) leadgenSelection(w http.ResponseWriter, rawProvider, rawModel string) (llm.Selection, bool) {
	if strings.TrimSpace(rawProvider) == "" && strings.TrimSpace(rawModel) == "" {
		rawProvider, rawModel = s.savedSelection()
	}
	return s.llmSelection(w, rawProvider, rawModel)
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
	sel, ok := s.leadgenSelection(w, req.Provider, req.Model)
	if !ok {
		return
	}

	report, err := s.deps.LeadGen.Run(r.Context(), leadgen.RunRequest{
		Query:           q,
		Region:          strings.TrimSpace(req.Region),
		WithContacts:    !req.SkipContacts,
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

// outreachRequest drafts messages for companies the operator picked by hand.
//
// It carries place ids rather than a search, and that is the whole difference
// between it and POST /maps/leadgen: a search is "find me companies", this is
// "write to these ones". The ids come from the ledger, which is where the
// checkbox column reads its rows from, so nothing here has to be trusted — an id
// the ledger does not hold simply is not written to.
type outreachRequest struct {
	PlaceIDs []string `json:"place_ids"`
	// Channels is what to write. Empty is email alone, which is what a client
	// written before WhatsApp existed means.
	Channels []string `json:"channels"`
	// Region labels the gap analysis. Empty lets the pipeline fall back to the
	// companies' own region, which is what the ledger's region filter already
	// shows the operator.
	Region   string `json:"region"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// handleDraftOutreach writes outreach for a chosen set of companies.
//
// The bound on how much this can cost is the request itself: one gap analysis
// per distinct category in the selection, then one draft per company per
// channel. That is why the route takes ids and not a filter — a filter would let
// a client spend a region's worth of tokens with one short string, and the
// operator would have no way to see beforehand how many companies that was.
func (s *Server) handleDraftOutreach(w http.ResponseWriter, r *http.Request) {
	var req outreachRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.PlaceIDs) == 0 {
		writeError(w, http.StatusBadRequest, codeBadRequest,
			"place_ids is required — pick the companies to write to")
		return
	}
	if len(req.PlaceIDs) > s.cfg.LeadsPageMax {
		writeError(w, http.StatusBadRequest, codeBadRequest,
			"too many companies in one request — select fewer than "+strconv.Itoa(s.cfg.LeadsPageMax))
		return
	}
	if s.deps.Leads == nil {
		writeError(w, http.StatusInternalServerError, codeInternal,
			"the lead ledger is not available on this daemon")
		return
	}

	channels := make([]settings.Channel, 0, len(req.Channels))
	for _, raw := range req.Channels {
		ch, ok := s.channel(w, raw)
		if !ok {
			return
		}
		channels = append(channels, ch)
	}

	sel, ok := s.leadgenSelection(w, req.Provider, req.Model)
	if !ok {
		return
	}

	rows, err := s.deps.Leads.LeadsByPlaceID(r.Context(), req.PlaceIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, err.Error())
		return
	}
	if len(rows) == 0 {
		writeError(w, http.StatusNotFound, codeNotFound,
			"none of those companies are in the ledger — run a search for them first")
		return
	}

	companies := make([]maps.Company, 0, len(rows))
	categories := make([]leadgen.Category, 0, len(rows))
	for _, row := range rows {
		companies = append(companies, maps.Company{
			PlaceID:          row.PlaceID,
			Name:             row.Name,
			FormattedAddress: row.Address,
			Latitude:         row.Latitude,
			Longitude:        row.Longitude,
			Rating:           row.Rating,
			ReviewCount:      row.ReviewCount,
			Website:          row.Website,
			Phone:            row.Phone,
			PrimaryType:      row.PrimaryType,
			BusinessStatus:   row.BusinessStatus,
			Source:           row.Source,
		})
		categories = append(categories, leadgen.Category(row.Category))
	}

	result, err := s.deps.LeadGen.DraftOutreach(r.Context(), leadgen.OutreachRequest{
		Companies:  companies,
		Categories: categories,
		Region:     strings.TrimSpace(req.Region),
		Channels:   channels,
		Selection:  sel,
	})
	if err != nil {
		writeLeadgenError(w, r, err)
		return
	}

	// The ledger holds the address an email goes to; the pipeline was handed
	// companies, which do not carry one. Put it back before answering, so the
	// screen can offer "send" beside a draft rather than beside nothing.
	emails := make(map[string]string, len(rows))
	for _, row := range rows {
		emails[row.PlaceID] = row.Email
	}
	for i := range result.Companies {
		result.Companies[i].Email = emails[result.Companies[i].PlaceID]
	}

	writeJSON(w, http.StatusOK, result)
}

type outreachStatusRequest struct {
	PlaceID string `json:"place_id"`
	Channel string `json:"channel"`
	Status  string `json:"status"`
}

// handleSetOutreachStatus records a human's decision on a drafted message.
//
// The prompt version is not a client field, and now it is not a server constant
// either: a draft's version is composed from the model and the rule file it was
// written under, so the row a client means by "the one I am looking at" is the
// newest draft that company has on that channel. The store resolves it; see the
// note above SetOutreachStatus for why reading and writing key differently.
func (s *Server) handleSetOutreachStatus(w http.ResponseWriter, r *http.Request) {
	var req outreachStatusRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.PlaceID) == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "place_id is required")
		return
	}
	ch, ok := s.channel(w, req.Channel)
	if !ok {
		return
	}
	if s.deps.Outreach == nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "outreach status is not available on this daemon")
		return
	}

	err := s.deps.Outreach.SetOutreachStatus(r.Context(), req.PlaceID, string(ch), req.Status)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, store.ErrOutreachStatusInvalid):
		writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
	case errors.Is(err, store.ErrOutreachNotFound):
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
	// Email is the company's own address — where a draft is sent, not the draft.
	Email string `json:"email,omitempty"`
	// Drafts is what has been written for this company, newest per channel.
	Drafts      []savedDraft `json:"drafts,omitempty"`
	FirstSeenAt int64        `json:"first_seen_at"`
	LastSeenAt  int64        `json:"last_seen_at"`
}

// savedDraft mirrors leadgen.Draft so a client renders one draft shape whether
// it came from a run or from the ledger.
type savedDraft struct {
	Channel   string `json:"channel"`
	Body      string `json:"body"`
	Status    string `json:"status,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
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
		Region:         strings.TrimSpace(q.Get("region")),
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
	drafts, err := s.deps.Leads.OutreachMessagesFor(r.Context(), ids)
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
			Email:          row.Email,
			PrimaryType:    row.PrimaryType,
			BusinessStatus: row.BusinessStatus,
			Source:         row.Source,
			Category:       row.Category,
			CategoryMethod: row.CategoryMethod,
			FirstSeenAt:    row.FirstSeenAt.Unix(),
			LastSeenAt:     row.LastSeenAt.Unix(),
		}
		for _, d := range drafts[row.PlaceID] {
			l.Drafts = append(l.Drafts, savedDraft{
				Channel:   d.Channel,
				Body:      d.Body,
				Status:    d.Status,
				Truncated: d.Truncated,
			})
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

// leadRegionView is one place the ledger holds leads for.
type leadRegionView struct {
	Region    string `json:"region"`
	Companies int    `json:"companies"`
	Runs      int    `json:"runs"`
	WithPhone int    `json:"with_phone"`
	WithSite  int    `json:"with_site"`
	LastRanAt string `json:"last_ran_at,omitempty"`
}

// handleListLeadRegions rolls the run history up by place.
//
// The picker's unit, and the reason it exists: a region searched seventeen
// times is one region. Listing runs put seventeen near-identical "Denizli" rows
// in front of an operator who has exactly one Denizli.
func (s *Server) handleListLeadRegions(w http.ResponseWriter, r *http.Request) {
	regions, err := s.deps.Leads.ListLeadRegions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, err.Error())
		return
	}

	out := make([]leadRegionView, 0, len(regions))
	for _, g := range regions {
		view := leadRegionView{
			Region:    g.Region,
			Companies: g.Companies,
			Runs:      g.Runs,
			WithPhone: g.WithPhone,
			WithSite:  g.WithSite,
		}
		if !g.LastRanAt.IsZero() {
			view.LastRanAt = g.LastRanAt.Format(time.RFC3339)
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"regions": out})
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
