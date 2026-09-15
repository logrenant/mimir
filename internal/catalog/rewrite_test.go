package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
)

// countingCompleter answers with a fixed rewrite and counts what it was asked
// for. "A resumed pass costs nothing for what it already wrote" is only a claim
// worth making if something is counting.
type countingCompleter struct {
	mu     sync.Mutex
	calls  int
	failAt int   // 1-based; the call that returns err
	err    error // returned at failAt, and at every call after it
	answer rewritten
}

func (c *countingCompleter) CompleteWith(_ context.Context, _ llm.Class, _ llm.Selection, _ llm.Request) (llm.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.failAt > 0 && c.calls >= c.failAt {
		return llm.Response{}, c.err
	}
	body, _ := json.Marshal(c.answer)
	return llm.Response{Structured: body, Provider: "agy", Model: "test"}, nil
}

func (c *countingCompleter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// countingResearcher is the market half, likewise counted.
type countingResearcher struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (r *countingResearcher) Research(_ context.Context, _ ResearchRequest) (Findings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.err != nil {
		return Findings{}, r.err
	}
	return Findings{
		Summary:   "Rakipler hacmi ve parabensizliği öne çıkarıyor.",
		KeyPoints: []string{"hacim ilk cümlede", "parabensiz bir ayrım noktası"},
		Sources:   []Source{{Title: "rakip", URL: "https://rakip.example/a"}},
		Refined:   true,
	}, nil
}

func (r *countingResearcher) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func goodAnswer() rewritten {
	return rewritten{
		Title:          "Nemlendirici Krem",
		SEOTitle:       "Nemlendirici Krem | Marka",
		SEODescription: "Kuru ciltler için 50 ml yoğun nemlendirici krem.",
		Tags:           []string{"krem", "nemlendirici"},
		Blocks: []struct {
			Kind  string `json:"kind"`
			Level int    `json:"level"`
			Text  string `json:"text"`
		}{
			{Kind: "paragraph", Text: "Kuru ciltler için 50 ml **yoğun** nemlendirici."},
			{Kind: "listitem", Text: "Parabensiz"},
		},
	}
}

// rewriteFixture stands up an import backed by memory, with both halves faked.
func rewriteFixture(t *testing.T) (*Studio, *memStore, Import, []string, *countingCompleter, *countingResearcher) {
	t.Helper()
	ms := newMemStore()
	cc := &countingCompleter{answer: goodAnswer()}
	cr := &countingResearcher{}

	s := New(config.Load(), ms, cc)
	s.UseResearcher(cr)
	s.UseClock(func() time.Time { return time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC) })

	imp, err := s.Import(context.Background(), "ikas.csv", readFixture(t, "ikas.csv"), llm.Selection{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	// The voice distil spent one call; the rewrite counters start from here.
	cc.mu.Lock()
	cc.calls = 0
	cc.mu.Unlock()

	views, err := s.Products(context.Background(), ProductFilter{ImportID: imp.ID}, "v")
	if err != nil {
		t.Fatalf("Products: %v", err)
	}
	ids := make([]string, 0, len(views))
	for _, v := range views {
		ids = append(ids, v.ID)
	}
	return s, ms, imp, ids, cc, cr
}

// The claim the whole layer is built on: a pass that ran once costs nothing to
// run again.
func TestRewrite_SecondPassOverTheSameProductsSpendsNothing(t *testing.T) {
	s, _, imp, ids, cc, cr := rewriteFixture(t)
	ctx := context.Background()
	req := RewriteRequest{ImportID: imp.ID, ProductIDs: ids, Research: true}

	first, err := s.Rewrite(ctx, req, nil)
	if err != nil {
		t.Fatalf("ilk koşu: %v", err)
	}
	if first.Written != len(ids) {
		t.Fatalf("beklenen %d yazım, alınan %d (%+v)", len(ids), first.Written, first)
	}
	modelCalls, researchCalls := cc.count(), cr.count()
	if modelCalls == 0 || researchCalls == 0 {
		t.Fatalf("ilk koşu hiçbir şey harcamadı: model=%d araştırma=%d", modelCalls, researchCalls)
	}

	second, err := s.Rewrite(ctx, req, nil)
	if err != nil {
		t.Fatalf("ikinci koşu: %v", err)
	}
	if second.Cached != len(ids) || second.Written != 0 {
		t.Errorf("ikinci koşu yeniden yazdı: %+v", second)
	}
	if cc.count() != modelCalls {
		t.Errorf("ikinci koşu %d model çağrısı daha harcadı", cc.count()-modelCalls)
	}
	if cr.count() != researchCalls {
		t.Errorf("ikinci koşu %d araştırma daha harcadı", cr.count()-researchCalls)
	}
}

// The reason the two cache keys have different shapes. Correcting the brand
// voice must be a cheap thing to do, and it is only cheap if the research
// survives it.
func TestRewrite_EditingTheVoiceRedraftsButKeepsTheResearch(t *testing.T) {
	s, ms, imp, ids, cc, cr := rewriteFixture(t)
	ctx := context.Background()
	req := RewriteRequest{ImportID: imp.ID, ProductIDs: ids, Research: true}

	if _, err := s.Rewrite(ctx, req, nil); err != nil {
		t.Fatalf("ilk koşu: %v", err)
	}
	researchRows := len(ms.research)
	researchCalls := cr.count()
	modelCalls := cc.count()

	edited, err := s.SetBrand(ctx, imp.ID, Voice{Address: "siz", Tone: "daha sıcak"})
	if err != nil {
		t.Fatalf("SetBrand: %v", err)
	}
	if edited.Brand.Version == imp.Brand.Version {
		t.Fatal("marka sürümü değişmedi; test hiçbir şey ölçmüyor")
	}

	after, err := s.Rewrite(ctx, req, nil)
	if err != nil {
		t.Fatalf("ikinci koşu: %v", err)
	}
	if after.Written != len(ids) {
		t.Errorf("ses düzeltildi ama metinler yeniden yazılmadı: %+v", after)
	}
	if cc.count() <= modelCalls {
		t.Error("yeniden yazım için hiç model çağrısı yapılmadı")
	}
	if cr.count() != researchCalls {
		t.Errorf("marka düzenlemesi %d araştırmayı yeniden satın aldı", cr.count()-researchCalls)
	}
	if len(ms.research) != researchRows {
		t.Errorf("araştırma satırları değişti: %d → %d", researchRows, len(ms.research))
	}
}

// A rate limit is the account's problem, not one product's. Carrying on would
// spend the rest of the catalog discovering the same thing several hundred more
// times — and everything already written has to survive.
func TestRewrite_StopsOnARateLimitAndKeepsWhatItAlreadyWrote(t *testing.T) {
	s, ms, imp, ids, cc, _ := rewriteFixture(t)
	if len(ids) < 2 {
		t.Skip("bu fikstür tek ürünlü")
	}
	cc.failAt = 2
	cc.err = llm.ErrRateLimited

	rep, err := s.Rewrite(context.Background(), RewriteRequest{
		ImportID: imp.ID, ProductIDs: ids, Research: false,
	}, nil)

	if !errors.Is(err, llm.ErrRateLimited) {
		t.Fatalf("beklenen ErrRateLimited, alınan %v", err)
	}
	if rep.Written != 1 {
		t.Errorf("limitten önce yazılan taslak sayısı: %d", rep.Written)
	}
	if len(ms.drafts) != 1 {
		t.Errorf("yazılmış taslak kayboldu: %d satır", len(ms.drafts))
	}
	// And the resumed pass pays only for what is left.
	cc.failAt = 0
	after, err := s.Rewrite(context.Background(), RewriteRequest{
		ImportID: imp.ID, ProductIDs: ids, Research: false,
	}, nil)
	if err != nil {
		t.Fatalf("sürdürülen koşu: %v", err)
	}
	if after.Cached != 1 {
		t.Errorf("sürdürülen koşu önceki taslağı yeniden yazdı: %+v", after)
	}
}

// Research is not the product. One that could not be researched is written from
// its own data and says so (SD-6).
func TestRewrite_AFailedResearchDoesNotStopThePass(t *testing.T) {
	s, _, imp, ids, _, cr := rewriteFixture(t)
	cr.err = errors.New("crawl4ai not reachable")

	rep, err := s.Rewrite(context.Background(), RewriteRequest{
		ImportID: imp.ID, ProductIDs: ids, Research: true,
	}, nil)
	if err != nil {
		t.Fatalf("araştırma hatası koşuyu düşürdü: %v", err)
	}
	if rep.Written != len(ids) {
		t.Errorf("beklenen %d yazım, alınan %d", len(ids), rep.Written)
	}
	if len(rep.Notes) == 0 {
		t.Error("kısmi cevabın kısmi olduğu hiçbir yerde yazmıyor")
	}
}

// An SEO title cut mid-word is worse than the one the store already had.
func TestRewrite_RejectsAnOverlongSEOFieldWithoutLosingTheProduct(t *testing.T) {
	s, _, imp, ids, cc, _ := rewriteFixture(t)
	answer := goodAnswer()
	answer.SEOTitle = strings.Repeat("uzun ", 30)
	cc.answer = answer

	rep, err := s.Rewrite(context.Background(), RewriteRequest{
		ImportID: imp.ID, ProductIDs: ids[:1], Research: false,
	}, nil)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if rep.Written != 1 {
		t.Fatalf("ürün kaybedildi: %+v", rep)
	}

	view, err := s.Product(context.Background(), ids[0], LangSource, rep.Version)
	if err != nil || view.Draft == nil {
		t.Fatalf("Product: %v", err)
	}
	if n := len([]rune(view.Draft.Content.SEOTitle)); n > 60 {
		t.Errorf("tavanı aşan SEO başlık kaydedildi: %d karakter", n)
	}
	if len(view.Draft.Notes) == 0 {
		t.Error("reddedilen alan operatöre söylenmiyor")
	}
}

// The claim of the package, on the rewrite path: model output cannot widen the
// brand's markup, and cannot invent a link.
func TestRewrite_CannotWidenTheBrandsMarkupOrInventALink(t *testing.T) {
	s, _, imp, ids, cc, _ := rewriteFixture(t)
	answer := goodAnswer()
	answer.Blocks = []struct {
		Kind  string `json:"kind"`
		Level int    `json:"level"`
		Text  string `json:"text"`
	}{
		{Kind: "heading", Level: 1, Text: "Uydurma başlık"},
		{Kind: "paragraph", Text: "Detay [burada](https://uydurma.example/yok)."},
	}
	cc.answer = answer

	rep, err := s.Rewrite(context.Background(), RewriteRequest{
		ImportID: imp.ID, ProductIDs: ids[:1], Research: false,
	}, nil)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	view, err := s.Product(context.Background(), ids[0], LangSource, rep.Version)
	if err != nil || view.Draft == nil {
		t.Fatalf("Product: %v", err)
	}
	html := view.Draft.Content.DescriptionHTML

	if strings.Contains(html, "uydurma.example") {
		t.Errorf("model bir URL uydurdu ve geçti: %s", html)
	}
	if strings.Contains(html, "<h1") {
		t.Errorf("markanın kullanmadığı bir etiket çıktıya girdi: %s", html)
	}
	if !strings.Contains(html, "Uydurma başlık") || !strings.Contains(html, "Detay") {
		t.Errorf("metin kayboldu: %s", html)
	}
}

// A person's words are never overwritten by a pass, and the pass says it
// skipped rather than reporting a failure.
func TestRewrite_SkipsAProductTheOperatorEditedByHand(t *testing.T) {
	s, _, imp, ids, _, _ := rewriteFixture(t)
	ctx := context.Background()

	kit, err := s.Get(ctx, imp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	version := s.DraftVersion(kit.Brand, llm.Selection{}, "", LangSource)
	if _, err := s.SaveDraft(ctx, ids[0], version, LangSource,
		Content{Title: "Elle yazıldı"}, []Field{FieldTitle}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	rep, err := s.Rewrite(ctx, RewriteRequest{
		ImportID: imp.ID, ProductIDs: ids[:1], Research: false,
	}, nil)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	// It is a cache hit, not a write and not a failure: the draft under this
	// exact version already exists.
	if rep.Written != 0 || rep.Failed != 0 {
		t.Errorf("elle yazılmış ürün yeniden yazıldı ya da hata sayıldı: %+v", rep)
	}

	view, err := s.Product(ctx, ids[0], LangSource, version)
	if err != nil || view.Draft == nil {
		t.Fatalf("Product: %v", err)
	}
	if view.Draft.Content.Title != "Elle yazıldı" {
		t.Errorf("elle yazılan başlık ezildi: %q", view.Draft.Content.Title)
	}
}

// The narration is how an operator watches a run they cannot otherwise see.
func TestRewrite_NarratesAStepPerProduct(t *testing.T) {
	s, _, imp, ids, _, _ := rewriteFixture(t)
	sink := &recordingSink{}

	if _, err := s.Rewrite(context.Background(), RewriteRequest{
		ImportID: imp.ID, ProductIDs: ids, Research: true,
	}, sink); err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	research, draft := sink.count("research"), sink.count("draft")
	if research != len(ids) || draft != len(ids) {
		t.Errorf("beklenen ürün başına iki adım, alınan research=%d draft=%d (%d ürün)",
			research, draft, len(ids))
	}
}

func TestRewrite_RefusesToWriteAnIdentityField(t *testing.T) {
	s, _, imp, ids, _, _ := rewriteFixture(t)
	_, err := s.Rewrite(context.Background(), RewriteRequest{
		ImportID: imp.ID, ProductIDs: ids, Fields: []Field{FieldHandle},
	}, nil)
	if !errors.Is(err, ErrNotWritable) {
		t.Fatalf("beklenen ErrNotWritable, alınan %v", err)
	}
}

// A daemon with no Crawl4AI still rewrites; it just says the copy was written
// without a market behind it.
func TestRewrite_SaysSoWhenThereIsNoResearchPipelineAtAll(t *testing.T) {
	ms := newMemStore()
	cc := &countingCompleter{answer: goodAnswer()}
	s := New(config.Load(), ms, cc)
	ctx := context.Background()

	imp, err := s.Import(ctx, "ikas.csv", readFixture(t, "ikas.csv"), llm.Selection{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	views, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v")
	ids := []string{views[0].ID}

	rep, err := s.Rewrite(ctx, RewriteRequest{
		ImportID: imp.ID, ProductIDs: ids, Research: true,
	}, nil)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if rep.Written != 1 {
		t.Errorf("araştırma hattı yokken ürün yazılmadı: %+v", rep)
	}
	joined := strings.Join(rep.Notes, " ")
	if !strings.Contains(joined, "araştırma") {
		t.Errorf("araştırmanın yapılmadığı söylenmiyor: %v", rep.Notes)
	}
}

// The research query asks the market what it says, and this store's own
// marketing copy is not the market.
func TestResearchQuery_AsksAboutTheProductAndNotAboutItsOwnCopy(t *testing.T) {
	p := Product{Category: "Cilt Bakımı"}
	p.Original.Title = "Nemlendirici Krem"
	p.Original.DescriptionHTML = "<p>eşsiz formülümüz</p>"

	got := researchQuery(p)
	if got != "Nemlendirici Krem Cilt Bakımı" {
		t.Errorf("beklenmeyen sorgu: %q", got)
	}
	if strings.Contains(got, "eşsiz") {
		t.Error("mağazanın kendi metni pazara soruluyor")
	}
}

func TestResearchVersion_CarriesTheModelAndNotTheBrand(t *testing.T) {
	s := New(config.Load(), nil, nil)
	plain := s.researchVersion(llm.Selection{})
	withModel := s.researchVersion(llm.Selection{Provider: "claude", Model: "x"})
	if plain == withModel {
		t.Error("model seçimi araştırma anahtarına girmiyor")
	}
	// The brand hash is deliberately absent — there is no parameter for it, and
	// this test exists so adding one is a deliberate act rather than a slip.
	if strings.Contains(plain, "brand") {
		t.Errorf("marka hash'i araştırma anahtarına sızmış: %q", plain)
	}
}

// recordingSink is the narration, captured.
type recordingSink struct {
	mu    sync.Mutex
	steps []string
	said  []string
}

func (r *recordingSink) Step(name string, _ any) func(bool, string) {
	r.mu.Lock()
	r.steps = append(r.steps, name)
	r.mu.Unlock()
	return func(bool, string) {}
}

func (r *recordingSink) Say(text string) {
	r.mu.Lock()
	r.said = append(r.said, text)
	r.mu.Unlock()
}

func (r *recordingSink) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.steps {
		if s == name {
			n++
		}
	}
	return n
}

// A provider that cannot be run is the machine's problem, not this product's.
// Carrying on would rediscover the same fact once per remaining product — four
// hundred subprocess launches to learn one thing — and end with a card marked
// completed above a catalog nobody touched.
func TestRewrite_StopsAtOnceWhenTheProviderCannotBeRun(t *testing.T) {
	s, ms, imp, ids, cc, _ := rewriteFixture(t)
	if len(ids) < 2 {
		t.Skip("bu fikstür tek ürünlü")
	}
	cc.failAt = 1
	cc.err = llm.ErrProviderUnavailable

	rep, err := s.Rewrite(context.Background(), RewriteRequest{
		ImportID: imp.ID, ProductIDs: ids, Research: false,
	}, nil)

	if err == nil {
		t.Fatal("sağlayıcı kullanılamazken koşu tamamlandı olarak döndü")
	}
	if !errors.Is(err, llm.ErrProviderUnavailable) {
		t.Errorf("sebep sarmalanmadı: %v", err)
	}
	if cc.count() != 1 {
		t.Errorf("aynı gerçeği öğrenmek için %d çağrı harcandı", cc.count())
	}
	if rep.Written != 0 || len(ms.drafts) != 0 {
		t.Errorf("hiçbir şey yazılmamalıydı: %+v", rep)
	}
}

// The other half of the same rule: a pass where every product failed for its
// own reason is a failed pass, not a completed one.
func TestRewrite_AProductionOfNothingIsAFailureNotACompletion(t *testing.T) {
	s, _, imp, ids, cc, _ := rewriteFixture(t)
	// An answer with no blocks: rejected per product, not fatal per pass.
	empty := goodAnswer()
	empty.Blocks = nil
	cc.answer = empty

	rep, err := s.Rewrite(context.Background(), RewriteRequest{
		ImportID: imp.ID, ProductIDs: ids, Research: false,
	}, nil)

	if err == nil {
		t.Fatal("hiçbir ürün yazılmadığı hâlde koşu tamamlandı sayıldı")
	}
	if rep.Failed != len(ids) {
		t.Errorf("beklenen %d başarısız ürün, alınan %d", len(ids), rep.Failed)
	}
	// Every product was tried: this is a degradation that happened to be total,
	// not a pass that gave up on the first one.
	if cc.count() != len(ids) {
		t.Errorf("ürün başına deneme yapılmadı: %d çağrı", cc.count())
	}
}

// And a pass that wrote nothing because everything was already cached is not a
// failure — it is the resumed pass this whole design is for.
func TestRewrite_AllCachedIsNotAFailure(t *testing.T) {
	s, _, imp, ids, _, _ := rewriteFixture(t)
	ctx := context.Background()
	req := RewriteRequest{ImportID: imp.ID, ProductIDs: ids, Research: false}

	if _, err := s.Rewrite(ctx, req, nil); err != nil {
		t.Fatalf("ilk koşu: %v", err)
	}
	rep, err := s.Rewrite(ctx, req, nil)
	if err != nil {
		t.Fatalf("tamamı önbellekten gelen koşu hata verdi: %v", err)
	}
	if rep.Cached != len(ids) {
		t.Errorf("beklenen %d önbellek isabeti, alınan %d", len(ids), rep.Cached)
	}
}

// arabicAnswer is a rewrite that passes the deterministic gate. It states no
// number, because the Turkish body it is written from states none either — the
// gate refuses a measurement the source does not make, which is the cheapest
// anti-hallucination check there is.
func arabicAnswer() rewritten {
	return rewritten{
		Title:          "كريم مرطب",
		SEOTitle:       "كريم مرطب للبشرة الجافة",
		SEODescription: "كريم مرطب مكثف للبشرة الجافة يدوم طوال اليوم.",
		Tags:           []string{"كريم", "مرطب"},
		Blocks: []struct {
			Kind  string `json:"kind"`
			Level int    `json:"level"`
			Text  string `json:"text"`
		}{
			{Kind: "paragraph", Text: "مرطب مكثف للبشرة الجافة، يدوم طوال اليوم."},
			{Kind: "listitem", Text: "خالٍ من البارابين"},
		},
	}
}

// langFixture stands up an import whose file actually has an Arabic column.
func langFixture(t *testing.T, answer rewritten) (*Studio, Import, []string, *countingCompleter) {
	t.Helper()
	ms := newMemStore()
	cc := &countingCompleter{answer: answer}
	s := New(config.Load(), ms, cc)
	s.UseClock(func() time.Time { return time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC) })

	imp, err := s.Import(context.Background(), "ikas-fields.csv",
		readFixture(t, "ikas-fields.csv"), llm.Selection{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	cc.mu.Lock()
	cc.calls = 0
	cc.mu.Unlock()

	views, err := s.Products(context.Background(), ProductFilter{ImportID: imp.ID}, "v")
	if err != nil {
		t.Fatalf("Products: %v", err)
	}
	ids := make([]string, 0, len(views))
	for _, v := range views {
		ids = append(ids, v.ID)
	}
	return s, imp, ids, cc
}

// Nobody at the merchant reads Arabic. A Turkish rewrite that goes slightly
// wrong is caught by the operator looking at the screen; an Arabic one is not
// caught by anybody until a customer reads it. That asymmetry is what the
// second call buys, and it is why it only runs for a target language.
func TestRewrite_ReviewsATargetLanguageAndDoesNotReviewTheSourceOne(t *testing.T) {
	ctx := context.Background()

	s, imp, ids, cc := langFixture(t, arabicAnswer())
	rep, err := s.Rewrite(ctx, RewriteRequest{
		ImportID: imp.ID, ProductIDs: ids[:1], Lang: LangAR,
	}, nil)
	if err != nil {
		t.Fatalf("Rewrite: %v (%+v)", err, rep.Notes)
	}
	if rep.Written != 1 {
		t.Fatalf("beklenen 1 yazım, alınan %d (%v)", rep.Written, rep.Notes)
	}
	if got := cc.count(); got != 2 {
		t.Errorf("hedef dilde iki çağrı bekleniyordu (yazım + gözden geçirme), alınan %d", got)
	}

	s2, imp2, ids2, cc2 := langFixture(t, goodAnswer())
	if _, err := s2.Rewrite(ctx, RewriteRequest{
		ImportID: imp2.ID, ProductIDs: ids2[:1],
	}, nil); err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if got := cc2.count(); got != 1 {
		t.Errorf("kaynak dilde tek çağrı bekleniyordu, alınan %d", got)
	}
}

// The Arabic body ships with a direction. Without one the bidirectional
// algorithm resolves punctuation and numbers against a paragraph direction
// that is not there, and "50 مل، يدوم" renders in the wrong order.
func TestRewrite_AnArabicDraftCarriesItsDirection(t *testing.T) {
	s, imp, ids, _ := langFixture(t, arabicAnswer())
	ctx := context.Background()

	if _, err := s.Rewrite(ctx, RewriteRequest{
		ImportID: imp.ID, ProductIDs: ids[:1], Lang: LangAR,
	}, nil); err != nil {
		t.Fatalf("Rewrite: %v", err)
	}

	version, err := s.CurrentDraftVersion(ctx, imp.ID, LangAR)
	if err != nil {
		t.Fatalf("CurrentDraftVersion: %v", err)
	}
	views, err := s.Products(ctx, ProductFilter{ImportID: imp.ID}, version)
	if err != nil {
		t.Fatalf("Products: %v", err)
	}
	if views[0].Draft == nil {
		t.Fatal("Arapça taslak saklanmadı")
	}
	html := views[0].Draft.Content.DescriptionHTML
	if !strings.Contains(html, `dir="rtl"`) || !strings.Contains(html, `lang="ar"`) {
		t.Errorf("Arapça gövde yönsüz yazıldı: %q", html)
	}
}

// A draft the deterministic gate refuses on both passes is not a draft with a
// caveat. The Arabic cell was empty, so there is no older value being kept by
// storing wrong copy.
func TestRewrite_FailsTheProductWhenTheCopyIsNotInTheLanguageItClaims(t *testing.T) {
	// The model answers in Turkish when it was asked for Arabic — and answers
	// the same way again when the reviewer asks it to fix that.
	s, imp, ids, cc := langFixture(t, goodAnswer())
	ctx := context.Background()

	rep, err := s.Rewrite(ctx, RewriteRequest{
		ImportID: imp.ID, ProductIDs: ids[:1], Lang: LangAR,
	}, nil)
	if err == nil {
		t.Fatal("Türkçe yazılmış bir Arapça taslak kabul edildi")
	}
	if rep.Failed != 1 || rep.Written != 0 {
		t.Errorf("beklenen 1 başarısız 0 yazım, alınan %d/%d", rep.Failed, rep.Written)
	}
	if cc.count() != 2 {
		t.Errorf("gözden geçirene düzeltme şansı verilmedi: %d çağrı", cc.count())
	}
	var saidWhy bool
	for _, n := range rep.Notes {
		if strings.Contains(n, "turkish_leak") || strings.Contains(n, "not_in_language") {
			saidWhy = true
		}
	}
	if !saidWhy {
		t.Errorf("neden reddedildiği söylenmedi: %v", rep.Notes)
	}
}

// SD-6 degrades rather than crashing, but the deterministic gate is the floor
// and it does not degrade: a draft the machine check already refused is not
// stored just because the reviewer could not be reached.
func TestRewrite_AnUnreachableReviewerDoesNotLowerTheMachineFloor(t *testing.T) {
	ctx := context.Background()

	// Clean copy, reviewer unreachable → stored, with the failure said out loud.
	s, imp, ids, cc := langFixture(t, arabicAnswer())
	cc.failAt = 2
	cc.err = errors.New("reviewer unreachable")
	rep, err := s.Rewrite(ctx, RewriteRequest{
		ImportID: imp.ID, ProductIDs: ids[:1], Lang: LangAR,
	}, nil)
	if err != nil {
		t.Fatalf("temiz metin gözden geçiren yokken de saklanmalıydı: %v", err)
	}
	if rep.Written != 1 {
		t.Fatalf("beklenen 1 yazım, alınan %d (%v)", rep.Written, rep.Notes)
	}
	// The note lives on the draft rather than in the card's narration: it is
	// about this one product, and a per-product line in a four-hundred-product
	// pass is a narration nobody can read.
	version, err := s.CurrentDraftVersion(ctx, imp.ID, LangAR)
	if err != nil {
		t.Fatalf("CurrentDraftVersion: %v", err)
	}
	views, err := s.Products(ctx, ProductFilter{ImportID: imp.ID}, version)
	if err != nil {
		t.Fatalf("Products: %v", err)
	}
	if views[0].Draft == nil {
		t.Fatal("taslak saklanmadı")
	}
	var told bool
	for _, n := range views[0].Draft.Notes {
		if strings.Contains(n, "Anadil kontrolü yapılamadı") {
			told = true
		}
	}
	if !told {
		t.Errorf("gözden geçirmenin yapılamadığı söylenmedi: %v", views[0].Draft.Notes)
	}

	// Copy the machine check already refused, reviewer unreachable → refused.
	s2, imp2, ids2, cc2 := langFixture(t, goodAnswer())
	cc2.failAt = 2
	cc2.err = errors.New("reviewer unreachable")
	if _, err := s2.Rewrite(ctx, RewriteRequest{
		ImportID: imp2.ID, ProductIDs: ids2[:1], Lang: LangAR,
	}, nil); err == nil {
		t.Error("makine kapısının reddettiği metin, gözden geçiren yok diye saklandı")
	}
}

// Two languages of the same product are two drafts, and approving one must not
// touch the other.
func TestRewrite_TheTwoLanguagesOfOneProductAreSeparateDrafts(t *testing.T) {
	s, imp, ids, _ := langFixture(t, arabicAnswer())
	ctx := context.Background()

	if _, err := s.Rewrite(ctx, RewriteRequest{
		ImportID: imp.ID, ProductIDs: ids[:1], Lang: LangAR,
	}, nil); err != nil {
		t.Fatalf("Rewrite(ar): %v", err)
	}

	arVersion, _ := s.CurrentDraftVersion(ctx, imp.ID, LangAR)
	srcVersion, _ := s.CurrentDraftVersion(ctx, imp.ID, LangSource)
	if arVersion == srcVersion {
		t.Fatal("iki dil aynı anahtarı paylaşıyor")
	}

	ar, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, arVersion)
	src, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, srcVersion)
	if ar[0].Draft == nil {
		t.Error("Arapça taslak yok")
	}
	if src[0].Draft != nil {
		t.Error("Arapça pas kaynak dilin taslağını da yazdı")
	}
}

// The configuration is a gate, not a default. A card written before the switch
// was flipped — or one from a client that names fields itself — still cannot
// write a field the operator switched off, and the operator would otherwise
// find out from the exported file.
func TestRewrite_HonoursTheOperatorsFieldConfigurationOverTheCard(t *testing.T) {
	s, _, imp, ids, _, _ := rewriteFixture(t)
	ctx := context.Background()

	if _, err := s.SetWrite(ctx, imp.ID, []LangField{{Field: FieldDescriptionHTML}}); err != nil {
		t.Fatalf("SetWrite: %v", err)
	}

	// The card asks for the title as well. The configuration wins.
	rep, err := s.Rewrite(ctx, RewriteRequest{
		ImportID:   imp.ID,
		ProductIDs: ids[:1],
		Fields:     []Field{FieldTitle, FieldDescriptionHTML},
	}, nil)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}

	version, _ := s.CurrentDraftVersion(ctx, imp.ID, LangSource)
	views, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, version)
	draft := views[0].Draft
	if draft == nil {
		t.Fatal("taslak yazılmadı")
	}
	if draft.Content.Title != views[0].Original.Title {
		t.Errorf("kapatılan alan yine de yazıldı: %q", draft.Content.Title)
	}
	if draft.Content.DescriptionHTML == views[0].Original.DescriptionHTML {
		t.Error("açık bırakılan alan yazılmadı")
	}

	// And the pass says what it did not touch. A green card above a field
	// nobody changed is worse than a card that explains itself.
	var told bool
	for _, n := range rep.Notes {
		if strings.Contains(n, "alan ayarlarında kapalı") && strings.Contains(n, "title") {
			told = true
		}
	}
	if !told {
		t.Errorf("atlanan alan söylenmedi: %v", rep.Notes)
	}
}

// A pass with nothing left to write is refused rather than reported as a
// success that changed nothing.
func TestRewrite_RefusesAPassWhoseEveryFieldIsSwitchedOff(t *testing.T) {
	s, _, imp, ids, cc, _ := rewriteFixture(t)
	ctx := context.Background()

	if _, err := s.SetWrite(ctx, imp.ID, []LangField{{Field: FieldTags}}); err != nil {
		t.Fatalf("SetWrite: %v", err)
	}
	_, err := s.Rewrite(ctx, RewriteRequest{
		ImportID:   imp.ID,
		ProductIDs: ids[:1],
		Fields:     []Field{FieldTitle},
	}, nil)
	if !errors.Is(err, ErrNothingToWrite) {
		t.Fatalf("beklenen ErrNothingToWrite, alınan %v", err)
	}
	if cc.count() != 0 {
		t.Errorf("yazacak alanı olmayan pas yine de model çağırdı: %d", cc.count())
	}
}
