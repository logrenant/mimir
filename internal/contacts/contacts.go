// Package contacts fills in how to reach a company.
//
// A scraped Maps result carries a name, a rating and — if the card showed one —
// a website. It does not carry a phone number, and it never carries an email
// address. The company's own site usually carries both, so this opens that
// site once and reads them off it.
//
// Two tiers, cheapest first, the same shape internal/leadgen's categorizer
// uses:
//
//  1. Patterns. A `mailto:`/`tel:` link or a phone number in a footer is not
//     work for a model, and most sites have one.
//  2. The model, on the pages where tier 1 found nothing. It reads the page
//     text through internal/refine's contact profile — the context-isolation
//     firewall — and is told, in the prompt and again by this package, never to
//     complete a partial number.
//
// **Nothing here is ever inferred.** A company with no website gets no contact
// details, a page that does not state a phone number leaves that field empty,
// and an empty field in the export means "not found", never "not looked for" —
// which is why Enriched records which tier answered.
package contacts

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/crawl"
	"github.com/logrenant/mimir/internal/extract"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/refine"
	"github.com/logrenant/mimir/internal/search"
	"golang.org/x/sync/errgroup"
)

// Method says which tier answered, and is carried into the export so an empty
// column can be read honestly.
const (
	MethodNone    = ""
	MethodPattern = "pattern"
	MethodModel   = "model"
	MethodNoSite  = "no_website"
	MethodFailed  = "fetch_failed"
)

// Enriched is what could be found for one company.
type Enriched struct {
	PlaceID string
	Phone   string
	Email   string
	// Website is only filled when the company arrived without one and a search
	// found its site. Recorded so the caller can persist it: a site found once
	// should not have to be found again on the next run.
	Website string
	// Address is only filled when the company had none already: a Maps address
	// is better evidence about where a business is than its own footer.
	Address string
	Method  string
	// Note carries why a lookup produced nothing, for the export's own column.
	Note string
}

// Extractor is internal/refine's contact profile.
type Extractor interface {
	ExtractContacts(ctx context.Context, in refine.ContactInput) (refine.ContactOutput, error)
}

// Fetcher is internal/crawl's client.
// Searcher finds a company's site when its listing did not carry one.
// *search.Client satisfies it. Nil disables the lookup, which is the honest
// state for a binary with no search configured — the company is then reported
// as MethodNoSite exactly as before.
type Searcher interface {
	Search(ctx context.Context, query string, count int) ([]search.Result, error)
}

type Fetcher interface {
	Markdown(ctx context.Context, targetURL string) (crawl.Page, error)
}

// Enricher opens company websites and reads contact details off them.
type Enricher struct {
	searcher  Searcher
	cfg       config.Config
	fetcher   Fetcher
	extractor Extractor
}

func New(cfg config.Config, fetcher Fetcher, extractor Extractor) *Enricher {
	return &Enricher{cfg: cfg, fetcher: fetcher, extractor: extractor}
}

// UseSearcher installs the search client used to find a missing website.
//
// Set after construction rather than passed to New because it is optional: the
// enricher is complete without it, and a caller that has no search client
// still gets tiers 1 and 2 on the companies that did list a site.
func (e *Enricher) UseSearcher(s Searcher) { e.searcher = s }

// findSite looks for a company's own website.
//
// Only reached when the listing carried none. The query is the name plus the
// town because a company name alone is rarely unique, and the first result that
// is not a directory is taken: aggregators outrank small businesses for their
// own names, so accepting result one unfiltered would file yelp.com as half the
// region's website. Nothing here is inferred — an unusable result set leaves
// the field empty, which is what MethodNoSite already means.
func (e *Enricher) findSite(ctx context.Context, c maps.Company) string {
	if e.searcher == nil || strings.TrimSpace(c.Name) == "" {
		return ""
	}
	query := c.Name
	if town := townOf(c.FormattedAddress); town != "" {
		query += " " + town
	}

	results, err := e.searcher.Search(ctx, query, 5)
	if err != nil {
		slog.Debug("contacts: site lookup failed", "company", c.Name, "error", err)
		return ""
	}
	for _, r := range results {
		if isDirectory(r.URL) {
			continue
		}
		if u, err := url.Parse(r.URL); err == nil && u.Host != "" {
			return u.Scheme + "://" + u.Host
		}
	}
	return ""
}

