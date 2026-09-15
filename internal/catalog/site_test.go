package catalog

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

// testLookup is the DNS the guard is tested against: fixed answers, so a
// refusal is about the address and never about the network.
func testLookup(_ context.Context, host string) ([]net.IP, error) {
	switch host {
	case "localhost":
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	case "internal.corp":
		return []net.IP{net.ParseIP("10.1.2.3")}, nil
	case "www.example.com", "example.com", "shop.test":
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}
	return nil, errors.New("no such host")
}

// The operator types this URL, so it is untrusted input that becomes an
// outbound request from the daemon. The crawler runs in a container with a
// route to the host's network, so "it is only localhost" is not a defence.
func TestCheckSiteURL_RefusesWhatMustNotBeFetched(t *testing.T) {
	ctx := context.Background()
	for _, raw := range []string{
		"",
		"not a url",
		"ftp://example.com",
		"file:///etc/passwd",
		"http://127.0.0.1:8080/admin",
		"http://[::1]/",
		"http://10.0.0.5/",
		"http://192.168.1.1/",
		"http://172.16.0.9/",
		"http://169.254.169.254/latest/meta-data/",
		"http://0.0.0.0/",
		"http://localhost:41777/catalog/imports",
		"https://internal.corp/",
	} {
		if _, err := checkSiteURL(ctx, raw, testLookup); err == nil {
			t.Errorf("kabul edilmemeliydi: %q", raw)
		}
	}
}

func TestCheckSiteURL_AcceptsAStorefront(t *testing.T) {
	ctx := context.Background()
	got, err := checkSiteURL(ctx, "https://www.example.com/collections/all", testLookup)
	if err != nil {
		t.Fatalf("checkSiteURL: %v", err)
	}
	if got != "https://www.example.com/collections/all" {
		t.Errorf("URL değişti: %q", got)
	}
	// A bare host is completed rather than refused: an operator types the shop,
	// not a URL.
	if got, err := checkSiteURL(ctx, "example.com", testLookup); err != nil || got != "https://example.com" {
		t.Errorf("çıplak alan adı tamamlanmadı: %q %v", got, err)
	}
}

// The probe writes its answer onto the document element because Crawl4AI does
// not return what the script evaluates to. Reading it back is where a wrong
// assumption would be silent, so it is pinned against the real shape — HTML
// entities and all.
func TestParseScanResult_ReadsTheProbeOffTheDocumentElement(t *testing.T) {
	raw := `<!DOCTYPE html><html lang="en" ` +
		`data-mimir-scan="{&quot;bg&quot;:&quot;rgb(236, 233, 226)&quot;,` +
		`&quot;fg&quot;:&quot;rgb(0, 0, 0)&quot;,&quot;font&quot;:&quot;Geograph, sans-serif&quot;,` +
		`&quot;size&quot;:&quot;16px&quot;,&quot;lh&quot;:&quot;24px&quot;,` +
		`&quot;content&quot;:{&quot;found&quot;:true,&quot;size&quot;:&quot;15px&quot;}}"` +
		`><body><p>x</p></body></html>`
	got, err := parseScanResult(raw)
	if err != nil {
		t.Fatalf("parseScanResult: %v", err)
	}
	if got.Background != "rgb(236, 233, 226)" {
		t.Errorf("arka plan %q", got.Background)
	}
	if got.FontFamily != "Geograph, sans-serif" || got.FontSize != "16px" || got.LineHeight != "24px" {
		t.Errorf("tipografi okunmadı: %+v", got)
	}
	if !got.Content.Found || got.Content.FontSize != "15px" {
		t.Errorf("açıklama kabı okunmadı: %+v", got.Content)
	}
}

// A page that did not run the probe is not a page with an empty theme: it is a
// scan that failed, and saying so is the difference between "this storefront is
// white" and "we never read it".
func TestParseScanResult_APageWithoutTheProbeIsAnError(t *testing.T) {
	if _, err := parseScanResult(`<html><body>hi</body></html>`); err == nil {
		t.Error("prob bulunmadan başarı döndü")
	}
	if _, err := parseScanResult(``); err == nil {
		t.Error("boş HTML başarı döndü")
	}
}

// The merchant's own description text is embedded in the script so the probe
// can find the element that actually holds it. A quote in that text must not
// end the string and change what runs.
func TestScanScript_EscapesTheSampleItSearchesFor(t *testing.T) {
	js := scanScript(`</script><img src=x onerror="alert('x')">"'`)
	if strings.Contains(js, "</script>") {
		t.Error("script kapanış etiketi kaçırılmadı")
	}
	if strings.Contains(js, `onerror="alert`) {
		t.Error("ham nitelik script'e sızdı")
	}
	if !strings.Contains(js, "data-mimir-scan") {
		t.Error("prob niteliği yazılmıyor")
	}
}

