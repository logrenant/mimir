# AGENTS.md — deploy/playwright-maps

The Places-API fallback's renderer: one Chromium instance behind a small HTTP
server, driven by `internal/mapscrape`. Sibling to `deploy/crawl4ai/` and held
to the same rules.

## Rules for this directory

- **The image tag and the library version are pinned and must stay equal**
  (SD-5). `mcr.microsoft.com/playwright:v1.62.1-noble` +
  `playwright@1.62.1`: the browsers baked into the image are the ones that
  library expects to drive. Bumping either is its own task, and it must also
  update `mapscrape.ImageTag`, which `diagnostics` reports.
- **The URL allowlist is a security boundary, not validation.** This is a
  loopback port with a browser attached; without `isMapsURL` it will fetch
  anything on the host's network, any cloud metadata endpoint, any `file://`
  path, for anyone who can reach the port. Match on the **parsed hostname**.
  Never on a substring — `https://evil.example/?x=www.google.com/maps/`
  contains the string and is not Google.
- **It returns HTML, never parsed companies.** Extraction belongs in
  `internal/mapscrape`, where it is fixture-tested and versioned with its
  consumer. Adding a parser here would create a second definition of what a
  result is, in a language with no tests in this repo.
- **Every request is bounded**: body size, navigation timeout, scroll count,
  result count, response size. A caller must not be able to make this process
  run forever or return unbounded bytes.
- **Fresh context per request, one browser per process.** A shared context would
  carry cookies and consent state between unrelated searches.
- Published on loopback only, and `shm_size` is not decoration — Chromium
  crashes on image-heavy pages with Docker's default 64MB `/dev/shm`.
- **Do not add evasion.** No proxy rotation, no captcha solving, no consent-wall
  bypass, no fingerprint spoofing. A consent or interstitial page is reported as
  a typed failure (409) and handed to the operator; it is not something this
  service tries to defeat.

## Reviewer focus

Is the allowlist still host-based? Are the image and library versions still
equal, and does `mapscrape.ImageTag` still match? Can any request path escape a
bound? Did anything start parsing results in JavaScript?
