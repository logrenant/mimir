# AGENTS.md — internal/regionsearch

Which provider answers "which companies are in this region", and in which
order. Introduced by `tasks/task-55`.

## The policy, in one sentence

**The free source is tried first.** `internal/mapscrape` (the local Playwright
sidecar) costs nothing and needs no credential, so it is primary;
`internal/maps` (Google Places) bills per request and answers when the free one
could not.

Two things follow from that, and both are the point:

- **A machine with no Google key has full region search.** It is not a degraded
  mode — it is the same path a machine *with* a key takes first.
- **The key-free path is the tested one.** Before this, "no Places key" meant
  the lead-gen pipeline was never built at all, so the branch nobody exercised
  was the branch every keyless machine would hit.

## Rules for this directory

- **The order lives here and nowhere else.** It used to be written twice — half
  in `internal/leadgen`'s pipeline, half in the registration of `maps_search` —
  and the two disagreed about what "no key" meant. A second place that decides
  which source to ask is the drift this package exists to stop.
- **Absent means a nil interface, never a nil pointer.** A `*maps.Client` that
  is nil inside a non-nil interface looks like a working provider until it is
  called. Callers build each provider into an interface-typed variable and
  assign it only when it exists; `cmd/mimir-daemon` is the worked example.
- **A provider that fails is a note, not an error.** The whole reason for two
  sources is that one being down is survivable. Only "nobody answered" is
  `ErrNoData`, and "nothing is configured" is `ErrNoSource` — a different
  problem with a different fix.
- **A cancelled caller is never a provider failure**, and never advances to the
  next source: the work nobody is waiting for must not be billed.
- **A billed answer says so.** `Search` appends a note when Places answered, and
  `Free()` is what a tool description or a UI badge reads. Telling a caller a
  search is free when it is not is the one lie this package could tell.

## Testing

Pure in-process fakes for both providers — no sidecar, no key, no network. The
order, the fallback, the cancelled context and the two error states are each
asserted directly.

## Reviewer focus

- A caller that reaches past `Router` to a concrete provider, or a second place
  that decides the order.
- `Free()` or a description drifting from what `Search` actually does.
