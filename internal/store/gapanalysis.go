package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// GapAnalysis is one cached category-level synthesis (internal/leadgen stage 3).
type GapAnalysis struct {
	Analysis     string
	CompanyCount int
	Truncated    bool
}

// GapAnalysisKey is the total cache key. The analysis is a function of the
// region, the category, the gap-analysis prompt version, and the exact set of
// companies it was synthesized across — CompanySetHash carries the last one
// (sha256 over the sorted place_id list, computed by the caller).
type GapAnalysisKey struct {
	Region         string
	Category       string
	PromptVersion  string
	CompanySetHash string
}

func (k GapAnalysisKey) valid() bool {
	return k.Region != "" && k.Category != "" && k.PromptVersion != "" && k.CompanySetHash != ""
}

// GetGapAnalysis returns the cached synthesis for key, or ok=false on a miss.
// Nil-Store tolerant: no cache is a slower run, never a failed one (SD-6).
func (s *Store) GetGapAnalysis(ctx context.Context, key GapAnalysisKey) (GapAnalysis, bool, error) {
	if s == nil || s.db == nil || !key.valid() {
		return GapAnalysis{}, false, nil
	}

	var (
		ga        GapAnalysis
		truncated int
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT analysis, company_count, truncated
		FROM category_gap_analysis
		WHERE region = ? AND category = ? AND prompt_version = ? AND company_set_hash = ?`,
		key.Region, key.Category, key.PromptVersion, key.CompanySetHash,
	).Scan(&ga.Analysis, &ga.CompanyCount, &truncated)
	if errors.Is(err, sql.ErrNoRows) {
		return GapAnalysis{}, false, nil
	}
	if err != nil {
		return GapAnalysis{}, false, unavailable(err)
	}
	ga.Truncated = truncated != 0
	return ga, true, nil
}

// PutGapAnalysis stores one synthesis. Re-writing the same key replaces it — a
// re-run with the same company set and prompt version should not need a delete
// first. An empty analysis is dropped: replaying it would hand a later stage
// nothing where it expects a paragraph.
func (s *Store) PutGapAnalysis(ctx context.Context, key GapAnalysisKey, ga GapAnalysis) error {
	if s == nil || s.db == nil || !key.valid() {
		return nil
	}
	if ga.Analysis == "" {
		return nil
	}

	truncated := 0
	if ga.Truncated {
		truncated = 1
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO category_gap_analysis
			(region, category, prompt_version, company_set_hash, analysis, company_count, truncated, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(region, category, prompt_version, company_set_hash) DO UPDATE SET
			analysis      = excluded.analysis,
			company_count = excluded.company_count,
			truncated     = excluded.truncated,
			created_at    = excluded.created_at`,
		key.Region, key.Category, key.PromptVersion, key.CompanySetHash,
		ga.Analysis, ga.CompanyCount, truncated, time.Now().Unix())
	if err != nil {
		return unavailable(err)
	}
	return nil
}
