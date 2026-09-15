package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// The storefront scan (task-116).
//
// The brand kit this package derives from the file answers "what markup and
// what voice does this store use". It cannot answer "what does this store look
// like", because a CSV carries class names and never the stylesheet behind
// them — which is why the preview has always rendered on a readable default
// that looks like this app rather than like the merchant's shop.
//
// This reads the shop itself. Two things are worth writing down about how.
//
// **The browser resolves the cascade, not this package.** The probe runs inside
// Crawl4AI's own browser and reports `getComputedStyle` values. Fetching the
// stylesheets and parsing them here would be a second CSS implementation that
// is wrong about specificity, custom properties, media queries and @import on
// the first real theme it meets — and it would need its own outbound fetch per
// stylesheet, which is a second SSRF surface for no gain.
//
// **What is captured is computed values, never markup.** An earlier design took
// the description container's outer HTML as a "mockup" to wrap the body in. It
// would not have worked: those class names are inert without the theme's
// stylesheet, the preview frame cannot load it (the app's CSP allows no
// external stylesheet, and inlining a theme is hundreds of kilobytes of rules
// written for a page this is not), and markup copied off a live page is markup
// this app would then have to sanitise and store. Resolved colour, font stack,
// size, line-height and measure are portable, small, and are what actually
// makes a preview look like the shop.
//
// The scan never touches `Vocabulary`. `Render`'s allow-list stays derived from
// the file's own HTML — that is this package's main guarantee and a storefront
// is not evidence about what the export contains. It is also deliberately
// absent from `BrandKit.hash`: a hash that moved on every rescan would throw
// away every draft in the catalogue for a colour change.

// SiteTheme is the page the storefront paints, as the browser computed it.
type SiteTheme struct {
	Background    string `json:"background,omitempty"`
	Text          string `json:"text,omitempty"`
	Link          string `json:"link,omitempty"`
	Accent        string `json:"accent,omitempty"`
	Border        string `json:"border,omitempty"`
	FontFamily    string `json:"font_family,omitempty"`
	FontSize      string `json:"font_size,omitempty"`
	LineHeight    string `json:"line_height,omitempty"`
	HeadingFamily string `json:"heading_family,omitempty"`
	HeadingWeight string `json:"heading_weight,omitempty"`
	HeadingColor  string `json:"heading_color,omitempty"`
}

// SiteContent is the element the description actually renders in on a product
// page — the one the preview is standing in for.
type SiteContent struct {
	Found      bool   `json:"found"`
	FontFamily string `json:"font_family,omitempty"`
	FontSize   string `json:"font_size,omitempty"`
	LineHeight string `json:"line_height,omitempty"`
	Color      string `json:"color,omitempty"`
	MaxWidth   string `json:"max_width,omitempty"`
	TextAlign  string `json:"text_align,omitempty"`
}

// SiteScan is one reading of the operator's storefront.
type SiteScan struct {
	URL       string      `json:"url"`
	ScannedAt time.Time   `json:"scanned_at,omitzero"`
	Theme     SiteTheme   `json:"theme,omitzero"`
	Content   SiteContent `json:"content,omitzero"`
	// Pages is what was actually read, so an operator can see the scan landed
	// on a product page rather than on a cookie wall.
	Pages []string `json:"pages,omitempty"`
	Note  string   `json:"note,omitempty"`
}

// Usable reports whether the preview can paint from this scan. A scan with no
// ground and no ink is a scan that did not happen, and the preview keeps its
// own readable default rather than rendering a storefront as a blank page.
func (s SiteScan) Usable() bool {
	return s.URL != "" && s.Theme.Background != "" && s.Theme.Text != ""
}

// errNoProbe wraps ErrSiteUnreadable so a page that never ran the probe reaches
// the operator as "the shop could not be read" rather than as an internal
// error — it is the most likely failure of all, and the least mysterious.
var errNoProbe = fmt.Errorf("%w: the page reported no measurement", ErrSiteUnreadable)

// checkSiteURL is the gate on an operator-supplied address.
//
// It is untrusted input that becomes an outbound request, and the request is
// made by a container with a route to the host's network — so refusing only
// loopback would leave the LAN, the host itself through a private address, and
// the cloud metadata endpoint all reachable by typing them into a text box.
// lookupIPs resolves a host name. It is a parameter rather than a call to
// net.DefaultResolver because the guard below is the one piece of this feature
// whose failure is a security failure, and a check that could only be exercised
// against live DNS is a check whose tests would be skipped the first time the
// network was slow.
type lookupIPs func(ctx context.Context, host string) ([]net.IP, error)

