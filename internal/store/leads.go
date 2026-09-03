package store

import (
	"context"
	"strings"
	"time"
)

// leadColumns is the single column list every ledger read shares, so a scan and
// its SELECT cannot drift apart.
const leadColumns = `place_id, name, address, latitude, longitude, rating,
	review_count, website, phone, primary_type, business_status, source,
	category, category_method, first_seen_at, last_seen_at`

// LeadRow is one business in the ledger. Unlike the companies cache this row
// has no TTL and is never deleted by a reader: it is a record of what was
// found, not a saving on what it cost to find.
type LeadRow struct {
	PlaceID        string
	Name           string
	Address        string
	Latitude       float64
	Longitude      float64
	Rating         float64
	ReviewCount    int
	Website        string
	Phone          string
	PrimaryType    string
	BusinessStatus string
	Source         string
	Category       string
	CategoryMethod string
	FirstSeenAt    time.Time
	LastSeenAt     time.Time
}

// LeadRun is one pipeline invocation: which search, when, and what it returned.
type LeadRun struct {
	ID           string
	RegionKey    string
	Query        string
	RegionLabel  string
	Source       string
	CompanyCount int
	WithGaps     bool
	WithEmails   bool
	RanAt        time.Time
}

// LeadFilter narrows a ledger read. A zero filter is "everything, newest
// first", bounded by Limit.
type LeadFilter struct {
	Category       string
	RunID          string
	Text           string
	WithoutWebsite bool
	Limit          int
	Offset         int
}

// CategoryCount is one row of the category rail.
type CategoryCount struct {
	Category       string
	Companies      int
	WithoutWebsite int
}

