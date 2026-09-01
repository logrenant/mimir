package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/mcp"
)

// RegionSearcher is the internal/maps seam. An interface rather than the
// concrete client so this tool's tests need neither a Places key nor a
// network — the same shape every other tool here uses.
type RegionSearcher interface {
	SearchText(ctx context.Context, q maps.Query) ([]maps.Company, error)
}

// MapsSearchTool answers "which companies exist in this region?" from the
// Google Places API.
//
// It is the only tool in the repo that spends the operator's money: Places
// bills per request by field-mask tier. Two consequences are deliberate and
// visible rather than clever — it is registered only when a key is present
// (see RegisterAll), and it does not consult the region cache, because
// internal/leadgen (M5) is the orchestrator that owns that decision, exactly
// as internal/pipeline — not internal/crawl — owns the page cache.
//
// No refine call: Places returns structured business facts and maps.FieldMask
// requests no free-text field, so there is nothing here for the
// context-isolation firewall to distil (the same reasoning the Stage F
// scrapers use).
type MapsSearchTool struct {
	searcher RegionSearcher
	cfg      config.Config
}

// NewMapsSearch creates a new MapsSearchTool.
func NewMapsSearch(cfg config.Config, searcher RegionSearcher) *MapsSearchTool {
	return &MapsSearchTool{searcher: searcher, cfg: cfg}
}

func (t *MapsSearchTool) Name() string { return "maps_search" }

func (t *MapsSearchTool) Description() string {
	return "Find companies in a region via the Google Places API — name, address, coordinates, rating, review count, website, phone. Structured facts only, no page text. Costs money: each call is a billed Places request and results are not cached yet, so do not repeat a search you already ran."
}

