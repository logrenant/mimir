package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/catalog"
)

// The catalog studio's persistence (task-85, migration 0025).
//
// Two of the four tables are a ledger and two are a cache, and the split is
// what makes a stopped bulk run resumable: the ledger says which products
// exist and where they stand, the caches say what has already been paid for.
// Every method here is nil-Store tolerant, like the rest of this package — a
// daemon with no database serves the screen and reports empty, rather than
// refusing to start.

// PutCatalogImport writes or replaces one import row.
func (s *Store) PutCatalogImport(ctx context.Context, imp catalog.StoredImport) error {
	if s == nil || s.db == nil || imp.ID == "" {
		return nil
	}
	now := time.Now().Unix()
	created := imp.CreatedAt.Unix()
	if imp.CreatedAt.IsZero() {
		created = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO catalog_imports
			(id, filename, dialect, product_count, file_json, brand_json, site_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			filename      = excluded.filename,
			dialect       = excluded.dialect,
			product_count = excluded.product_count,
			file_json     = excluded.file_json,
			brand_json    = excluded.brand_json,
			site_json     = excluded.site_json,
			updated_at    = excluded.updated_at`,
		imp.ID, imp.Filename, imp.Dialect, imp.ProductCount,
		string(imp.FileJSON), string(imp.BrandJSON), string(imp.SiteJSON), created, now)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

func (s *Store) GetCatalogImport(ctx context.Context, id string) (catalog.StoredImport, bool, error) {
	if s == nil || s.db == nil || id == "" {
		return catalog.StoredImport{}, false, nil
	}
	var (
		imp                   catalog.StoredImport
		fileJSON, brand, site string
		created               int64
	)
	// COALESCE because site_json is nullable: an import nobody has pointed at a
	// shop has no scan, which is a different thing from a shop measured blank.
	err := s.db.QueryRowContext(ctx, `
		SELECT id, filename, dialect, product_count, file_json, brand_json,
		       COALESCE(site_json, ''), created_at
		FROM catalog_imports WHERE id = ?`, id).
		Scan(&imp.ID, &imp.Filename, &imp.Dialect, &imp.ProductCount, &fileJSON, &brand, &site, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.StoredImport{}, false, nil
	}
	if err != nil {
		return catalog.StoredImport{}, false, unavailable(err)
	}
	imp.FileJSON = json.RawMessage(fileJSON)
	imp.BrandJSON = json.RawMessage(brand)
	if site != "" {
		imp.SiteJSON = json.RawMessage(site)
	}
	imp.CreatedAt = time.Unix(created, 0).UTC()
	return imp, true, nil
}

// ListCatalogImports returns imports newest first. The file body is left out:
// a listing is a menu, and every row carrying a whole CSV would make opening
// the screen cost as much as opening every import at once.
func (s *Store) ListCatalogImports(ctx context.Context, limit int) ([]catalog.StoredImport, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, filename, dialect, product_count, brand_json, created_at
		FROM catalog_imports ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []catalog.StoredImport
	for rows.Next() {
		var (
			imp     catalog.StoredImport
			brand   string
			created int64
		)
		if err := rows.Scan(&imp.ID, &imp.Filename, &imp.Dialect, &imp.ProductCount, &brand, &created); err != nil {
			return nil, unavailable(err)
		}
		imp.BrandJSON = json.RawMessage(brand)
		imp.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, imp)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// DeleteCatalogImport removes an import and everything derived from it.
//
// The drafts go too. They are keyed by a product id derived from this import's
// id, so leaving them would leave rows no product will ever address again —
// and a re-upload gets a new import id, so they could not be re-found either.
func (s *Store) DeleteCatalogImport(ctx context.Context, id string) error {
	if s == nil || s.db == nil || id == "" {
		return nil
	}
	// No transaction, for the reason stated at the top of runs.go: this
	// package avoids holding a write lock across statements. A delete that is
	// interrupted leaves derived rows behind, which is untidy; one that
	// deadlocks leaves the daemon unusable.
	for _, q := range []string{
		`DELETE FROM catalog_drafts WHERE product_id IN
			(SELECT id FROM catalog_products WHERE import_id = ?)`,
		`DELETE FROM catalog_research WHERE product_id IN
			(SELECT id FROM catalog_products WHERE import_id = ?)`,
		// Before the products, because it reads them.
		`DELETE FROM catalog_product_langs WHERE product_id IN
			(SELECT id FROM catalog_products WHERE import_id = ?)`,
		`DELETE FROM catalog_products WHERE import_id = ?`,
		`DELETE FROM catalog_imports WHERE id = ?`,
	} {
		if _, err := s.db.ExecContext(ctx, q, id); err != nil {
			return unavailable(err)
		}
	}
	return nil
}

// PutCatalogProducts writes an import's products.
//
// A re-import of the same file addresses the same product ids, so this is an
// upsert that preserves status: the operator's approvals survive a re-upload
// of a corrected export, which is the whole reason the ids are derived rather
// than random.
func (s *Store) PutCatalogProducts(ctx context.Context, importID string, products []catalog.StoredProduct) error {
	if s == nil || s.db == nil || importID == "" {
		return nil
	}
	now := time.Now().Unix()
	for i, p := range products {
		body, err := json.Marshal(p.Product)
		if err != nil {
			return err
		}
		status := p.Status
		if status == "" {
			status = catalog.StatusPending
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO catalog_products
				(id, import_id, row_index, product_key, handle, sku, category,
				 product_json, status, reason, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				row_index    = excluded.row_index,
				product_key  = excluded.product_key,
				handle       = excluded.handle,
				sku          = excluded.sku,
				category     = excluded.category,
				product_json = excluded.product_json,
				updated_at   = excluded.updated_at`,
			p.ID, importID, i, p.Key, p.Handle, p.SKU, p.Category,
			string(body), status, now, now); err != nil {
			return unavailable(err)
		}
	}
	return nil
}