// Only values the preview can actually paint survive. A storefront that reports
// `rgba(0, 0, 0, 0)` for its body background is reporting "whatever is behind
// me", and painting the preview transparent would be this app inventing a look
// the storefront does not have.
func TestThemeOf_DropsValuesThatSayNothing(t *testing.T) {
	var p siteProbe
	p.Background = "rgba(0, 0, 0, 0)"
	p.Text = "rgb(17, 17, 17)"
	p.FontFamily = ""
	got := themeOf(p)
	if got.Background != "" {
		t.Errorf("şeffaf arka plan tema olarak alındı: %q", got.Background)
	}
	if got.Text != "rgb(17, 17, 17)" {
		t.Errorf("metin rengi düştü: %q", got.Text)
	}
}

func TestSiteScan_UsableSaysWhetherThePreviewCanPaintIt(t *testing.T) {
	var empty SiteScan
	if empty.Usable() {
		t.Error("taranmamış kayıt kullanılabilir göründü")
	}
	scanned := SiteScan{URL: "https://x.test", Theme: SiteTheme{Background: "rgb(255,255,255)", Text: "rgb(0,0,0)"}}
	if !scanned.Usable() {
		t.Error("zemin ve metin rengi olan tarama kullanılamaz göründü")
	}
}

// Which page to measure. A storefront's home page is a hero image and a
// newsletter box; the description renders on a product page, so that is what
// has to be read — and the file already says which products exist.
func TestPickProductURL_PrefersALinkCarryingOneOfTheFilesOwnHandles(t *testing.T) {
	root := `<html><body>
	  <a href="/pages/about">Hakkımızda</a>
	  <a href="/collections/all">Tümü</a>
	  <a href="/products/cerave-nemlendirici">Cerave</a>
	</body></html>`
	got, ok := pickProductURL(root, "https://shop.test", []string{"cerave-nemlendirici", "baska"})
	if !ok || got != "https://shop.test/products/cerave-nemlendirici" {
		t.Fatalf("kendi ürününün bağlantısı seçilmedi: %q %v", got, ok)
	}
}

// A file whose handles do not appear — an IKAS custom-fields export has no
// handle column at all — still gets a product page, by the shape of the path.
func TestPickProductURL_FallsBackToAProductShapedPath(t *testing.T) {
	root := `<html><body>
	  <a href="/sepet">Sepet</a>
	  <a href="https://shop.test/urun/gunes-kremi-50ml">Güneş kremi</a>
	</body></html>`
	got, ok := pickProductURL(root, "https://shop.test", nil)
	if !ok || got != "https://shop.test/urun/gunes-kremi-50ml" {
		t.Fatalf("ürün biçimli yol seçilmedi: %q %v", got, ok)
	}
}

// Nothing product-shaped is not an error and not a guess: the root is measured
// and the scan says that is what happened.
func TestPickProductURL_SaysWhenItFoundNothing(t *testing.T) {
	if _, ok := pickProductURL(`<html><body><a href="/hakkimizda">x</a></body></html>`,
		"https://shop.test", nil); ok {
		t.Error("ürün bağlantısı yokken bulundu denildi")
	}
}

// A link off the storefront is not the storefront. Following one would measure
// somebody else's design and store it as this brand's.
func TestPickProductURL_StaysOnTheStorefront(t *testing.T) {
	root := `<html><body><a href="https://other.test/products/x">dış</a></body></html>`
	if got, ok := pickProductURL(root, "https://shop.test", []string{"x"}); ok {
		t.Errorf("başka alan adına çıkıldı: %q", got)
	}
}

type fakeSite struct {
	pages map[string]string
	calls []string
	err   error
}

func (f *fakeSite) Fetch(_ context.Context, target, _ string) (string, error) {
	f.calls = append(f.calls, target)
	if f.err != nil {
		return "", f.err
	}
	return f.pages[target], nil
}

const probedPDP = `<html data-mimir-scan="{&quot;bg&quot;:&quot;rgb(255, 255, 255)&quot;,` +
	`&quot;fg&quot;:&quot;rgb(20, 20, 20)&quot;,&quot;font&quot;:&quot;Inter, sans-serif&quot;,` +
	`&quot;size&quot;:&quot;16px&quot;,&quot;content&quot;:{&quot;found&quot;:true,&quot;size&quot;:&quot;15px&quot;}}"><body>x</body></html>`