func (t *MapsSearchTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": { "type": "string", "minLength": 1, "description": "What you would type into Google Maps, e.g. 'dentists in Kadıköy, Istanbul'" },
			"count": { "type": "integer", "minimum": 1, "maximum": 60, "description": "How many companies to collect. Default 20, hard ceiling 60 (the API's own limit)." },
			"language_code": { "type": "string", "description": "Language of returned names and addresses, e.g. 'tr', 'en'" },
			"region_code": { "type": "string", "description": "CLDR region biasing result formatting, e.g. 'TR', 'US'" },
			"near": {
				"type": "object",
				"description": "Optional circle to bias results towards. All three fields are required together.",
				"properties": {
					"latitude": { "type": "number" },
					"longitude": { "type": "number" },
					"radius_meters": { "type": "number", "exclusiveMinimum": 0 }
				},
				"required": ["latitude", "longitude", "radius_meters"],
				"additionalProperties": false
			}
		},
		"required": ["query"],
		"additionalProperties": false
	}`)
}

type mapsSearchArgs struct {
	Query        string       `json:"query"`
	Count        int          `json:"count"`
	LanguageCode string       `json:"language_code"`
	RegionCode   string       `json:"region_code"`
	Near         *mapsNearArg `json:"near"`
}

type mapsNearArg struct {
	Latitude     float64 `json:"latitude"`
	Longitude    float64 `json:"longitude"`
	RadiusMeters float64 `json:"radius_meters"`
}

// mapsCompany is the wire shape of one company.
//
// It is narrower than maps.Company on purpose: Google's types[] is the input
// to internal/leadgen's category rule table, not something the consumer
// reads, and it is ~80 bytes of budget per row.
type mapsCompany struct {
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
}

type mapsSearchResponse struct {
	Query string `json:"query"`
	// Returned is how many companies this response carries; TotalFound is how
	// many Places returned. They differ when the budget forced a trim.
	Returned   int           `json:"returned"`
	TotalFound int           `json:"total_found"`
	Truncated  bool          `json:"truncated"`
	Companies  []mapsCompany `json:"companies"`

	budget int `json:"-"`
}

func (r mapsSearchResponse) MetadataOnly() bool    { return true }
func (r mapsSearchResponse) SizeBudgetTokens() int { return r.budget }

func (t *MapsSearchTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var input mapsSearchArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	// Checked before the call, not after: a blank query is a caller bug, and
	// finding that out from a billed 400 would be an odd way to learn it.
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return nil, errors.New("query must not be empty — pass what you would type into Google Maps")
	}

	q := maps.Query{
		Text:         query,
		LanguageCode: input.LanguageCode,
		RegionCode:   input.RegionCode,
		MaxResults:   t.clampCount(input.Count),
	}
	if input.Near != nil {
		if input.Near.RadiusMeters <= 0 {
			return nil, errors.New("near.radius_meters must be greater than 0")
		}
		q.Bias = &maps.Circle{
			Latitude:     input.Near.Latitude,
			Longitude:    input.Near.Longitude,
			RadiusMeters: input.Near.RadiusMeters,
		}
	}

	found, err := t.searcher.SearchText(ctx, q)
	if err != nil {
		return nil, mapsToolError(err)
	}

	companies := make([]mapsCompany, 0, len(found))
	for _, c := range found {
		companies = append(companies, mapsCompany{
			PlaceID:        c.PlaceID,
			Name:           c.Name,
			Address:        c.FormattedAddress,
			Latitude:       c.Latitude,
			Longitude:      c.Longitude,
			Rating:         c.Rating,
			ReviewCount:    c.ReviewCount,
			Website:        c.Website,
			Phone:          c.Phone,
			PrimaryType:    c.PrimaryType,
			BusinessStatus: c.BusinessStatus,
		})
	}

	resp := mapsSearchResponse{
		Query:      query,
		TotalFound: len(companies),
		Companies:  companies,
		budget:     t.cfg.MapsSearchMaxTokens,
	}
	// SD-7: 60 companies do not fit the ceiling, and the choke-point must
	// never be the thing that discovers that — it fails the whole call, which
	// would turn a paid search into nothing at all. Trim here instead, and
	// report what was dropped.
	resp.Companies = trimToBudget(resp.Companies, resp)
	resp.Returned = len(resp.Companies)
	resp.Truncated = resp.Returned < resp.TotalFound

	return resp, nil
}

// clampCount resolves the caller's count against the standardized default and
// the API's ceiling. Out-of-range values are clamped, not rejected: the schema
// already states the range, and a caller asking for 100 wants "as many as you
// can".
func (t *MapsSearchTool) clampCount(count int) int {
	switch {
	case count <= 0:
		return t.cfg.MapsSearchDefaultCount
	case count > t.cfg.MapsSearchMaxCount:
		return t.cfg.MapsSearchMaxCount
	default:
		return count
	}
}

// trimToBudget drops companies from the tail until the marshalled response
// fits its token budget. Places ranks by relevance, so the tail is the right
// end to lose.
func trimToBudget(companies []mapsCompany, resp mapsSearchResponse) []mapsCompany {
	if resp.budget <= 0 {
		return companies
	}
	for len(companies) > 0 {
		probe := resp
		probe.Companies = companies
		probe.Returned = len(companies)
		probe.Truncated = len(companies) < resp.TotalFound

		b, err := json.Marshal(probe)
		if err != nil {
			// Unmarshalable is not a size problem; let the choke-point speak.
			return companies
		}
		if len(b)/4 <= resp.budget {
			break
		}
		companies = companies[:len(companies)-1]
	}
	return companies
}

// mapsToolError turns a maps sentinel into the fix the operator has to apply
// (SD-6). None of these messages can carry the key: internal/maps never puts
// it in an error, and nothing here reads it.
func mapsToolError(err error) error {
	switch {
	case errors.Is(err, maps.ErrCredentialMissing):
		return errors.New("no Google Places API key — set MIMIR_GOOGLE_PLACES_API_KEY and restart; it is provisioned by the operator, not passed per call")
	case errors.Is(err, maps.ErrRequestDenied):
		return errors.New("the Places key was rejected — check it is valid, that the Places API (New) is enabled for its project, and that any key restriction allows this machine")
	case errors.Is(err, maps.ErrQuotaExceeded):
		return errors.New("the Places quota or billing cap is spent — raise the quota or wait for the window to reset")
	case errors.Is(err, maps.ErrBadRequest):
		return errors.New("the Places API rejected this query — rephrase it, or drop the near/language/region hints")
	case errors.Is(err, maps.ErrUnavailable):
		return errors.New("the Places API is unreachable — check network access, then retry")
	default:
		return err
	}
}

var _ mcp.Tool = (*MapsSearchTool)(nil)
