package catalogjob

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/catalog"
	"github.com/logrenant/mimir/internal/coderunner"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
	"github.com/logrenant/mimir/internal/settings"
	"github.com/logrenant/mimir/internal/store"
)

type fakeStudio struct {
	req catalog.RewriteRequest
	rep catalog.Report
	err error
}

func (f *fakeStudio) Rewrite(_ context.Context, req catalog.RewriteRequest, out catalog.Sink) (catalog.Report, error) {
	f.req = req
	if out != nil {
		done := out.Step("draft", nil)
		done(true, "ok")
	}
	return f.rep, f.err
}

// recordingSink is the runner's Sink, captured.
type recordingSink struct {
	steps []string
	said  []string
}

func (r *recordingSink) Started(string, string) {}
func (r *recordingSink) Stderr(string)          {}
func (r *recordingSink) Say(text string)        { r.said = append(r.said, text) }
func (r *recordingSink) Step(name string, _ any) func(bool, string) {
	r.steps = append(r.steps, name)
	return func(bool, string) {}
}

func row(params string) store.RunRow {
	return store.RunRow{ID: "run_1", Agent: Agent, Params: params, Prompt: "katalog"}
}

// A card that cannot be decoded is refused before anything is claimed, so the
// reason lands on the card rather than in the middle of a pass that has already
// spent money.
func TestPrepare_RefusesACardWhoseParamsWillNotDecode(t *testing.T) {
	e := New(config.Load(), &fakeStudio{}, nil)
	for name, params := range map[string]string{
		"boş":              "",
		"bozuk json":       "{",
		"import yok":       `{"product_ids":["a"]}`,
		"ürün yok":         `{"import_id":"imp_1"}`,
		"boş ürün listesi": `{"import_id":"imp_1","product_ids":[]}`,
	} {
		if err := e.Prepare(context.Background(), row(params)); err == nil {
			t.Errorf("%s: kabul edildi", name)
		}
	}
}

// Unlike lead-gen's, there is no fallback to the prompt: a region is a sentence
// somebody can type and a set of product ids is not.
func TestParse_DoesNotGuessProductsFromThePrompt(t *testing.T) {
	r := row("")
	r.Prompt = "bütün ürünleri yeniden yaz"
	if _, err := Parse(r); !errors.Is(err, ErrNoProducts) {
		t.Fatalf("beklenen ErrNoProducts, alınan %v", err)
	}
}

func TestPrepare_RefusesAnIdentityField(t *testing.T) {
	e := New(config.Load(), &fakeStudio{}, nil)
	err := e.Prepare(context.Background(),
		row(`{"import_id":"imp_1","product_ids":["a"],"fields":["handle"]}`))
	if !errors.Is(err, catalog.ErrNotWritable) {
		t.Fatalf("beklenen ErrNotWritable, alınan %v", err)
	}
}

// A worker-lane job spends the daemon's own model budget, not a claude
// credential slot held for the life of a session — gating it on a connected
// coding account would refuse work for a reason that does not apply to it.
func TestExecutor_RunsOnTheWorkerLaneAndNeedsNoProject(t *testing.T) {
	e := New(config.Load(), &fakeStudio{}, nil)
	if e.Agent() != "catalog" {
		t.Errorf("ajan adı: %q", e.Agent())
	}
	if e.Lane() != coderunner.LaneWorker {
		t.Error("katalog kartı hesap lane'ine kondu")
	}
}

// Research defaults to true when the key is absent: quietly giving somebody the
// cheaper thing because they left a field out is how a tool ends up producing
// worse copy than it advertises.
func TestParams_ResearchDefaultsOn(t *testing.T) {
	p, err := Parse(row(`{"import_id":"i","product_ids":["a"]}`))
	if err != nil || !p.DoResearch() {
		t.Fatalf("araştırma varsayılan olarak kapalı: %v %+v", err, p)
	}
	off, err := Parse(row(`{"import_id":"i","product_ids":["a"],"research":false}`))
	if err != nil || off.DoResearch() {
		t.Fatalf("açıkça kapatılan araştırma açık kaldı: %v %+v", err, off)
	}
}