// directoryHosts are the aggregators that outrank a small business for its own
// name. Filing one of these as a company's website would be worse than finding
// nothing: an operator would ring the directory instead of the company.
var directoryHosts = []string{
	"google.", "facebook.", "instagram.", "linkedin.", "twitter.", "x.com",
	"youtube.", "yelp.", "tripadvisor.", "foursquare.", "yellowpages.",
	"sahibinden.", "hepsiburada.", "n11.", "trendyol.", "gittigidiyor.",
	"wikipedia.", "maps.apple.", "bing.", "pinterest.", "tiktok.",
	"firmarehberi", "rehberim", "bulurum", "sayfa", "kolayadres",
}

func isDirectory(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return true
	}
	host := strings.ToLower(u.Host)
	for _, d := range directoryHosts {
		if strings.Contains(host, d) {
			return true
		}
	}
	return false
}

// townOf pulls the town out of a formatted address, to disambiguate a name.
// Turkish Maps addresses end "... , 20100 Merkezefendi/Denizli"; the segment
// after the last slash is the province, which is the useful half.
func townOf(address string) string {
	if i := strings.LastIndex(address, "/"); i >= 0 && i+1 < len(address) {
		return strings.TrimSpace(address[i+1:])
	}
	return ""
}

var (
	// emailRe is deliberately conservative: a local part without spaces, a
	// dotted domain, and a TLD of letters. It will miss an obfuscated address,
	// which is the right way to be wrong here — a wrong address is worse than
	// a missing one.
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

	// telHrefRe reads a tel: link, which is a company stating its own number in
	// machine-readable form — the best evidence a page can offer.
	telHrefRe = regexp.MustCompile(`(?i)href=["']tel:([^"']+)["']`)

	// mailtoRe, likewise, for an address a page put in a link.
	mailtoRe = regexp.MustCompile(`(?i)href=["']mailto:([^"'?]+)`)

	// phoneRe is the loose text pattern, used only when no tel: link exists.
	// It requires either a leading + or a leading 0 so that a price, a date or
	// a postcode cannot become a phone number.
	phoneRe = regexp.MustCompile(`(?:\+|00)[0-9][0-9()\s.\-]{7,17}[0-9]|0[0-9]{3}[\s.\-]?[0-9]{3}[\s.\-]?[0-9]{2}[\s.\-]?[0-9]{2}`)

	// junkEmailRe drops the addresses that belong to the page's toolchain
	// rather than to the business. An agency's address in a footer credit is
	// the classic wrong answer here.
	junkEmailRe = regexp.MustCompile(`(?i)@(example|sentry|wixpress|godaddy|w3\.org|schema\.org|googlemail)\.`)
)

// Enrich looks up every company that has a website, in bounded parallel.
//
// One page per company, never a crawl of the site: the contact details are on
// the front page or they are on a "contact" page this does not go looking for,
// and a per-company page budget is what keeps an export of sixty companies from
// becoming sixty crawls of sixty sites.
func (e *Enricher) Enrich(ctx context.Context, companies []maps.Company) map[string]Enriched {
	out := make(map[string]Enriched, len(companies))
	if len(companies) == 0 {
		return out
	}

	var mu sync.Mutex
	record := func(en Enriched) {
		mu.Lock()
		defer mu.Unlock()
		out[en.PlaceID] = en
	}

	limit := e.cfg.MaxConcurrentRefines
	if limit <= 0 {
		limit = 1
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(limit)

	for _, c := range companies {
		company := c
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			record(e.enrichOne(gctx, company))
			return nil
		})
	}
	// A cancelled context is the only error these goroutines return, and the
	// partial map is still the honest answer for what was looked up.
	_ = g.Wait()

	return out
}

