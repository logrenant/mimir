package leadgen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/maps"
	"github.com/logrenant/goat-mcp/internal/refine"
	"github.com/logrenant/goat-mcp/internal/store"
)

// GapAnalyzer is the model tier for stage 3. *refine.Client satisfies it.
type GapAnalyzer interface {
	AnalyzeGaps(ctx context.Context, in refine.GapInput) (refine.Output, error)
}

// GapStore is the cache tier for stage 3. *store.Store satisfies it, and a nil
// GapStore is legal — the analysis then costs its tokens every time (SD-6).
type GapStore interface {
	GetGapAnalysis(ctx context.Context, key store.GapAnalysisKey) (store.GapAnalysis, bool, error)
	PutGapAnalysis(ctx context.Context, key store.GapAnalysisKey, ga store.GapAnalysis) error
}

// GapResult is one category's synthesized gaps and needs. Method says where it
// came from, using the same vocabulary as categorization's Result.Method.
type GapResult struct {
	Region       string
	Category     Category
	Analysis     string
	CompanyCount int
	Truncated    bool
	Method       string // MethodCache | MethodModel | MethodUnresolved
}

// GapAnalyzerRunner resolves one category to a gap analysis through two tiers,
// cheapest first: the cache, then the model. There is no free rule tier here —
// a synthesis is generative by nature — but the cache makes a re-run of the
// same region free, which is the point.
type GapAnalyzerRunner struct {
	cfg      config.Config
	analyzer GapAnalyzer
	store    GapStore
}

// NewGapAnalyzer wires the runner. analyzer or s may be nil; a nil analyzer
// means every category comes back unresolved, a nil store means nothing is
// cached.
func NewGapAnalyzer(cfg config.Config, analyzer GapAnalyzer, s GapStore) *GapAnalyzerRunner {
	return &GapAnalyzerRunner{cfg: cfg, analyzer: analyzer, store: s}
}

// AnalyzeCategory synthesizes the gaps and needs common to companies in one
// region/category.
//
// gaps names what could not be resolved and why; it is diagnostic, never a
// reason to fail. Too few companies, a failing cache, a failing subprocess, or
// an empty model answer all degrade to an unresolved GapResult for this
// category (SD-6). Only the caller's own cancellation returns an error.
func (r *GapAnalyzerRunner) AnalyzeCategory(ctx context.Context, region string, cat Category, companies []maps.Company) (GapResult, []string, error) {
	res := GapResult{Region: region, Category: cat, Method: MethodUnresolved}
	if err := ctx.Err(); err != nil {
		return GapResult{}, nil, err
	}

	var gaps []string

	minCompanies := r.cfg.LeadgenGapMinCompanies
	if minCompanies <= 0 {
		minCompanies = 3
	}
	if len(companies) < minCompanies {
		gaps = append(gaps, fmt.Sprintf("gap analysis skipped for %s/%s: %d companies, need at least %d",
			region, cat, len(companies), minCompanies))
		return res, gaps, nil
	}

	// Sort a copy so the same set of companies produces the same prompt and the
	// same cache key regardless of the order the caller passed them in.
	sorted := make([]maps.Company, len(companies))
	copy(sorted, companies)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].PlaceID != sorted[j].PlaceID {
			return sorted[i].PlaceID < sorted[j].PlaceID
		}
		return sorted[i].Name < sorted[j].Name
	})

	key := store.GapAnalysisKey{
		Region:         region,
		Category:       string(cat),
		PromptVersion:  r.cfg.LeadgenGapVersion,
		CompanySetHash: companySetHash(sorted),
	}

	// Tier 1: the cache.
	if r.store != nil {
		hit, ok, err := r.store.GetGapAnalysis(ctx, key)
		if err != nil {
			gaps = append(gaps, "gap analysis cache unavailable: "+err.Error())
		} else if ok {
			res.Analysis = hit.Analysis
			res.CompanyCount = hit.CompanyCount
			res.Truncated = hit.Truncated
			res.Method = MethodCache
			return res, gaps, nil
		}
	}

	if r.analyzer == nil {
		gaps = append(gaps, fmt.Sprintf("gap analysis for %s/%s left unresolved: no analyzer configured", region, cat))
		return res, gaps, nil
	}

	// Tier 2: the model. Fed only pre-computed facts — no raw page text.
	out, err := r.analyzer.AnalyzeGaps(ctx, refine.GapInput{
		Region:    region,
		Category:  string(cat),
		Companies: gapFacts(sorted),
		MaxTokens: r.cfg.LeadgenGapMaxTokens,
	})
	if err != nil {
		// A cancelled caller is the one failure that is not a gap: nobody is
		// waiting for this answer any more.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return GapResult{}, nil, ctxErr
		}
		gaps = append(gaps, fmt.Sprintf("gap analysis for %s/%s failed: %v", region, cat, err))
		return res, gaps, nil
	}
	if !out.Refined || strings.TrimSpace(out.Text) == "" {
		gaps = append(gaps, fmt.Sprintf("gap analysis for %s/%s returned nothing usable", region, cat))
		return res, gaps, nil
	}

	res.Analysis = out.Text
	res.CompanyCount = len(sorted)
	res.Truncated = out.Truncated
	res.Method = MethodModel

	if r.store != nil {
		if err := r.store.PutGapAnalysis(ctx, key, store.GapAnalysis{
			Analysis:     res.Analysis,
			CompanyCount: res.CompanyCount,
			Truncated:    res.Truncated,
		}); err != nil {
			gaps = append(gaps, "caching gap analysis failed: "+err.Error())
		}
	}

	return res, gaps, nil
}

// gapFacts reduces each company to the deterministic signals the synthesis
// needs. Everything here is computed locally: no field is model-written, and
// only Name is provider-controlled free text (internal/refine fences it).
func gapFacts(companies []maps.Company) []refine.GapCompany {
	out := make([]refine.GapCompany, 0, len(companies))
	for _, co := range companies {
		out = append(out, refine.GapCompany{
			Name:        co.Name,
			HasWebsite:  strings.TrimSpace(co.Website) != "",
			HasPhone:    strings.TrimSpace(co.Phone) != "",
			Rating:      co.Rating,
			ReviewCount: co.ReviewCount,
			Status:      co.BusinessStatus,
		})
	}
	return out
}

// companySetHash is sha256 over the sorted identifiers of the set, hex-encoded.
// A company with no place_id (a scrape fallback row) still contributes a stable
// identifier so two runs over the same list hash the same.
func companySetHash(companies []maps.Company) string {
	ids := make([]string, 0, len(companies))
	for _, co := range companies {
		id := co.PlaceID
		if id == "" {
			id = "name:" + co.Name
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)

	h := sha256.New()
	for _, id := range ids {
		_, _ = h.Write([]byte(id))
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}