func TestExecute_PassesTheCardThroughAndNarratesTheSummary(t *testing.T) {
	fs := &fakeStudio{rep: catalog.Report{
		Requested: 3, Written: 2, Cached: 1, Notes: []string{"bir not"},
	}}
	e := New(config.Load(), fs, nil)
	sink := &recordingSink{}

	out := e.Execute(context.Background(), coderunner.Job{
		Row:          row(`{"import_id":"imp_1","product_ids":["a","b","c"]}`),
		Skill:        "kurallar",
		SkillVersion: "sk1",
		Out:          sink,
	})

	if out.Status != store.RunStatusCompleted {
		t.Fatalf("beklenen tamamlandı, alınan %q (%v)", out.Status, out.Err)
	}
	if fs.req.ImportID != "imp_1" || len(fs.req.ProductIDs) != 3 {
		t.Errorf("kart motora eksik geçti: %+v", fs.req)
	}
	if fs.req.Skill != "kurallar" || fs.req.SkillVersion != "sk1" {
		t.Error("skill gövdesi ya da sürümü geçmedi — mandat bir yerde kayboluyor")
	}
	// The note is streamed whether the pass ended well or badly: the pipeline
	// degrades rather than failing, and Notes is the only place that says a
	// partial answer is partial.
	joined := strings.Join(sink.said, "\n")
	if !strings.Contains(joined, "bir not") {
		t.Errorf("notlar akıtılmadı: %v", sink.said)
	}
	if !strings.Contains(joined, "2 yazıldı") {
		t.Errorf("özet yok: %v", sink.said)
	}
}

// The sentence an operator needs and cannot see: nothing already written was
// lost, and retrying costs only what is left. Without it a failed card looks
// like a run to start over, and starting a 400-product catalog over is exactly
// the bill this design exists to avoid.
func TestExecute_ARateLimitedCardSaysWhatSurvivedAndHowToResume(t *testing.T) {
	fs := &fakeStudio{
		rep: catalog.Report{Requested: 400, Written: 290, Cached: 9},
		err: llm.ErrRateLimited,
	}
	out := New(config.Load(), fs, nil).Execute(context.Background(), coderunner.Job{
		Row: row(`{"import_id":"imp_1","product_ids":["a"]}`),
		Out: &recordingSink{},
	})

	if out.Status != store.RunStatusFailed {
		t.Fatalf("beklenen başarısız, alınan %q", out.Status)
	}
	if !errors.Is(out.Err, llm.ErrRateLimited) {
		t.Errorf("sebep sarmalanmadı: %v", out.Err)
	}
	msg := out.Err.Error()
	for _, want := range []string{"299/400", "101", "kendiliğinden"} {
		if !strings.Contains(msg, want) {
			t.Errorf("kart mesajı %q içermiyor: %s", want, msg)
		}
	}
}

func TestExecute_ACancelledCardSaysTheSameThing(t *testing.T) {
	fs := &fakeStudio{
		rep: catalog.Report{Requested: 10, Written: 4},
		err: context.Canceled,
	}
	out := New(config.Load(), fs, nil).Execute(context.Background(), coderunner.Job{
		Row: row(`{"import_id":"imp_1","product_ids":["a"]}`),
		Out: &recordingSink{},
	})
	if !strings.Contains(out.Err.Error(), "4/10") {
		t.Errorf("yarıda kesilen koşu neyi koruduğunu söylemiyor: %v", out.Err)
	}
}

// Both strings become argv to a subprocess, so a value that is no longer
// offered must resolve to "route by class" rather than reach internal/llm.
type fakeValues struct {
	v   settings.Values
	err error
}

func (f fakeValues) Get() (settings.Values, error) { return f.v, f.err }

