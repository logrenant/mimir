# AGENTS.md — internal/maps

The Google Places API (New) client. Introduced by `tasks/task-23` for
Track B / M4 as the **primary** source of company data for the Maps lead-gen
pipeline; `internal/mapscrape` (M5) is the fallback for what Places does not
cover, and it fills the same `Company` shape.

## What this package is for

Answering "which companies exist in this region?" deterministically, in one
billed HTTP call per page, with **zero** Claude tokens. Everything a model is
needed for happens later, in `internal/leadgen`.

## Rules for this directory

- **The API key is an argument, never an environment read (SD-1).** `New`
  takes it in `Options`. There is no `os.Getenv` in this package and there must
  not be: the credential is operator-provisioned and injected by the process
  that starts the daemon. `BaseURL` and `HTTPClient` in `Options` are test
  seams — documented as such, never a production call site.

- **The key never reaches a log, an error, or a response.** Transport failures
  are reported without wrapping the underlying error, because Go's HTTP errors
  carry the request URL and a mis-built URL could carry the key. If you add a
  log line here, log the query, never the request.

- **The field mask is the cost lever, not just a schema.** Places bills by
  which fields `X-Goog-FieldMask` names. Adding a field is a billing change:
  say so in the task that adds it. `FieldMask` deliberately requests **no**
  free-text field (no editorial summary, no reviews, no generative content) —
  that is what lets every result skip `internal/refine` and travel to a tool
  response without a size negotiation (SD-2/SD-7). A test asserts this.

- **No store access, no orchestration.** This package makes HTTP calls and
  normalizes their answers. Deciding when a search may come from cache belongs
  to `internal/leadgen`, exactly as `internal/pipeline` — not `internal/crawl` —
  owns the page cache. `Query.Key()` exists so the orchestrator has a total
  cache key; using it is the caller's job.

- **`Query.Key()` must stay total.** Every request field that can change which
  companies come back is hashed into it. Adding a field to `searchTextRequest`
  without adding it to `Key` is a correctness bug: a cached region replays
  under a different question. `TestQueryKeyIsTotal` is the guard — extend it
  with the field.

- **Pagination is bounded by us, not by the server (SD-3).** The loop stops at
  the caller's cap, at the API ceiling, at an absent `nextPageToken`, or at a
  repeated token. A server that keeps issuing tokens must not be able to keep
  us spending. Every request carries the caller's context.

- **`Company` stays flat and closed.** No description, no review text, no
  unbounded string. A free-text field added here needs a clamp at the same
  commit (`internal/extract.ClampText` is the house pattern), not later.

## Testing

The Places API is an external paid service, so it is **mocked** — `httptest`
only, no live network and no real key in any test. Cover, at minimum: the
credential and field-mask headers, field-by-field normalization, pagination
(including de-duplication, the result cap and a repeated token), each HTTP
status → sentinel mapping, the single 5xx retry, and context cancellation
mid-pagination.

Live verification against the real API belongs behind `//go:build integration`
with an operator-provided key, and is not part of `make check` (M7).

## Reviewer focus

Grep for `os.Getenv` and for the key appearing anywhere but the
`X-Goog-Api-Key` header. Check that `FieldMask` still names no free-text field
and that any new field was declared as a cost change. Confirm the paging loop
has no exit that depends solely on the server, and that `Query.Key()` covers
every field `searchTextRequest` sends.