func resolveWithSystem(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

func checkSiteURL(ctx context.Context, raw string, lookup lookupIPs) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: the address is empty", ErrSiteRefused)
	}
	// An operator types a shop, not a URL.
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %q could not be parsed", ErrSiteRefused, raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%w: only http and https are fetched, not %q", ErrSiteRefused, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("%w: the address has no host", ErrSiteRefused)
	}

	// A literal address is judged directly; a name is judged by every address
	// it resolves to, because a name that resolves inward is the same request.
	ips := []net.IP{}
	if lit := net.ParseIP(host); lit != nil {
		ips = append(ips, lit)
	} else {
		resolved, err := lookup(ctx, host)
		if err != nil {
			return "", fmt.Errorf("%w: %s does not resolve", ErrSiteRefused, host)
		}
		ips = append(ips, resolved...)
	}
	for _, ip := range ips {
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() ||
			ip.IsLinkLocalUnicast() || isSharedAddressSpace(ip) {
			return "", fmt.Errorf("%w: %s resolves to a local address (%s); only public storefronts are fetched", ErrSiteRefused, host, ip)
		}
	}
	return u.String(), nil
}

// sharedAddressSpace is RFC 6598's 100.64.0.0/10, the carrier-grade NAT range.
//
// It has its own check because Go has no predicate for it and the four it does
// have all wave it through: IsGlobalUnicast reports true and IsPrivate reports
// false. Container networks and ISP deployments really do route internal
// services through this block, so without this the guard has a hole exactly
// where it looks most complete.
var sharedAddressSpace = &net.IPNet{
	IP:   net.IPv4(100, 64, 0, 0),
	Mask: net.CIDRMask(10, 32),
}

func isSharedAddressSpace(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && sharedAddressSpace.Contains(v4)
}

// siteProbe is what the script writes onto the document element. The short JSON
// names are the script's, kept short because the whole object travels back as
// an HTML attribute value.
type siteProbe struct {
	Background    string `json:"bg"`
	Text          string `json:"fg"`
	Link          string `json:"link"`
	Accent        string `json:"accent"`
	Border        string `json:"border"`
	FontFamily    string `json:"font"`
	FontSize      string `json:"size"`
	LineHeight    string `json:"lh"`
	HeadingFamily string `json:"hfont"`
	HeadingWeight string `json:"hweight"`
	HeadingColor  string `json:"hcolor"`
	// Href is where the script actually ran. The crawler follows redirects —
	// verified against the pinned image — so the address the guard approved and
	// the page that was measured are not necessarily the same one.
	Href    string `json:"href"`
	Content struct {
		Found      bool   `json:"found"`
		FontFamily string `json:"font"`
		FontSize   string `json:"size"`
		LineHeight string `json:"lh"`
		Color      string `json:"color"`
		MaxWidth   string `json:"maxw"`
		TextAlign  string `json:"align"`
	} `json:"content"`
}

const scanAttr = "data-mimir-scan"

// landingScript reports only where it ran. The root fetch takes this one: it is
// not measured for style, but its HTML is parsed and mined for links, so where
// it actually landed still has to be known.
func landingScript() string {
	return `document.documentElement.setAttribute("` + scanAttr + `", JSON.stringify({href: location.href}));`
}

// checkLanding judges where a fetch actually ended up, by the same rule as the
// address the operator typed.
//
// The guard runs before a fetch; the crawler follows redirects — verified
// against the pinned image — so a public host that redirects inward would be
// read under a guard that had already said yes. This cannot stop the request
// being made, only the crawler could; what it stops is the answer being parsed,
// stored or shown, and it names the address it refused.
//
// A page that reported no landing is left alone here: parseScanResult is what
// decides whether a missing probe is fatal, and for the root fetch it is not —
// a shop whose home page blocks scripts is still a shop.
func checkLanding(ctx context.Context, rawHTML string, lookup lookupIPs) error {
	probe, err := parseScanResult(rawHTML)
	if err != nil || probe.Href == "" {
		return nil
	}
	if _, err := checkSiteURL(ctx, probe.Href, lookup); err != nil {
		return fmt.Errorf("%w (yönlendirme: %s)", err, probe.Href)
	}
	return nil
}