func TestFromSettings_DropsAModelTheDaemonNoLongerOffers(t *testing.T) {
	cfg := config.Load()
	if len(cfg.LLMProviders) == 0 {
		t.Skip("bu derlemede sağlayıcı listesi boş")
	}
	allowed := cfg.LLMProviders[0]

	good := FromSettings(cfg, fakeValues{v: settings.Values{
		Provider: allowed.ID, Model: allowed.DefaultModel,
	}}).Selection()
	if good.IsZero() {
		t.Errorf("izin listesindeki seçim düşürüldü: %+v", allowed)
	}

	bad := FromSettings(cfg, fakeValues{v: settings.Values{
		Provider: "uydurma", Model: "yok",
	}}).Selection()
	if !bad.IsZero() {
		t.Errorf("izin listesi dışındaki seçim geçti: %+v", bad)
	}

	if !FromSettings(cfg, fakeValues{err: errors.New("okunamadı")}).Selection().IsZero() {
		t.Error("okunamayan ayar bir seçim üretti")
	}
	if !FromSettings(cfg, nil).Selection().IsZero() {
		t.Error("nil ayar deposu bir seçim üretti")
	}
}

// A spent budget is a pause, not a failure. The card goes back to the queue with
// its reason on it and the window's own wake-up restarts it — and everything
// already written is already written, so what resumes pays only for what is
// left.
func TestExecute_ARateLimitAsksToBeParkedAtTheProvidersOwnTime(t *testing.T) {
	fs := &fakeStudio{
		rep: catalog.Report{Requested: 400, Written: 290},
		err: fmt.Errorf("%w: Claude AI usage limit reached|1788104400", llm.ErrRateLimited),
	}
	out := New(config.Load(), fs, nil).Execute(context.Background(), coderunner.Job{
		Row: row(`{"import_id":"imp_1","product_ids":["a"]}`),
		Out: &recordingSink{},
	})

	if out.ParkUntil.IsZero() {
		t.Fatal("limit çarptı ama kart park istemedi")
	}
	if out.ParkUntil.Unix() != 1788104400 {
		t.Errorf("sağlayıcının kendi zamanı kullanılmadı: %v", out.ParkUntil)
	}
	// The sentence stays: parking adds the automation, it does not remove the
	// explanation.
	if !strings.Contains(out.Err.Error(), "290/400") {
		t.Errorf("neyin korunduğu söylenmiyor: %v", out.Err)
	}
}

func TestExecute_ARateLimitWithNoTimeFallsBackToARecheckInterval(t *testing.T) {
	fs := &fakeStudio{
		rep: catalog.Report{Requested: 10, Written: 3},
		err: fmt.Errorf("%w: too many requests", llm.ErrRateLimited),
	}
	cfg := config.Load()
	e := New(cfg, fs, nil)
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	e.UseClock(func() time.Time { return now })

	out := e.Execute(context.Background(), coderunner.Job{
		Row: row(`{"import_id":"imp_1","product_ids":["a"]}`),
		Out: &recordingSink{},
	})
	if !out.ParkUntil.Equal(now.Add(cfg.CodingLimitRecheck)) {
		t.Errorf("geri düşüş aralığı kullanılmadı: %v", out.ParkUntil)
	}
}

// Everything else fails. A card the operator has to go and look at must not be
// disguised as one that resumes on its own.
func TestExecute_OnlyARateLimitAsksToBeParked(t *testing.T) {
	for name, err := range map[string]error{
		"sağlayıcı yok": llm.ErrProviderUnavailable,
		"iptal":         context.Canceled,
		"başka":         errors.New("hiçbir ürün yazılamadı"),
	} {
		out := New(config.Load(), &fakeStudio{
			rep: catalog.Report{Requested: 2, Written: 1}, err: err,
		}, nil).Execute(context.Background(), coderunner.Job{
			Row: row(`{"import_id":"imp_1","product_ids":["a"]}`),
			Out: &recordingSink{},
		})
		if !out.ParkUntil.IsZero() {
			t.Errorf("%s: park istendi", name)
		}
		if out.Status != store.RunStatusFailed {
			t.Errorf("%s: başarısız değil", name)
		}
	}
}

// --- the card's own model ----------------------------------------------------

