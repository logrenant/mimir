# AGENTS.md — internal/mapscrape

The Places-API **fallback** for Maps region search. Same question as
`internal/maps`, same `maps.Query` in and `[]maps.Company` out, different
provider: the public results feed, rendered by `deploy/playwright-maps`.

## Rules for this directory

- **Same types as the primary, or this is not a fallback.** `Search` takes
  `maps.Query` and returns `[]maps.Company` so a caller can swap providers
  without reshaping anything, and so both share `Query.Key()` as a cache key. A
  bespoke result type here would fork the pipeline.
- **Namespaced ids, always.** `store.companies` is keyed by `place_id`; a
  scraped row written under a bare Places-looking id would overwrite a billed
  row and take its provenance with it. Every id this package emits starts with
  `PlaceIDPrefix`, and a test asserts it.
- **Never fabricate a field.** The feed gives name, coordinates, rating, review
  count and sometimes a website. No `types[]`, no phone, address only when the
  card shows one. Anything not found is the zero value — never inferred from
  another field, never a plausible default. `internal/leadgen` handles a
  typeless company correctly; it cannot handle an invented type.
- **Extraction is anchored on accessibility markup and URL grammar**, in that
  order: `a[href*="/maps/place/"]` + `aria-label` for the name, `!1s`/`!3d`/
  `!4d` for id and coordinates, and a number-shaped read of the localized rating
  label. **Never use class names** (`hfpxzc`, `MW4etd`, …) — they are minified
  build output and change without notice.
- **The review count is optional, and only the rating label may supply it.**
  Google serves two variants of that label — a rich "4,9 yıldızlı 506 Yorum" and
  a bare "4,9 yıldızlı" — apparently A/B rather than by locale: the same query
  at the same minute produced both during live verification, and in the bare
  variant the count is nowhere in the feed. So a zero count means "not shown",
  and that is the honest answer. The first version had a fallback that searched
  the card for a parenthesised number and read Istanbul phone numbers,
  "(0216) 330 09 99", as 216 reviews. Do not reintroduce it, in any shape.
- **Numbers are localized; parse them as such.** "4,5" is a score of four and a
  half, "1.024" is a thousand and twenty-four. `extract.ParseFloatLoose` treats
  a comma as a thousands separator and drops a leading minus, so it is wrong for
  both the rating and the coordinates here — hence the local `parseRating`,
  `parseCount`, and `strconv` on the coordinate groups.
- **One card's failure costs one card.** A result with no name or no feature id
  is skipped and counted in the `skipped` return; it never fails the search. The
  log line carrying `parsed`/`skipped`/`rendered` is how a silent redesign
  announces itself — keep it.
- **Three different failures, three different errors** (SD-6):
  `ErrSidecarUnavailable` (container down — names `make maps-up`), `ErrBlocked`
  (Google served consent/captcha), `ErrNoResults` (feed rendered, nothing
  usable). Reporting a consent wall as a dead container sends an operator to
  restart Docker for a problem Docker does not have.
- **One attempt per search, and the politeness wait is not optional.** This is
  someone else's site, and the sidecar is a browser: one search here is a page
  plus its images, fonts and tiles. A retry loop against Google is not
  resilience, it is load.
- **No evasion, ever.** No proxies, no IP rotation, no captcha solving, no
  consent-wall bypass, no fingerprint spoofing. A block is reported, not
  defeated.
- **No store, no credential, no orchestration.** Which provider to try first,
  and when to fall back, belongs to the caller — the same boundary `internal/maps`
  holds.
- **Phone and address are visible in the feed and deliberately not taken.** They
  are only reachable through minified class names (`UsdlK`, `W4Efsd`), which the
  rule above rules out. A caller that needs them should use the Places API,
  which returns them as data.
- **The unit tests prove the parser, not the selectors.** `testdata/feed.html`
  is synthetic. Only `-tags integration` against the live site can tell you the
  markup still looks like that, and it is expected to fail one day. When it
  does, that is the system working.

## Reviewer focus

Can a scraped id reach `store.companies` without its prefix? Does any field get
guessed when it is not found? Is a class name being matched anywhere? Are the
three error kinds still distinguishable by `errors.Is`? Did a retry loop appear?