// scanScript builds the probe.
//
// `sample` is the merchant's own description text from the file, and it is the
// signal that finds the element the description renders in: no class-name
// guesswork survives contact with a second theme, but the text is the same text.
// It is embedded through json.Marshal — it is merchant content, and a quote or
// a closing tag in it must not decide what runs.
func scanScript(sample string) string {
	needle, err := json.Marshal(normalizeSample(sample))
	if err != nil {
		needle = []byte(`""`)
	}
	return `(() => {
  const g = (el, p) => el ? getComputedStyle(el)[p] : "";
  const body = document.body;
  const head = document.querySelector("h1, h2");
  const link = document.querySelector("a[href]");
  const btn = document.querySelector("button, [type=submit], .btn, .button");
  const norm = (s) => (s || "").replace(/\s+/g, " ").trim().toLowerCase();
  const needle = ` + string(needle) + `;

  // The deepest element that still contains the description text: walking down
  // rather than up lands on the paragraph's own container instead of the page.
  let box = null;
  if (needle) {
    const all = document.body ? document.body.querySelectorAll("*") : [];
    for (const el of all) {
      if (norm(el.textContent).indexOf(needle) !== -1) box = el;
    }
  }
  const out = {
    href: location.href,
    bg: g(body, "backgroundColor"), fg: g(body, "color"),
    link: g(link, "color"), accent: g(btn, "backgroundColor"),
    border: g(btn, "borderColor"),
    font: g(body, "fontFamily"), size: g(body, "fontSize"), lh: g(body, "lineHeight"),
    hfont: g(head, "fontFamily"), hweight: g(head, "fontWeight"), hcolor: g(head, "color"),
    content: box ? {
      found: true,
      font: g(box, "fontFamily"), size: g(box, "fontSize"),
      lh: g(box, "lineHeight"), color: g(box, "color"),
      maxw: g(box, "maxWidth"), align: g(box, "textAlign")
    } : { found: false }
  };
  document.documentElement.setAttribute("` + scanAttr + `", JSON.stringify(out));
})();`
}

// normalizeSample trims the merchant's description down to the run of words the
// probe matches on. Long enough to be unique on the page, short enough that a
// rewrite of the tail still matches.
func normalizeSample(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(stripTags(s)), " "))
	const max = 60
	if len(s) > max {
		// Cut on a rune boundary; a half character never matches anything.
		cut := max
		for cut > 0 && !isASCII(s[cut]) {
			cut--
		}
		s = strings.TrimSpace(s[:cut])
	}
	return s
}

func isASCII(b byte) bool { return b < 0x80 }

// stripTags removes markup so the probe searches for what a reader sees. The
// file's description is HTML and the page's textContent is not.
func stripTags(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// parseScanResult reads the probe back off the returned document.
//
// Crawl4AI hands back HTML, not what the script evaluated to, so the answer
// travels as an attribute. Parsed with the HTML parser rather than a regular
// expression because the value is JSON inside an attribute: every quote in it
// is an entity, and unescaping those by hand is the kind of thing that works
// until a storefront has an apostrophe in its font stack.
func parseScanResult(rawHTML string) (siteProbe, error) {
	if strings.TrimSpace(rawHTML) == "" {
		return siteProbe{}, errNoProbe
	}
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return siteProbe{}, fmt.Errorf("%w: the page could not be parsed (%v)", ErrSiteUnreadable, err)
	}
	raw := findScanAttr(doc)
	if raw == "" {
		return siteProbe{}, errNoProbe
	}
	var p siteProbe
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return siteProbe{}, fmt.Errorf("%w: the measurement could not be read (%v)", ErrSiteUnreadable, err)
	}
	return p, nil
}

