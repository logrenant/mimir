package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/logrenant/goat-mcp/internal/leadgen"
	"github.com/logrenant/goat-mcp/internal/maps"
	"github.com/logrenant/goat-mcp/internal/store"
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
}

// handleLeadgen runs the full Maps lead-gen pipeline: region search →
// categorize → (optional) per-category gap analysis → (optional) per-company
// outreach email. Stages 1–2 always run; 3–4 are opt-in per request because
// they spend Claude tokens.
//
// The response is the pipeline's Report verbatim — this handler is a door, not
// a floor: internal/leadgen owns every decision about caching, cost, and
// degradation, and its Notes field already carries the diagnostic gaps.
func (s *Server) handleLeadgen(w http.ResponseWriter, r *http.Request) {
	var req leadgenRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "query is required — pass what you would type into Google Maps")
		return
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
			return
		}
		q.Bias = &maps.Circle{
			Latitude:     req.Near.Latitude,
			Longitude:    req.Near.Longitude,
			RadiusMeters: req.Near.RadiusMeters,
		}
	}

	report, err := s.deps.LeadGen.Run(r.Context(), leadgen.RunRequest{
		Query:           q,
		Region:          strings.TrimSpace(req.Region),
		WithGapAnalysis: req.GapAnalysis,
		WithEmails:      req.Emails,
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