// PutLeadRun records one lead-gen run and the businesses it returned, in one
// transaction: the run row, the lead upserts, and the memberships either all
// land or none do, so a run can never name businesses the ledger does not hold.
//
// The upsert never overwrites a populated column with an empty one. The free
// scrape returns no phone where the billed Places call did, and a later re-run
// through the cheaper provider must not erase a number already known.
//
// Nil-Store tolerant (SD-6): with no store the run still answered, it is just
// not remembered.
func (s *Store) PutLeadRun(ctx context.Context, run LeadRun, rows []LeadRow) error {
	if s == nil || s.db == nil || run.ID == "" {
		return nil
	}

	ranAt := run.RanAt
	if ranAt.IsZero() {
		ranAt = time.Now()
	}
	now := ranAt.UTC().Unix()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return unavailable(err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO lead_runs
			(id, region_key, query, region_label, source, company_count,
			 with_gaps, with_emails, ran_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			region_key    = excluded.region_key,
			query         = excluded.query,
			region_label  = excluded.region_label,
			source        = excluded.source,
			company_count = excluded.company_count,
			with_gaps     = excluded.with_gaps,
			with_emails   = excluded.with_emails,
			ran_at        = excluded.ran_at`,
		run.ID, run.RegionKey, run.Query, run.RegionLabel, run.Source,
		run.CompanyCount, boolInt(run.WithGaps), boolInt(run.WithEmails), now,
	); err != nil {
		return unavailable(err)
	}

	for _, r := range rows {
		if r.PlaceID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO leads (`+leadColumns+`)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(place_id) DO UPDATE SET
				name            = CASE WHEN excluded.name            <> '' THEN excluded.name            ELSE leads.name            END,
				address         = CASE WHEN excluded.address         <> '' THEN excluded.address         ELSE leads.address         END,
				latitude        = CASE WHEN excluded.latitude        <> 0  THEN excluded.latitude        ELSE leads.latitude        END,
				longitude       = CASE WHEN excluded.longitude       <> 0  THEN excluded.longitude       ELSE leads.longitude       END,
				rating          = CASE WHEN excluded.rating          <> 0  THEN excluded.rating          ELSE leads.rating          END,
				review_count    = CASE WHEN excluded.review_count    <> 0  THEN excluded.review_count    ELSE leads.review_count    END,
				website         = CASE WHEN excluded.website         <> '' THEN excluded.website         ELSE leads.website         END,
				phone           = CASE WHEN excluded.phone           <> '' THEN excluded.phone           ELSE leads.phone           END,
				primary_type    = CASE WHEN excluded.primary_type    <> '' THEN excluded.primary_type    ELSE leads.primary_type    END,
				business_status = CASE WHEN excluded.business_status <> '' THEN excluded.business_status ELSE leads.business_status END,
				source          = CASE WHEN excluded.source          <> '' THEN excluded.source          ELSE leads.source          END,
				category        = CASE WHEN excluded.category        <> '' THEN excluded.category        ELSE leads.category        END,
				category_method = CASE WHEN excluded.category_method <> '' THEN excluded.category_method ELSE leads.category_method END,
				last_seen_at    = excluded.last_seen_at`,
			r.PlaceID, r.Name, r.Address, r.Latitude, r.Longitude, r.Rating,
			r.ReviewCount, r.Website, r.Phone, r.PrimaryType, r.BusinessStatus,
			r.Source, r.Category, r.CategoryMethod, now, now,
		); err != nil {
			return unavailable(err)
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO lead_run_members (run_id, place_id, category)
			VALUES (?, ?, ?)
			ON CONFLICT(run_id, place_id) DO UPDATE SET
				category = excluded.category`,
			run.ID, r.PlaceID, r.Category,
		); err != nil {
			return unavailable(err)
		}
	}

	if err := tx.Commit(); err != nil {
		return unavailable(err)
	}
	return nil
}

// leadWhere builds the shared filter. It is one function so the listing and the
// category counts can never disagree about what a filter means.
func leadWhere(f LeadFilter) (string, []any) {
	var (
		clauses []string
		args    []any
	)
	if f.Category != "" {
		clauses = append(clauses, "l.category = ?")
		args = append(args, f.Category)
	}
	if f.RunID != "" {
		clauses = append(clauses,
			"l.place_id IN (SELECT place_id FROM lead_run_members WHERE run_id = ?)")
		args = append(args, f.RunID)
	}
	if t := strings.TrimSpace(f.Text); t != "" {
		// LIKE rather than FTS: the ledger's searchable text is a name and an
		// address, which is a substring question, and adding a second FTS table
		// for two short columns would cost more than it answers.
		like := "%" + strings.ToLower(t) + "%"
		clauses = append(clauses,
			"(lower(l.name) LIKE ? OR lower(l.address) LIKE ? OR lower(l.phone) LIKE ?)")
		args = append(args, like, like, like)
	}
	if f.WithoutWebsite {
		clauses = append(clauses, "l.website = ''")
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// ListLeads returns the ledger, newest-seen first. Nil-Store tolerant.
func (s *Store) ListLeads(ctx context.Context, f LeadFilter) ([]LeadRow, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if f.Limit <= 0 {
		return nil, nil
	}

	where, args := leadWhere(f)
	args = append(args, f.Limit, max(f.Offset, 0))

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+prefixed(leadColumns, "l")+`
		FROM leads l`+where+`
		ORDER BY l.last_seen_at DESC, l.place_id
		LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []LeadRow
	for rows.Next() {
		r, err := scanLead(rows.Scan)
		if err != nil {
			return nil, unavailable(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// LeadCategoryCounts is the category rail: how many businesses per category and
// how many of them have no website, under whatever filter it is given.
//
// It honours f.Category like every other read here rather than quietly dropping
// it. Whether the rail should show the categories the operator is *not* looking
// at is a decision about the screen, and it is made once, at the handler.
func (s *Store) LeadCategoryCounts(ctx context.Context, f LeadFilter) ([]CategoryCount, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}

	where, args := leadWhere(f)

	rows, err := s.db.QueryContext(ctx, `
		SELECT l.category, COUNT(*), SUM(CASE WHEN l.website = '' THEN 1 ELSE 0 END)
		FROM leads l`+where+`
		GROUP BY l.category
		ORDER BY COUNT(*) DESC, l.category`, args...)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []CategoryCount
	for rows.Next() {
		var c CategoryCount
		if err := rows.Scan(&c.Category, &c.Companies, &c.WithoutWebsite); err != nil {
			return nil, unavailable(err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// ListLeadRuns returns the run history, newest first.
func (s *Store) ListLeadRuns(ctx context.Context, limit int) ([]LeadRun, error) {
	if s == nil || s.db == nil || limit <= 0 {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, region_key, query, region_label, source, company_count,
		       with_gaps, with_emails, ran_at
		FROM lead_runs
		ORDER BY ran_at DESC, id
		LIMIT ?`, limit)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []LeadRun
	for rows.Next() {
		var (
			r                 LeadRun
			gaps, emails, ran int64
		)
		if err := rows.Scan(&r.ID, &r.RegionKey, &r.Query, &r.RegionLabel,
			&r.Source, &r.CompanyCount, &gaps, &emails, &ran); err != nil {
			return nil, unavailable(err)
		}
		r.WithGaps = gaps != 0
		r.WithEmails = emails != 0
		r.RanAt = time.Unix(ran, 0).UTC()
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// OutreachEmailsFor resolves the draft status of many leads at once, so the
// ledger listing can show 'sent' / 'skipped' without one query per row. Place
// ids missing from the result simply have no draft.
func (s *Store) OutreachEmailsFor(ctx context.Context, placeIDs []string, promptVersion string) (map[string]OutreachEmail, error) {
	if s == nil || s.db == nil || promptVersion == "" || len(placeIDs) == 0 {
		return nil, nil
	}

	args := make([]any, 0, len(placeIDs)+1)
	args = append(args, promptVersion)
	holders := make([]string, 0, len(placeIDs))
	for _, id := range placeIDs {
		if id == "" {
			continue
		}
		holders = append(holders, "?")
		args = append(args, id)
	}
	if len(holders) == 0 {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT place_id, email, status, truncated
		FROM outreach_emails
		WHERE prompt_version = ? AND place_id IN (`+strings.Join(holders, ",")+`)`,
		args...)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]OutreachEmail, len(holders))
	for rows.Next() {
		var (
			id        string
			oe        OutreachEmail
			truncated int
		)
		if err := rows.Scan(&id, &oe.Email, &oe.Status, &truncated); err != nil {
			return nil, unavailable(err)
		}
		oe.Truncated = truncated != 0
		out[id] = oe
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

func scanLead(scan func(dest ...any) error) (LeadRow, error) {
	var (
		r                   LeadRow
		firstSeen, lastSeen int64
	)
	err := scan(&r.PlaceID, &r.Name, &r.Address, &r.Latitude, &r.Longitude,
		&r.Rating, &r.ReviewCount, &r.Website, &r.Phone, &r.PrimaryType,
		&r.BusinessStatus, &r.Source, &r.Category, &r.CategoryMethod,
		&firstSeen, &lastSeen)
	if err != nil {
		return LeadRow{}, err
	}
	r.FirstSeenAt = time.Unix(firstSeen, 0).UTC()
	r.LastSeenAt = time.Unix(lastSeen, 0).UTC()
	return r, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