func (e *Enricher) enrichOne(ctx context.Context, c maps.Company) Enriched {
	en := Enriched{PlaceID: c.PlaceID}

	site := strings.TrimSpace(c.Website)
	if site == "" {
		// The listing had none, so go and look for one before giving up.
		if found := e.findSite(ctx, c); found != "" {
			site = found
			en.Website = found
		} else {
			en.Method = MethodNoSite
			en.Note = "no website on the listing, and none found by search"
			return en
		}
	}
	if !strings.HasPrefix(site, "http://") && !strings.HasPrefix(site, "https://") {
		site = "https://" + site
	}
	if _, err := url.ParseRequestURI(site); err != nil {
		en.Method = MethodFailed
		en.Note = "unusable website URL"
		return en
	}

	page, err := e.fetcher.Markdown(ctx, site)
	if err != nil {
		en.Method = MethodFailed
		en.Note = trimNote(err.Error())
		return en
	}

	// Tier 1 — patterns over the raw HTML, because tel:/mailto: live in
	// attributes that Markdown conversion drops.
	phone, email := fromPatterns(page.RawHTML, page.Markdown)
	if phone != "" || email != "" {
		en.Phone, en.Email, en.Method = phone, email, MethodPattern
		return en
	}

	// Tier 2 — the model, on what is left.
	if e.extractor == nil {
		en.Method = MethodNone
		en.Note = "nothing matched and no model is configured"
		return en
	}
	text := extract.ClampText(page.Markdown, e.cfg.ContactModelMaxChars)
	if strings.TrimSpace(text) == "" {
		en.Method = MethodNone
		en.Note = "the page had no readable text"
		return en
	}

	res, err := e.extractor.ExtractContacts(ctx, refine.ContactInput{
		Company:   c.Name,
		Text:      text,
		MaxTokens: e.cfg.ContactModelMaxTokens,
	})
	if err != nil {
		slog.Debug("contact extraction failed", "company", c.Name, "error", err)
		en.Method = MethodFailed
		en.Note = trimNote(err.Error())
		return en
	}

	// The model's answer is held to the same patterns tier 1 uses. A number it
	// paraphrased, or an address it assembled, does not survive this.
	en.Phone = normalizePhone(res.Phone)
	en.Email = normalizeEmail(res.Email)
	if c.FormattedAddress == "" {
		en.Address = strings.TrimSpace(res.Address)
	}
	if en.Phone == "" && en.Email == "" && en.Address == "" {
		en.Method = MethodNone
		en.Note = "the page did not state contact details"
		return en
	}
	en.Method = MethodModel
	return en
}

// fromPatterns reads a phone and an email out of a page without a model.
func fromPatterns(rawHTML, markdown string) (phone, email string) {
	if m := telHrefRe.FindStringSubmatch(rawHTML); len(m) > 1 {
		phone = normalizePhone(m[1])
	}
	if m := mailtoRe.FindStringSubmatch(rawHTML); len(m) > 1 {
		email = normalizeEmail(m[1])
	}

	text := markdown
	if text == "" {
		text = rawHTML
	}
	if phone == "" {
		phone = normalizePhone(phoneRe.FindString(text))
	}
	if email == "" {
		for _, candidate := range emailRe.FindAllString(text, 8) {
			if e := normalizeEmail(candidate); e != "" {
				email = e
				break
			}
		}
	}
	return phone, email
}

// normalizePhone keeps a number only if it still looks like one after the
// punctuation is stripped. Nine digits is the shortest national number worth
// treating as real; more than fifteen is not a phone number at all (E.164).
func normalizePhone(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var digits strings.Builder
	plus := strings.HasPrefix(s, "+")
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	d := digits.String()
	if len(d) < 9 || len(d) > 15 {
		return ""
	}
	if plus {
		return "+" + d
	}
	return d
}

func normalizeEmail(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || !emailRe.MatchString(s) {
		return ""
	}
	if junkEmailRe.MatchString(s) {
		return ""
	}
	return emailRe.FindString(s)
}

func trimNote(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 160
	if len(s) > max {
		return s[:max]
	}
	return fmt.Sprint(s)
}
