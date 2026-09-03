# task-63 — The lead ledger: a lead-gen run is remembered, not just answered

- **Status:** done
- **Owner agent:** daemon
- **Prerequisites:** task-55 (the region-search router), task-59 (categories that exist)
- **Primary paths:** `internal/store/migrations/0016_leads.sql`, `internal/store/leads.go`, `internal/store/leads_test.go`, `internal/leadgen/pipeline.go`, `internal/api/maps.go`, `internal/api/api.go`, `internal/config/config.go`, `cmd/mimir-daemon/main.go`
- **Roadmap bucket:** B.4 extension

## Context

A lead-gen run answers and then forgets. `Pipeline.Run` returns a `Report`, the
handler writes it to the socket, and the only thing that survives is a set of
**caches**: `companies` expires on `LeadgenRegionTTL`, and `GetRegionSearch` is
all-or-nothing — one expired member and the whole region misses. So the operator
cannot ask "which businesses have I found, in which categories" without paying
for the search again, and the desktop screen opens empty every time.

The fix is not to make the cache durable. `internal/store/AGENTS.md` is explicit
that the store is a cache and losing it must cost time, never correctness, and
`GetCompany` deletes expired rows as it reads them. A ledger is the opposite
kind of table: no TTL, one row per business, and a record of which run found it.
So it is a new table beside the cache, not a change to it.

## Scope (do exactly this)

1. `0016_leads.sql` — `leads` (one row per `place_id`, no TTL, `category` and
   `category_method` columns, `first_seen_at` / `last_seen_at`), `lead_runs`
   (one row per pipeline run: region key, query, label, source, counts, flags),
   `lead_run_members` (`run_id`, `place_id`, `category`). Indexes for the two
   reads that are not point lookups: by category, and by place.
2. `internal/store/leads.go` in the shape of `emails.go`: `PutLeadRun`,
   `ListLeads`, `LeadCategoryCounts`, `ListLeadRuns`, `OutreachEmailsFor`.
   `PutLeadRun` is one transaction. The `ON CONFLICT` clause never overwrites a
   populated column with an empty one — the free scrape returns no phone where
   Places did, and a re-run must not erase it.
3. `internal/leadgen`: a `LedgerStore` seam and `Pipeline.UseLedger`, installed
   after construction like `UseContacts`. `Run` records at the end; a lead with
   no `place_id` is skipped, and a failed write is a `Report` note, never an
   error (SD-6).
4. `internal/api`: `GET /maps/leads`, `GET /maps/leads/categories`,
   `GET /maps/leads/runs`, registered only when the dep is non-nil. Page bounds
   are `config` constants (`LeadsPageDefault`, `LeadsPageMax`) — no env knob.
5. `cmd/mimir-daemon/main.go`: `UseLedger(db)` and `api.Deps{Leads: db}`.

## Out of scope (do NOT do here)

- Any change to `companies`, `region_searches`, or their TTLs.
- A write endpoint for leads. The one human decision on a lead is its email
  status, and `POST /maps/emails/status` already owns it.
- Deleting or archiving leads.
- Anything under `desktop/` — that is task-64.

## Interfaces / contracts

```go
type LedgerStore interface {
    PutLeadRun(ctx context.Context, run store.LeadRun, rows []store.LeadRow) error
}
func (p *Pipeline) UseLedger(l LedgerStore)
```

`GET /maps/leads?category=&run_id=&q=&without_website=&limit=&offset=` answers
with the same snake_case company shape `CompanyLead` uses, plus `email_status`
joined from `outreach_emails`.

## Definition of Done

- [x] A run, a daemon restart, and `GET /maps/leads` returns the same businesses
      with their categories.
- [x] The same search run twice adds a `lead_runs` row and no `leads` rows.
- [x] Tests cover: migration idempotency, round-trip, a miss per filter
      component, the empty-does-not-overwrite-populated rule, nil-store tolerance.
- [x] `make check` green
- [x] Status set to `done` with changelog

## Notes for the reviewer (Opus)

SD-1 (the two page bounds are constants), SD-6 (a ledger that cannot be written
degrades the run to today's behaviour, it does not fail it), and the store's own
rule that a cache and a record are different tables.

## Changelog

- `internal/store/migrations/0016_leads.sql` (new) — `leads`, `lead_runs`,
  `lead_run_members`, no TTL, beside the caches rather than inside them.
- `internal/store/leads.go` / `leads_test.go` (new) — `PutLeadRun` (one
  transaction, empty never overwrites populated), `ListLeads`,
  `LeadCategoryCounts`, `ListLeadRuns`, `OutreachEmailsFor`.
- `internal/leadgen/pipeline.go` — `LedgerStore`, `UseLedger`, `record`; a
  failed write is a `Report` note. Tests in `pipeline_test.go`.
- `internal/api/api.go` / `maps.go` — `LeadLedger` dep and
  `GET /maps/leads`, `/maps/leads/categories`, `/maps/leads/runs`. The rail
  drops the selected category at the handler, not in the store: it is a decision
  about the screen. Tests in `maps_test.go`.
- `internal/config/config.go` — `LeadsPageDefault`, `LeadsPageMax`,
  `LeadRunsMax`, validated; no env override.
- `cmd/mimir-daemon/main.go` — `UseLedger(db)` and `Leads: db`, the latter wired
  unconditionally so saved businesses read on a machine with no region source.
- `internal/store/AGENTS.md`, `internal/leadgen/AGENTS.md` — why a record is not
  a cache, and why the ledger is not a stage.

**Decision:** the ledger is a new table rather than a durability change to
`companies`. `internal/store/AGENTS.md` makes losing the database a cost in time,
never correctness, and `GetCompany` deletes expired rows as it reads them —
correct for a cache, and silent data loss for a record.

`make check` green.