func TestScanSite_ReadsAProductPageAndKeepsWhatThePreviewCanPaint(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()

	site := &fakeSite{pages: map[string]string{
		"https://shop.test":           `<html><body><a href="/urun/krem">k</a></body></html>`,
		"https://shop.test/urun/krem": probedPDP,
	}}
	s.UseSiteReader(site)
	s.lookup = testLookup

	got, err := s.ScanSite(ctx, imp.ID, "shop.test")
	if err != nil {
		t.Fatalf("ScanSite: %v", err)
	}
	if !got.Usable() {
		t.Fatalf("tarama kullanılamaz döndü: %+v", got)
	}
	if got.Theme.Background != "rgb(255, 255, 255)" || got.Theme.FontFamily != "Inter, sans-serif" {
		t.Errorf("tema okunmadı: %+v", got.Theme)
	}
	if !got.Content.Found || got.Content.FontSize != "15px" {
		t.Errorf("açıklama kabı okunmadı: %+v", got.Content)
	}
	if len(got.Pages) == 0 || got.Pages[len(got.Pages)-1] != "https://shop.test/urun/krem" {
		t.Errorf("ölçülen sayfa bildirilmedi: %v", got.Pages)
	}
	if got.ScannedAt.IsZero() {
		t.Error("tarama zamanı yok")
	}
}

// The scan is stored on the import and it does NOT move the brand hash. A hash
// that moved here would discard every approved draft in the catalogue because
// somebody corrected a colour.
func TestScanSite_DoesNotMoveTheBrandHash(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()
	before, err := s.CurrentDraftVersion(ctx, imp.ID, LangSource)
	if err != nil {
		t.Fatalf("CurrentDraftVersion: %v", err)
	}

	s.UseSiteReader(&fakeSite{pages: map[string]string{
		"https://shop.test":           `<html><body><a href="/urun/krem">k</a></body></html>`,
		"https://shop.test/urun/krem": probedPDP,
	}})
	s.lookup = testLookup
	if _, err := s.ScanSite(ctx, imp.ID, "shop.test"); err != nil {
		t.Fatalf("ScanSite: %v", err)
	}

	after, err := s.CurrentDraftVersion(ctx, imp.ID, LangSource)
	if err != nil {
		t.Fatalf("CurrentDraftVersion: %v", err)
	}
	if before != after {
		t.Errorf("marka taraması taslak sürümünü değiştirdi: %q → %q", before, after)
	}
}

