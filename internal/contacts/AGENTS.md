# AGENTS.md — internal/contacts

Fills in how to reach a company: a phone number, an email address, and an
address when the listing had none. Introduced by `tasks/task-57`.

## Why it exists

A scraped Maps result has no phone number and never an email address — the feed
does not carry them (`internal/mapscrape/AGENTS.md`). The company's own website
usually carries both. This opens that site once and reads them off it.

## Rules for this directory

- **Nothing is ever inferred.** A company with no website gets no contact
  details; a page that does not state a phone number leaves that field empty.
  Every row records which tier answered (`Method`), so an empty cell in the
  export reads as "looked, found nothing" rather than "never looked".
- **Patterns first, the model second.** A `tel:`/`mailto:` link or a phone
  number in a footer is not work for a model, and most sites have one. Tier 2
  runs only where tier 1 found nothing — the same discipline
  `internal/leadgen`'s categorizer applies, for the same reason.
- **The model's answer is validated like the pattern tier's.** `normalizePhone`
  and `normalizeEmail` run over what the model returned: a number it
  paraphrased, or an address it assembled from context, does not survive. This
  is the guard that makes a model-read contact worth writing into a file
  somebody will dial.
- **One page per company, never a crawl.** The details are on the front page or
  they are not being fetched. A per-company page budget is what keeps an export
  of sixty companies from becoming sixty crawls of sixty sites.
- **A toolchain's address is not the company's.** `junkEmailRe` drops the ones
  that belong to a platform or an agency credit. A wrong address is worse than
  a missing one, and this is the one place that can tell them apart cheaply.
- **A phone number is nine to fifteen digits.** Shorter is a price, a year or a
  postcode; longer is not a phone number (E.164). The loose text pattern also
  requires a leading `+` or `0` for the same reason.

## Testing

Fakes for both the fetcher and the model — no network, no tokens. The tests
that matter are the ones asserting the model is *not* called when patterns
answered, and that a paraphrased number is dropped.

## Reviewer focus

- Any field filled from another field, or a default that is not empty.
- The model tier being reached on a page the patterns already answered.
- A relaxed phone or email pattern: the failure mode here is a plausible wrong
  number in a file somebody dials.
