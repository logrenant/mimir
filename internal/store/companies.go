package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/logrenant/mimir/internal/maps"
)

// companyColumns is the single column list every company read shares, so a
// scan and its SELECT cannot drift apart.
const companyColumns = `place_id, name, formatted_address, latitude, longitude,
	rating, review_count, website, phone, types, primary_type, business_status,
	source, fetched_at, cached_at`

// PutCompany stores (or refreshes) one company.
//
// Nil-Store tolerant like the rest of this package: no cache is a slower run,
// never a failed one (SD-6).
func (s *Store) PutCompany(ctx context.Context, c maps.Company) error {
	if s == nil || s.db == nil {
		return nil
	}
	if c.PlaceID == "" {
		return nil
	}
	return execPutCompany(ctx, s.db, c, time.Now().Unix())
}

// execer is the subset of *sql.DB and *sql.Tx PutCompany needs, so the
// single-row and transactional paths share one statement.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func execPutCompany(ctx context.Context, db execer, c maps.Company, cachedAt int64) error {
	types, err := json.Marshal(c.Types)
	if err != nil {
		return unavailable(err)
	}

	fetchedAt := c.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = time.Now()
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO companies
			(place_id, name, formatted_address, latitude, longitude, rating,
			 review_count, website, phone, types, primary_type, business_status,
			 source, fetched_at, cached_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(place_id) DO UPDATE SET
			name              = excluded.name,
			formatted_address = excluded.formatted_address,
			latitude          = excluded.latitude,
			longitude         = excluded.longitude,
			rating            = excluded.rating,
			review_count      = excluded.review_count,
			website           = excluded.website,
			phone             = excluded.phone,
			types             = excluded.types,
			primary_type      = excluded.primary_type,
			business_status   = excluded.business_status,
			source            = excluded.source,
			fetched_at        = excluded.fetched_at,
			cached_at         = excluded.cached_at`,
		c.PlaceID, c.Name, c.FormattedAddress, c.Latitude, c.Longitude, c.Rating,
		c.ReviewCount, c.Website, c.Phone, string(types), c.PrimaryType,
		c.BusinessStatus, c.Source, fetchedAt.Unix(), cachedAt)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

func scanCompany(scan func(dest ...any) error) (maps.Company, int64, error) {
	var (
		c                   maps.Company
		types               string
		fetchedAt, cachedAt int64
	)
	err := scan(
		&c.PlaceID, &c.Name, &c.FormattedAddress, &c.Latitude, &c.Longitude,
		&c.Rating, &c.ReviewCount, &c.Website, &c.Phone, &types, &c.PrimaryType,
		&c.BusinessStatus, &c.Source, &fetchedAt, &cachedAt,
	)
	if err != nil {
		return maps.Company{}, 0, err
	}
	if types != "" {
		// A row written by a future/foreign writer with unparseable types is
		// still a usable company: drop the taxonomy, keep the business.
		_ = json.Unmarshal([]byte(types), &c.Types)
	}
	c.FetchedAt = time.Unix(fetchedAt, 0).UTC()
	return c, cachedAt, nil
}

// GetCompany returns the cached company for placeID, if present and within
// ttl. The TTL is the caller's (a company changes on a scale of months, a page
// on a scale of hours) — same shape as GetCrawl.
func (s *Store) GetCompany(ctx context.Context, placeID string, ttl time.Duration) (maps.Company, bool, error) {
	if s == nil || s.db == nil {
		return maps.Company{}, false, nil
	}

	row := s.db.QueryRowContext(ctx,
		`SELECT `+companyColumns+` FROM companies WHERE place_id = ?`, placeID)

	c, cachedAt, err := scanCompany(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return maps.Company{}, false, nil
	}
	if err != nil {
		return maps.Company{}, false, unavailable(err)
	}
	if expired(cachedAt, ttl) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM companies WHERE place_id = ?`, placeID)
		return maps.Company{}, false, nil
	}
	return c, true, nil
}

// PutRegionSearch records one completed region search: every company it
// returned, plus the ordered place_id list under regionKey
// (maps.Query.Key()).
//
// Written in one transaction on purpose. A half-written region — the search
// row present, some company rows missing — would read back as a hit with
// silently fewer companies, which is worse than a miss.
func (s *Store) PutRegionSearch(ctx context.Context, regionKey, query string, cs []maps.Company) error {
	if s == nil || s.db == nil {
		return nil
	}
	if regionKey == "" {
		return nil
	}

	ids := make([]string, 0, len(cs))
	for _, c := range cs {
		if c.PlaceID != "" {
			ids = append(ids, c.PlaceID)
		}
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return unavailable(err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return unavailable(err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().Unix()
	for _, c := range cs {
		if c.PlaceID == "" {
			continue
		}
		if err := execPutCompany(ctx, tx, c, now); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO region_searches (region_key, query, place_ids, cached_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(region_key) DO UPDATE SET
			query     = excluded.query,
			place_ids = excluded.place_ids,
			cached_at = excluded.cached_at`,
		regionKey, query, string(encoded), now); err != nil {
		return unavailable(err)
	}

	if err := tx.Commit(); err != nil {
		return unavailable(err)
	}
	return nil
}

// GetRegionSearch replays a cached region search in its original result order.
//
// All-or-nothing: if the search row is missing or expired, or any company it
// named is missing or expired, this is a miss. Returning a partial region
// would quietly shrink a lead list — the caller must re-run the search to know
// it has the whole region.
//
// A search that legitimately found nothing is a HIT with an empty slice: that
// is the answer, and re-asking Places for it costs money.
func (s *Store) GetRegionSearch(ctx context.Context, regionKey string, ttl time.Duration) ([]maps.Company, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, nil
	}

	var (
		encoded  string
		cachedAt int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT place_ids, cached_at FROM region_searches WHERE region_key = ?`, regionKey,
	).Scan(&encoded, &cachedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, unavailable(err)
	}
	if expired(cachedAt, ttl) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM region_searches WHERE region_key = ?`, regionKey)
		return nil, false, nil
	}

	var ids []string
	if err := json.Unmarshal([]byte(encoded), &ids); err != nil {
		return nil, false, unavailable(err)
	}

	out := make([]maps.Company, 0, len(ids))
	for _, id := range ids {
		c, ok, err := s.GetCompany(ctx, id, ttl)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		out = append(out, c)
	}
	return out, true, nil
}