// The vocabulary is the file's, and a storefront is not evidence about what the
// export contains. Widening it here would let a rewrite emit markup the file
// never had — the guarantee this package is built on.
func TestScanSite_DoesNotTouchTheVocabulary(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()
	before := imp.Brand.Vocab

	s.UseSiteReader(&fakeSite{pages: map[string]string{
		"https://shop.test":           `<html><body><a href="/urun/krem">k</a></body></html>`,
		"https://shop.test/urun/krem": probedPDP,
	}})
	s.lookup = testLookup
	if _, err := s.ScanSite(ctx, imp.ID, "shop.test"); err != nil {
		t.Fatalf("ScanSite: %v", err)
	}
	after, err := s.Get(ctx, imp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(after.Brand.Vocab.Tags) != len(before.Tags) {
		t.Errorf("etiket sözlüğü değişti: %d → %d", len(before.Tags), len(after.Brand.Vocab.Tags))
	}
}

// A storefront that never answers is a failed scan, not an empty theme stored
// as the brand's look.
func TestScanSite_AFailedFetchIsNotAnEmptyTheme(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	s.UseSiteReader(&fakeSite{err: errors.New("crawl4ai down")})
	s.lookup = testLookup
	if _, err := s.ScanSite(context.Background(), imp.ID, "shop.test"); err == nil {
		t.Fatal("erişilemeyen mağaza başarı döndü")
	}
	after, _ := s.Get(context.Background(), imp.ID)
	if after.Site.URL != "" {
		t.Errorf("başarısız tarama kaydedildi: %+v", after.Site)
	}
}

// A refusal has to reach the operator as a sentence they can act on. It is
// typed so the HTTP layer answers 400 with the reason rather than 500 with
// "internal error" — a typo in a shop address is not a server fault.
func TestCheckSiteURL_RefusalIsTyped(t *testing.T) {
	_, err := checkSiteURL(context.Background(), "http://10.0.0.5/", testLookup)
	if !errors.Is(err, ErrSiteRefused) {
		t.Fatalf("refusal not typed: %v", err)
	}
	if !strings.Contains(err.Error(), "10.0.0.5") {
		t.Errorf("hangi adres olduğu söylenmiyor: %v", err)
	}
}

func TestScanSite_UnreadableIsTyped(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	s.UseSiteReader(&fakeSite{pages: map[string]string{
		"https://shop.test":           `<html><body><a href="/urun/krem">k</a></body></html>`,
		"https://shop.test/urun/krem": `<html><body>hiç ölçüm yok</body></html>`,
	}})
	s.lookup = testLookup
	_, err := s.ScanSite(context.Background(), imp.ID, "shop.test")
	if !errors.Is(err, ErrSiteUnreadable) {
		t.Fatalf("unreadable not typed: %v", err)
	}
}

// The guard checks the address the operator typed; the crawler then follows
// redirects, which is verified behaviour of the pinned image. So a public
// hostname that redirects inward would be fetched under a guard that already
// said yes. The probe reports where it actually landed, and a landing place
// that would not have been allowed is refused rather than stored.
func TestScanSite_RefusesAScanThatRedirectedSomewhereItWouldNotHaveGone(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	landed := `<html data-mimir-scan="{&quot;bg&quot;:&quot;rgb(1, 2, 3)&quot;,` +
		`&quot;fg&quot;:&quot;rgb(4, 5, 6)&quot;,` +
		`&quot;href&quot;:&quot;http://169.254.169.254/latest/meta-data/&quot;}"><body>x</body></html>`
	s.UseSiteReader(&fakeSite{pages: map[string]string{
		"https://shop.test":           `<html><body><a href="/urun/krem">k</a></body></html>`,
		"https://shop.test/urun/krem": landed,
	}})
	s.lookup = testLookup

	_, err := s.ScanSite(context.Background(), imp.ID, "shop.test")
	if !errors.Is(err, ErrSiteRefused) {
		t.Fatalf("iç adrese yönlenen tarama kabul edildi: %v", err)
	}
	after, _ := s.Get(context.Background(), imp.ID)
	if after.Site.URL != "" {
		t.Error("reddedilen tarama yine de kaydedildi")
	}
}

// A redirect that stays on the public internet is ordinary — shops redirect to
// their canonical host constantly — and must not be refused.
func TestScanSite_AllowsAnOrdinaryPublicRedirect(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	landed := `<html data-mimir-scan="{&quot;bg&quot;:&quot;rgb(1, 2, 3)&quot;,` +
		`&quot;fg&quot;:&quot;rgb(4, 5, 6)&quot;,` +
		`&quot;href&quot;:&quot;https://www.example.com/urun/krem&quot;}"><body>x</body></html>`
	s.UseSiteReader(&fakeSite{pages: map[string]string{
		"https://shop.test":           `<html><body><a href="/urun/krem">k</a></body></html>`,
		"https://shop.test/urun/krem": landed,
	}})
	s.lookup = testLookup

	if _, err := s.ScanSite(context.Background(), imp.ID, "shop.test"); err != nil {
		t.Fatalf("olağan yönlendirme reddedildi: %v", err)
	}
}

// The root page is fetched first, to find a product link on it. That fetch had
// no landing check at all while the product fetch had one, so a host that
// redirected inward was read, parsed, and mined for links before anything
// looked at where it had gone.
func TestScanSite_ChecksWhereTheRootPageLandedToo(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	root := `<html data-mimir-scan="{&quot;href&quot;:&quot;http://10.0.0.7/admin&quot;}">` +
		`<body><a href="/urun/krem">k</a></body></html>`
	s.UseSiteReader(&fakeSite{pages: map[string]string{"https://shop.test": root}})
	s.lookup = testLookup

	_, err := s.ScanSite(context.Background(), imp.ID, "shop.test")
	if !errors.Is(err, ErrSiteRefused) {
		t.Fatalf("iç adrese yönlenen ana sayfa kabul edildi: %v", err)
	}
}

// RFC 6598 carrier-grade NAT. Go reports it as global unicast and not private,
// so the four stdlib predicates all pass it — and container and ISP networks
// really do route internal services through it.
func TestCheckSiteURL_RefusesTheSharedAddressSpace(t *testing.T) {
	lookup := func(_ context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("100.64.0.1")}, nil
	}
	if _, err := checkSiteURL(context.Background(), "https://cgnat.test", lookup); err == nil {
		t.Error("100.64.0.0/10 kabul edildi")
	}
	// The boundaries: 100.63.255.255 and 100.128.0.0 are ordinary public space.
	for _, ok := range []string{"100.63.255.255", "100.128.0.0"} {
		l := func(_ context.Context, _ string) ([]net.IP, error) {
			return []net.IP{net.ParseIP(ok)}, nil
		}
		if _, err := checkSiteURL(context.Background(), "https://public.test", l); err != nil {
			t.Errorf("%s reddedildi, kamuya açık olmalı: %v", ok, err)
		}
	}
}
