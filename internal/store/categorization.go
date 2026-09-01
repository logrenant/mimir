package store

import (
	"context"
	"strings"
	"time"
)

// PutCategorization records one company's normalized category under a taxonomy
// version. Re-writing the same (place_id, version) replaces it — a re-run with
// a fixed rule table should not need a delete first.
//
// Nil-Store tolerant like the rest of this package: no cache is a slower run,
// never a failed one (SD-6).
func (s *Store) PutCategorization(ctx context.Context, placeID, version, category, method string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if placeID == "" || version == "" || category == "" {
		return nil
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO company_categorization (place_id, version, category, method, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(place_id, version) DO UPDATE SET
			category   = excluded.category,
			method     = excluded.method,
			created_at = excluded.created_at`,
		placeID, version, category, method, time.Now().Unix())
	if err != nil {
		return unavailable(err)
	}
	return nil
}

// GetCategorizations returns the cached categories for placeIDs at version,
// keyed by place_id. Ids with no row are simply absent.
//
// One query, not one per company: a region is hundreds of companies and the
// whole point of this tier is that a repeat run is nearly free.
func (s *Store) GetCategorizations(ctx context.Context, placeIDs []string, version string) (map[string]string, error) {
	out := make(map[string]string, len(placeIDs))
	if s == nil || s.db == nil || len(placeIDs) == 0 || version == "" {
		return out, nil
	}

	// Chunked because SQLite caps the number of bound parameters per statement
	// and a region can be hundreds of companies. Still far fewer round trips
	// than one query per id.
	const chunk = 500
	for start := 0; start < len(placeIDs); start += chunk {
		end := min(start+chunk, len(placeIDs))
		if err := s.readCategorizations(ctx, placeIDs[start:end], version, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) readCategorizations(ctx context.Context, placeIDs []string, version string, out map[string]string) error {
	args := make([]any, 0, len(placeIDs)+1)
	args = append(args, version)
	for _, id := range placeIDs {
		args = append(args, id)
	}

	query := `SELECT place_id, category FROM company_categorization
		WHERE version = ? AND place_id IN (?` +
		strings.Repeat(", ?", len(placeIDs)-1) + `)`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id, category string
		if err := rows.Scan(&id, &category); err != nil {
			return unavailable(err)
		}
		out[id] = category
	}
	if err := rows.Err(); err != nil {
		return unavailable(err)
	}
	return nil
}