func findScanAttr(n *html.Node) string {
	if n.Type == html.ElementNode {
		for _, a := range n.Attr {
			if a.Key == scanAttr {
				return a.Val
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if got := findScanAttr(c); got != "" {
			return got
		}
	}
	return ""
}

// themeOf keeps only what the preview can paint with.
//
// A computed value is not automatically a fact about the design: a body whose
// background resolves to `rgba(0, 0, 0, 0)` is saying "whatever is behind me",
// and carrying that through would paint the preview transparent and call it the
// storefront's colour.
func themeOf(p siteProbe) SiteTheme {
	return SiteTheme{
		Background:    paintable(p.Background),
		Text:          paintable(p.Text),
		Link:          paintable(p.Link),
		Accent:        paintable(p.Accent),
		Border:        paintable(p.Border),
		FontFamily:    strings.TrimSpace(p.FontFamily),
		FontSize:      strings.TrimSpace(p.FontSize),
		LineHeight:    strings.TrimSpace(p.LineHeight),
		HeadingFamily: strings.TrimSpace(p.HeadingFamily),
		HeadingWeight: strings.TrimSpace(p.HeadingWeight),
		HeadingColor:  paintable(p.HeadingColor),
	}
}

// paintable drops a colour that says nothing: absent, or fully transparent.
func paintable(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "transparent" {
		return ""
	}
	if strings.HasPrefix(v, "rgba(") && strings.HasSuffix(v, " 0)") {
		return ""
	}
	return v
}

func contentOf(p siteProbe) SiteContent {
	return SiteContent{
		Found:      p.Content.Found,
		FontFamily: strings.TrimSpace(p.Content.FontFamily),
		FontSize:   strings.TrimSpace(p.Content.FontSize),
		LineHeight: strings.TrimSpace(p.Content.LineHeight),
		Color:      paintable(p.Content.Color),
		MaxWidth:   strings.TrimSpace(p.Content.MaxWidth),
		TextAlign:  strings.TrimSpace(p.Content.TextAlign),
	}
}

// productPaths are the segments a storefront puts a product under. Every
// platform this package knows spells it one of these ways, and a path that
// matches none is a page rather than a product.
var productPaths = []string{"/products/", "/product/", "/urun/", "/urunler/", "/p/", "/dp/"}

// pickProductURL chooses which page to measure.
//
// A home page is a hero image and a newsletter box; the description renders on a
// product page, so that is the page whose typography the preview is standing in
// for. The file already says which products exist, and a link carrying one of
// its own handles is the strongest possible evidence that the page is a product
// of this catalogue rather than a product of the theme's demo content.
//
// It never leaves the storefront: following a link to another host would
// measure somebody else's design and store it as this brand's.
func pickProductURL(rootHTML, base string, handles []string) (string, bool) {
	root, err := url.Parse(base)
	if err != nil {
		return "", false
	}
	doc, err := html.Parse(strings.NewReader(rootHTML))
	if err != nil {
		return "", false
	}
	links := collectLinks(doc, root)

	for _, h := range handles {
		h = strings.TrimSpace(strings.ToLower(h))
		if h == "" {
			continue
		}
		for _, l := range links {
			if strings.Contains(strings.ToLower(l), h) {
				return l, true
			}
		}
	}
	for _, l := range links {
		low := strings.ToLower(l)
		for _, seg := range productPaths {
			if strings.Contains(low, seg) {
				return l, true
			}
		}
	}
	return "", false
}

// collectLinks resolves every href against the page and keeps the ones that
// stayed on it.
func collectLinks(n *html.Node, root *url.URL) []string {
	var out []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, a := range n.Attr {
				if a.Key != "href" {
					continue
				}
				ref, err := url.Parse(strings.TrimSpace(a.Val))
				if err != nil {
					continue
				}
				abs := root.ResolveReference(ref)
				if abs.Host == root.Host && (abs.Scheme == "http" || abs.Scheme == "https") {
					abs.Fragment = ""
					out = append(out, abs.String())
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

// ScanSite reads the operator's storefront and stores what the preview can
// paint from.
//
// It writes `Import.Site` and nothing else. In particular it does not touch
// `Brand`: the vocabulary is the file's own markup and the voice is the
// operator's own writing, and a storefront is evidence about neither. Keeping
// them apart is also what keeps `BrandKit.hash` still, so a rescan costs a
// colour and not every approved draft in the catalogue.
func (s *Studio) ScanSite(ctx context.Context, importID, rawURL string) (SiteScan, error) {
	if s.site == nil {
		return SiteScan{}, fmt.Errorf("%w: no crawler is wired", ErrSiteUnreadable)
	}
	imp, err := s.Get(ctx, importID)
	if err != nil {
		return SiteScan{}, err
	}
	lookup := s.lookup
	if lookup == nil {
		lookup = resolveWithSystem
	}
	target, err := checkSiteURL(ctx, rawURL, lookup)
	if err != nil {
		return SiteScan{}, err
	}

	scan := SiteScan{URL: target, ScannedAt: time.Now().UTC()}

	// The root first, only to find a product page on it. Its own styles are not
	// measured — a home page's body is not the page a description renders in —
	// but it still carries the landing probe, because the crawler follows
	// redirects and this page's HTML is about to be parsed and mined for links.
	// Checking only the second fetch left the first one reading whatever it was
	// redirected to.
	rootHTML, err := s.site.Fetch(ctx, target, landingScript())
	if err != nil {
		return SiteScan{}, fmt.Errorf("%w: the shop did not open (%v)", ErrSiteUnreadable, err)
	}
	if err := checkLanding(ctx, rootHTML, lookup); err != nil {
		return SiteScan{}, err
	}
	scan.Pages = append(scan.Pages, target)

	handles, sample := s.siteHints(ctx, importID)

	page := target
	if pdp, ok := pickProductURL(rootHTML, target, handles); ok {
		page = pdp
	} else {
		scan.Note = "Mağaza sayfasında ürün bağlantısı bulunamadı; ölçüm ana sayfadan alındı."
	}

	measured, err := s.site.Fetch(ctx, page, scanScript(sample))
	if err != nil {
		return SiteScan{}, fmt.Errorf("%w: %s could not be read (%v)", ErrSiteUnreadable, page, err)
	}
	if page != target {
		scan.Pages = append(scan.Pages, page)
	}

	probe, err := parseScanResult(measured)
	if err != nil {
		return SiteScan{}, err
	}
	if err := checkLanding(ctx, measured, lookup); err != nil {
		return SiteScan{}, err
	}
	scan.Theme = themeOf(probe)
	scan.Content = contentOf(probe)
	if !scan.Content.Found && scan.Note == "" {
		scan.Note = "Ürün açıklaması sayfada bulunamadı; ölçüm sayfanın gövdesinden alındı."
	}
	if !scan.Usable() {
		return SiteScan{}, fmt.Errorf("%w: %s reported no colour or type", ErrSiteUnreadable, page)
	}

	// PutCatalogImport, not save(): save() also rewrites every product row and
	// resets each one to pending, which would make measuring a colour throw
	// away the catalogue's decisions. SetBrand writes the import alone for the
	// same reason.
	imp.Site = scan
	if s.store != nil {
		if err := s.store.PutCatalogImport(ctx, imp.stored()); err != nil {
			return SiteScan{}, err
		}
	}
	return scan, nil
}

// SiteReader fetches one page and returns its HTML, optionally after running a
// script in it.
//
// Stated here rather than taken as *crawl.Client for the reason `Researcher`
// is: this package is imported by `internal/store`, and reaching outward for a
// concrete crawler would drag the pipeline's dependency graph in behind it. One
// method, because one is all the scan needs — and a test needs a fake today,
// which is the other half of the rule that allows an interface at all.
type SiteReader interface {
	Fetch(ctx context.Context, url, js string) (string, error)
}

// UseSiteReader wires the crawler. A studio without one refuses to scan rather
// than returning an empty reading.
func (s *Studio) UseSiteReader(r SiteReader) { s.site = r }

// siteSampleProducts is how many rows the scan reads to learn the file's own
// handles and one description. Enough for a link to match; far short of a page.
const siteSampleProducts = 25

// siteHints reads the file's own products for the two things the scan needs
// from them: the slugs that identify a link to this catalogue's own product,
// and one real description to find on the page.
func (s *Studio) siteHints(ctx context.Context, importID string) (handles []string, sample string) {
	if s.store == nil {
		return nil, ""
	}
	rows, err := s.store.ListCatalogProducts(ctx, ProductFilter{
		ImportID: importID, Limit: siteSampleProducts,
	})
	if err != nil {
		// The scan is worth running without hints: it falls back to the shape
		// of the path and to the page body. Failing here would make a storefront
		// unreadable because a local read hiccuped.
		return nil, ""
	}
	for _, r := range rows {
		if h := strings.TrimSpace(r.Handle); h != "" {
			handles = append(handles, h)
		}
		if sample == "" {
			if d := strings.TrimSpace(r.Original.DescriptionHTML); len(stripTags(d)) > 40 {
				sample = d
			}
		}
	}
	return handles, sample
}