// The bug this answers, from a real run: an operator changed the model, watched
// the same pass keep spending the old one, and had no control that said
// otherwise. A card that names a pair spends that pair, whatever the settings
// file says at the moment each call is made.
func TestExecute_TheCardsOwnModelBeatsTheSavedOne(t *testing.T) {
	cfg := config.Load()
	if len(cfg.LLMProviders) < 2 {
		t.Skip("bu derlemede tek sağlayıcı var")
	}
	saved, card := cfg.LLMProviders[0], cfg.LLMProviders[1]

	fs := &fakeStudio{rep: catalog.Report{Requested: 1, Written: 1}}
	sel := FromSettings(cfg, fakeValues{v: settings.Values{
		Provider: saved.ID, Model: saved.DefaultModel,
	}})
	params := fmt.Sprintf(
		`{"import_id":"imp_1","product_ids":["a"],"provider":%q,"model":%q}`,
		card.ID, card.DefaultModel)

	out := New(cfg, fs, sel).Execute(context.Background(), coderunner.Job{
		Row: row(params), Out: &recordingSink{},
	})

	if fs.req.Selection.Provider != card.ID || fs.req.Selection.Model != card.DefaultModel {
		t.Errorf("kartın kendi modeli harcanmadı: %+v", fs.req.Selection)
	}
	// And it is what the card reports having spent, so the board does not label
	// the run with a model it never touched.
	if out.Model != card.DefaultModel {
		t.Errorf("kart yanlış modeli rapor etti: %q", out.Model)
	}
}

// A card that names nothing is every card written before this field existed.
// It still follows the operator's saved choice, which is where that decision
// lived and still lives.
func TestExecute_ACardWithNoModelFallsBackToTheSavedOne(t *testing.T) {
	cfg := config.Load()
	if len(cfg.LLMProviders) == 0 {
		t.Skip("bu derlemede sağlayıcı listesi boş")
	}
	saved := cfg.LLMProviders[0]

	fs := &fakeStudio{rep: catalog.Report{Requested: 1, Written: 1}}
	sel := FromSettings(cfg, fakeValues{v: settings.Values{
		Provider: saved.ID, Model: saved.DefaultModel,
	}})

	New(cfg, fs, sel).Execute(context.Background(), coderunner.Job{
		Row: row(`{"import_id":"imp_1","product_ids":["a"]}`),
		Out: &recordingSink{},
	})

	if fs.req.Selection.Provider != saved.ID {
		t.Errorf("kayıtlı seçim uygulanmadı: %+v", fs.req.Selection)
	}
}

// Both halves of a selection become argv to a subprocess, and a card's params
// are a row that outlives the request that wrote it. The allow-list is asked
// again here, before the pass is claimed.
func TestPrepare_RefusesAModelPairTheDaemonWillNotRun(t *testing.T) {
	e := New(config.Load(), &fakeStudio{}, nil)
	err := e.Prepare(context.Background(), row(
		`{"import_id":"imp_1","product_ids":["a"],"provider":"uydurma","model":"yok"}`))
	if !errors.Is(err, ErrUnknownModel) {
		t.Errorf("izin listesi dışındaki çift kabul edildi: %v", err)
	}
}

// What the run's opening line names. The row's model column is the coding
// model on this lane — a model no catalog pass ever spends — so the executor
// answers for itself.
func TestModelFor_NamesWhatThePassWillActuallySpend(t *testing.T) {
	cfg := config.Load()
	if len(cfg.LLMProviders) == 0 {
		t.Skip("bu derlemede sağlayıcı listesi boş")
	}
	p := cfg.LLMProviders[0]

	e := New(cfg, &fakeStudio{}, nil)
	got := e.ModelFor(row(fmt.Sprintf(
		`{"import_id":"imp_1","product_ids":["a"],"provider":%q,"model":%q}`,
		p.ID, p.DefaultModel)))
	if got != p.DefaultModel {
		t.Errorf("başlangıç satırı yanlış modeli anıyor: %q", got)
	}
	if e.ModelFor(row("{")) != "" {
		t.Error("okunamayan kart bir model adı uydurdu")
	}
}