func (s *Store) GetCatalogProduct(ctx context.Context, id string, lang catalog.Lang) (catalog.StoredProduct, bool, error) {
	if s == nil || s.db == nil || id == "" {
		return catalog.StoredProduct{}, false, nil
	}
	var (
		p       catalog.StoredProduct
		body    string
		updated int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT p.product_json, `+langStatusExpr(lang)+`, `+langReasonExpr(lang)+`,
		       `+langUpdatedExpr(lang)+`, p.status
		FROM catalog_products p `+langJoin(lang)+`
		WHERE p.id = ?`, langArgs(lang, id)...).
		Scan(&body, &p.Status, &p.Reason, &updated, &p.SourceStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.StoredProduct{}, false, nil
	}
	if err != nil {
		return catalog.StoredProduct{}, false, unavailable(err)
	}
	if err := json.Unmarshal([]byte(body), &p.Product); err != nil {
		return catalog.StoredProduct{}, false, err
	}
	p.UpdatedAt = time.Unix(updated, 0).UTC()
	markLang(&p, lang)
	return p, true, nil
}

// The language branch, in one place.
//
// catalog.ProductFilter carries a language and catalog_products carries only
// the source language's decision, so every read here is one of two shapes. They
// are written as four small functions rather than as two copies of each query
// because the rule "the source language is the product's own column and a
// target language is a row that may not exist" has to be spelled once: a second
// spelling is how a screen ends up filtering on one table and displaying the
// other.
//
// A target language reads through a LEFT JOIN and COALESCE because ABSENCE IS
// PENDING. A product nobody has judged in Arabic has no row in
// catalog_product_langs, and an INNER JOIN would answer "nothing is waiting"
// for a catalogue nobody has touched.
func langJoin(lang catalog.Lang) string {
	if lang == catalog.LangSource {
		return ""
	}
	return `LEFT JOIN catalog_product_langs l
		ON l.product_id = p.id AND l.lang = ?`
}

func langStatusExpr(lang catalog.Lang) string {
	if lang == catalog.LangSource {
		return "p.status"
	}
	return "COALESCE(l.status, 'pending')"
}

func langReasonExpr(lang catalog.Lang) string {
	if lang == catalog.LangSource {
		return "p.reason"
	}
	return "COALESCE(l.reason, '')"
}

func langUpdatedExpr(lang catalog.Lang) string {
	if lang == catalog.LangSource {
		return "p.updated_at"
	}
	return "COALESCE(l.updated_at, p.updated_at)"
}

// langArgs puts the join's own placeholder in front of the caller's, because
// the LEFT JOIN clause is emitted before the WHERE.
func langArgs(lang catalog.Lang, rest ...any) []any {
	if lang == catalog.LangSource {
		return rest
	}
	return append([]any{string(lang)}, rest...)
}

// markLang fills the two fields that only mean something for a target language.
// On a source-language read both stay empty, which is what keeps that read's
// JSON byte-identical to what every client already parses.
func markLang(p *catalog.StoredProduct, lang catalog.Lang) {
	if lang == catalog.LangSource {
		p.SourceStatus = ""
		return
	}
	p.Lang = lang
}

// CatalogStatusCounts is how many products each import holds in each status.
//
// One GROUP BY for the whole list rather than a read per import: the catalogs
// screen draws "1 taslak · 50 bekliyor" on every row, and asking per row is the
// 1+N fan-out task-80 removed from the board — one screen open would be one
// query per file on every poll.
//
// The outer key is the import id, the middle one the language and the inner one
// a `catalog.Status*` value. An import with no products at all is absent rather
// than present-and-empty: the caller draws a row for every import it already
// has, and a missing entry and an empty map say the same thing to it.
//
// A target language appears only once something has been decided in it, and
// that is deliberate rather than incomplete. The obvious-looking alternative —
// a CROSS JOIN over catalog.Langs() so every language reports its pending count
// — would be wrong here: ListCatalogImports leaves file_json on disk on
// purpose, so the catalogs list does not know which languages a file can carry,
// and synthesising them would draw "Arapça · 40 bekliyor" over a Shopify export
// with no Arabic column. That is the exact claim File.Langs exists to prevent.
// The per-file screen does hold the file and computes its own waiting count
// from Import.ProductCount minus what has been decided.
func (s *Store) CatalogStatusCounts(ctx context.Context) (map[string]map[catalog.Lang]map[string]int, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT import_id, '' AS lang, status, COUNT(*)
		FROM catalog_products GROUP BY import_id, status
		UNION ALL
		SELECT p.import_id, l.lang, l.status, COUNT(*)
		FROM catalog_product_langs l
		JOIN catalog_products p ON p.id = l.product_id
		GROUP BY p.import_id, l.lang, l.status`)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]map[catalog.Lang]map[string]int{}
	for rows.Next() {
		var (
			importID string
			lang     string
			status   string
			n        int
		)
		if err := rows.Scan(&importID, &lang, &status, &n); err != nil {
			return nil, unavailable(err)
		}
		if out[importID] == nil {
			out[importID] = map[catalog.Lang]map[string]int{}
		}
		if out[importID][catalog.Lang(lang)] == nil {
			out[importID][catalog.Lang(lang)] = map[string]int{}
		}
		out[importID][catalog.Lang(lang)][status] = n
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// ListCatalogProducts reads a page in file order, so the table an operator
// scrolls is the file they uploaded rather than an arbitrary permutation.
func (s *Store) ListCatalogProducts(ctx context.Context, f catalog.ProductFilter) ([]catalog.StoredProduct, error) {
	if s == nil || s.db == nil || f.ImportID == "" {
		return nil, nil
	}
	// One query, not two. "Pending in Arabic" is the ABSENCE of a row in
	// catalog_product_langs, and absent rows cannot be selected from the table
	// that does not have them — so the product table drives and the decision
	// table is joined to it. Paging settles it either way: ORDER BY row_index
	// with LIMIT/OFFSET has to be applied to catalog_products, because file
	// order is the one thing this read exists to preserve.
	//
	// p.status rides along on every row. It is the source language's decision,
	// and a screen showing "TR onaylı · AR bekliyor" should not need a second
	// request to say so.
	q := strings.Builder{}
	q.WriteString(`SELECT p.product_json, ` + langStatusExpr(f.Lang) + `, ` +
		langReasonExpr(f.Lang) + `, ` + langUpdatedExpr(f.Lang) + `, p.status
		FROM catalog_products p ` + langJoin(f.Lang) + `
		WHERE p.import_id = ?`)
	args := langArgs(f.Lang, f.ImportID)
	if f.Status != "" {
		q.WriteString(" AND " + langStatusExpr(f.Lang) + " = ?")
		args = append(args, f.Status)
	}
	if f.Category != "" {
		q.WriteString(" AND p.category = ?")
		args = append(args, f.Category)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	q.WriteString(" ORDER BY p.row_index LIMIT ? OFFSET ?")
	args = append(args, limit, max(f.Offset, 0))

	rows, err := s.db.QueryContext(ctx, q.String(), args...)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []catalog.StoredProduct
	for rows.Next() {
		var (
			p       catalog.StoredProduct
			body    string
			updated int64
		)
		if err := rows.Scan(&body, &p.Status, &p.Reason, &updated, &p.SourceStatus); err != nil {
			return nil, unavailable(err)
		}
		if err := json.Unmarshal([]byte(body), &p.Product); err != nil {
			return nil, err
		}
		p.UpdatedAt = time.Unix(updated, 0).UTC()
		markLang(&p, f.Lang)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// SetCatalogProductStatus records one decision, about one product, in one
// language.
//
// The target-language branch inserts through a SELECT over catalog_products
// rather than as a plain upsert. A plain upsert always affects a row, which
// would lose the sql.ErrNoRows the caller turns into ErrUnknownProduct and
// would leave a decision about a product that does not exist — the same
// structural guard PutCatalogDraft's edited_by_operator clause already is.
func (s *Store) SetCatalogProductStatus(
	ctx context.Context, id string, lang catalog.Lang, status, reason string, at time.Time,
) error {
	if s == nil || s.db == nil || id == "" {
		return nil
	}
	if !catalog.ValidStatus(status) {
		return errors.New("store: unknown catalog product status " + status)
	}
	if !catalog.KnownLang(lang) {
		return errors.New("store: unknown catalog language " + string(lang))
	}
	if at.IsZero() {
		at = time.Now()
	}

	var (
		res sql.Result
		err error
	)
	if lang == catalog.LangSource {
		res, err = s.db.ExecContext(ctx, `
			UPDATE catalog_products SET status = ?, reason = ?, updated_at = ? WHERE id = ?`,
			status, reason, at.Unix(), id)
	} else {
		res, err = s.db.ExecContext(ctx, `
			INSERT INTO catalog_product_langs
				(product_id, lang, status, reason, created_at, updated_at)
			SELECT p.id, ?, ?, ?, ?, ? FROM catalog_products p WHERE p.id = ?
			ON CONFLICT(product_id, lang) DO UPDATE SET
				status     = excluded.status,
				reason     = excluded.reason,
				updated_at = excluded.updated_at`,
			string(lang), status, reason, at.Unix(), at.Unix(), id)
	}
	if err != nil {
		return unavailable(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetCatalogResearch returns a product's cached market research.
//
// It is the read that makes a stopped bulk run cheap to resume: a pass that
// stopped at product 300 has already paid for 299 searches, crawls and refines,
// and a resumed pass that re-bought them would cost more than the pass it is
// resuming.
func (s *Store) GetCatalogResearch(ctx context.Context, productID, version string) (catalog.Findings, bool, error) {
	if s == nil || s.db == nil || productID == "" || version == "" {
		return catalog.Findings{}, false, nil
	}
	var findings, sources string
	err := s.db.QueryRowContext(ctx, `
		SELECT findings_json, sources_json
		FROM catalog_research WHERE product_id = ? AND version = ?`,
		productID, version).Scan(&findings, &sources)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.Findings{}, false, nil
	}
	if err != nil {
		return catalog.Findings{}, false, unavailable(err)
	}

	var f catalog.Findings
	if err := json.Unmarshal([]byte(findings), &f); err != nil {
		return catalog.Findings{}, false, err
	}
	// The sources ride their own column so a listing can name what was read
	// without decoding the whole brief.
	_ = json.Unmarshal([]byte(sources), &f.Sources)
	return f, true, nil
}

// PutCatalogResearch stores what one product's research found.
//
// Plain replace, with no guard: unlike a draft there is nothing here a person
// decided, so a newer reading of the market is simply newer. What keeps it from
// being re-bought is the version, not a write guard.
func (s *Store) PutCatalogResearch(ctx context.Context, productID, version string, f catalog.Findings) error {
	if s == nil || s.db == nil || productID == "" || version == "" {
		return nil
	}
	sources := f.Sources
	f.Sources = nil
	findings, err := json.Marshal(f)
	if err != nil {
		return err
	}
	srcJSON, err := json.Marshal(sources)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO catalog_research (product_id, version, findings_json, sources_json, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(product_id, version) DO UPDATE SET
			findings_json = excluded.findings_json,
			sources_json  = excluded.sources_json,
			created_at    = excluded.created_at`,
		productID, version, string(findings), string(srcJSON), time.Now().Unix())
	if err != nil {
		return unavailable(err)
	}
	return nil
}

func (s *Store) GetCatalogDraft(ctx context.Context, productID, version string) (catalog.StoredDraft, bool, error) {
	if s == nil || s.db == nil || productID == "" || version == "" {
		return catalog.StoredDraft{}, false, nil
	}
	d, err := scanDraft(s.db.QueryRowContext(ctx, `
		SELECT product_id, version, content_json, fields_json, notes_json,
		       provider, model, edited_by_operator, created_at
		FROM catalog_drafts WHERE product_id = ? AND version = ?`, productID, version))
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.StoredDraft{}, false, nil
	}
	if err != nil {
		return catalog.StoredDraft{}, false, err
	}
	return d, true, nil
}

// GetCatalogDrafts is the read a bulk run makes before it spends anything: one
// query for the whole selection rather than one per product. A per-product read
// would put a round trip in front of every cache hit and make resuming a
// stopped run cost more than the run it is resuming.
func (s *Store) GetCatalogDrafts(ctx context.Context, productIDs []string, version string) (map[string]catalog.StoredDraft, error) {
	if s == nil || s.db == nil || len(productIDs) == 0 || version == "" {
		return map[string]catalog.StoredDraft{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(productIDs)), ",")
	args := make([]any, 0, len(productIDs)+1)
	for _, id := range productIDs {
		args = append(args, id)
	}
	args = append(args, version)

	rows, err := s.db.QueryContext(ctx, `
		SELECT product_id, version, content_json, fields_json, notes_json,
		       provider, model, edited_by_operator, created_at
		FROM catalog_drafts WHERE product_id IN (`+placeholders+`) AND version = ?`, args...)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]catalog.StoredDraft, len(productIDs))
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		out[d.ProductID] = d
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// CatalogProductLangStatuses is every target-language decision for a page of
// products: one query for the whole page rather than one per product, the same
// argument GetCatalogDrafts makes above.
//
// The source language is absent because it is not in this table — every status
// stored before languages existed is a decision about the file's own language
// and lives on catalog_products. Composing the two belongs to the one caller
// that knows that, not to a second read that would have to join for it.
//
// A product nobody has decided on in any target language is absent rather than
// present-and-empty: absence is pending, and the caller reads it that way.
func (s *Store) CatalogProductLangStatuses(
	ctx context.Context, productIDs []string,
) (map[string]map[catalog.Lang]string, error) {
	if s == nil || s.db == nil || len(productIDs) == 0 {
		return map[string]map[catalog.Lang]string{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(productIDs)), ",")
	args := make([]any, 0, len(productIDs))
	for _, id := range productIDs {
		args = append(args, id)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT product_id, lang, status
		FROM catalog_product_langs WHERE product_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]map[catalog.Lang]string, len(productIDs))
	for rows.Next() {
		var id, lang, status string
		if err := rows.Scan(&id, &lang, &status); err != nil {
			return nil, unavailable(err)
		}
		if out[id] == nil {
			out[id] = map[catalog.Lang]string{}
		}
		out[id][catalog.Lang(lang)] = status
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// PutCatalogDraft stores one rewrite.
//
// On conflict it replaces the row only if the existing one was not edited by
// the operator, **or the incoming write is itself the operator's**. That guard
// is the difference between a re-run that finishes the job and a re-run that
// quietly throws away an afternoon of corrections, and it lives in the SQL so a
// concurrent write cannot slip past it — the same shape as
// PutOutreachMessage's `WHERE status = 'draft'`.
//
// The second half of the condition is what makes the first half a guard rather
// than a trap. Without it a person could not edit their own draft twice: the
// row they had just written locked them out of it, and the screen answered
// their second save with a refusal it could not explain.
//
// A write the guard refused is reported as catalog.ErrDraftLocked rather than
// swallowed: the caller narrates it as a skip, which is what it is.
func (s *Store) PutCatalogDraft(ctx context.Context, d catalog.StoredDraft) error {
	if s == nil || s.db == nil || d.ProductID == "" || d.Version == "" {
		return nil
	}
	content, err := json.Marshal(d.Content)
	if err != nil {
		return err
	}
	fields, err := json.Marshal(d.Fields)
	if err != nil {
		return err
	}
	notes, err := json.Marshal(d.Notes)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	created := d.CreatedAt.Unix()
	if d.CreatedAt.IsZero() {
		created = now
	}
	edited := 0
	if d.EditedByOperator {
		edited = 1
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO catalog_drafts
			(product_id, version, content_json, fields_json, notes_json,
			 provider, model, edited_by_operator, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(product_id, version) DO UPDATE SET
			content_json       = excluded.content_json,
			fields_json        = excluded.fields_json,
			notes_json         = excluded.notes_json,
			provider           = excluded.provider,
			model              = excluded.model,
			edited_by_operator = excluded.edited_by_operator,
			updated_at         = excluded.updated_at
		WHERE catalog_drafts.edited_by_operator = 0 OR excluded.edited_by_operator = 1`,
		d.ProductID, d.Version, string(content), string(fields), string(notes),
		d.Provider, d.Model, edited, created, now)
	if err != nil {
		return unavailable(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return catalog.ErrDraftLocked
	}
	return nil
}

// rowScanner is what *sql.Row and *sql.Rows have in common.
type rowScanner interface{ Scan(dest ...any) error }

// ListCatalogDrafts reads generated content across every import at once.
//
// The caller hands over (import, version, language) triples and this method
// joins against them as an inline table. Three things force that shape:
//
//   - catalog_drafts has no import_id and must not grow one. The row's
//     authority on which import it belongs to is catalog_products, and
//     productID already hashes the import id into the key; a duplicated column
//     is a column that can disagree.
//   - The language cannot be read back out of a version string here. It is a
//     suffix on a cache key and parsing one open in this layer would be a
//     second implementation of a format that has already drifted once. The
//     inline table carries it instead.
//   - The constraint is on the PAIR. One version can be current for import A
//     and superseded for import B — two imports share a brand hash until one
//     of their voices is edited — so `AND p.import_id = k.import_id` is what
//     keeps B's stale draft out. A `version IN (…)` filter is subtly wrong in
//     exactly that case and in no other, which is why it is written down.
//
// The ORDER BY is total. A partial sort key with OFFSET paging repeats rows on
// one page and drops them from the next.
func (s *Store) ListCatalogDrafts(ctx context.Context, f catalog.DraftFilter) ([]catalog.DraftRow, error) {
	if s == nil || s.db == nil || len(f.Keys) == 0 {
		return nil, nil
	}

	// The keys go in as a multi-row VALUES, not as a chain of UNION ALL
	// SELECTs. SQLite caps a compound SELECT at 500 terms, and this list is
	// imports × languages: at the 200-import ceiling that is 600 terms, so the
	// whole screen failed with "too many terms in compound SELECT" — reported
	// to the operator as an unavailable store, on a perfectly healthy database.
	// VALUES has no such limit; the ceiling that remains is the variable one,
	// which is 32766 against the three placeholders per key this uses.
	keys := strings.Builder{}
	args := make([]any, 0, len(f.Keys)*3+3)
	for i, k := range f.Keys {
		if i > 0 {
			keys.WriteString(", ")
		}
		keys.WriteString("(?, ?, ?)")
		args = append(args, k.ImportID, k.Version, string(k.Lang))
	}

	q := strings.Builder{}
	q.WriteString(`
		WITH k(import_id, version, lang) AS (VALUES ` + keys.String() + `)
		SELECT k.lang, i.id, i.filename, i.dialect,
		       p.id, p.handle, p.product_json,
		       CASE WHEN k.lang = '' THEN p.status
		            ELSE COALESCE(pl.status, 'pending') END AS status,
		       d.content_json, d.fields_json,
		       d.provider, d.model, d.edited_by_operator,
		       d.created_at, d.updated_at
		FROM k
		JOIN catalog_drafts   d ON d.version = k.version
		JOIN catalog_products p ON p.id = d.product_id AND p.import_id = k.import_id
		JOIN catalog_imports  i ON i.id = p.import_id
		LEFT JOIN catalog_product_langs pl
		       ON pl.product_id = d.product_id AND pl.lang = k.lang
		WHERE 1 = 1`)
	if f.Status != "" {
		q.WriteString(` AND (CASE WHEN k.lang = '' THEN p.status
			ELSE COALESCE(pl.status, 'pending') END) = ?`)
		args = append(args, f.Status)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	q.WriteString(" ORDER BY d.updated_at DESC, p.import_id, p.id, k.lang LIMIT ? OFFSET ?")
	// One more than asked for, so the caller can say whether there is another
	// page without a second COUNT over the same join.
	args = append(args, limit+1, max(f.Offset, 0))

	rows, err := s.db.QueryContext(ctx, q.String(), args...)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []catalog.DraftRow
	for rows.Next() {
		var (
			r                     catalog.DraftRow
			lang                  string
			body, content, fields string
			edited                int
			created, updated      int64
		)
		if err := rows.Scan(&lang, &r.ImportID, &r.Filename, &r.Dialect,
			&r.ProductID, &r.Handle, &body, &r.Status,
			&content, &fields, &r.Provider, &r.Model, &edited,
			&created, &updated); err != nil {
			return nil, unavailable(err)
		}
		if err := json.Unmarshal([]byte(body), &r.Product); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(content), &r.Content); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(fields), &r.Fields)
		r.Lang = catalog.Lang(lang)
		r.EditedByOperator = edited != 0
		r.CreatedAt = time.Unix(created, 0).UTC()
		r.UpdatedAt = time.Unix(updated, 0).UTC()
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

func scanDraft(r rowScanner) (catalog.StoredDraft, error) {
	var (
		d                      catalog.StoredDraft
		content, fields, notes string
		edited                 int
		created                int64
	)
	if err := r.Scan(&d.ProductID, &d.Version, &content, &fields, &notes,
		&d.Provider, &d.Model, &edited, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return catalog.StoredDraft{}, err
		}
		return catalog.StoredDraft{}, unavailable(err)
	}
	if err := json.Unmarshal([]byte(content), &d.Content); err != nil {
		return catalog.StoredDraft{}, err
	}
	_ = json.Unmarshal([]byte(fields), &d.Fields)
	_ = json.Unmarshal([]byte(notes), &d.Notes)
	d.EditedByOperator = edited != 0
	d.CreatedAt = time.Unix(created, 0).UTC()
	return d, nil
}
