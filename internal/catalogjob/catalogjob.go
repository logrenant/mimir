// Package catalogjob makes a catalog rewrite a card on the same board as
// everything else: queued by the one dispatcher, narrated through the one
// stream, reconciled after a restart by the one sweep.
//
// It is a package of its own so internal/catalog never imports
// internal/coderunner and the runner never imports the studio — task-81's
// arrangement for lead-gen, for the reason its own file gives: a new kind of
// work does not get a second scheduler, it gets an Executor.
//
// What is different here, and the reason this package is worth reading: a
// catalog pass spends real money per product, and it is the first executor
// where stopping halfway is the expected case rather than the failure case.
// Everything about it is arranged so that the second attempt is cheap.
package catalogjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/agents"
	"github.com/logrenant/mimir/internal/catalog"
	"github.com/logrenant/mimir/internal/coderunner"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
	"github.com/logrenant/mimir/internal/settings"
	"github.com/logrenant/mimir/internal/skills"
	"github.com/logrenant/mimir/internal/store"
)

// Agent is the wire string a row carries and the dispatcher routes on.
const Agent = "catalog"

// Params is what a card says about the work.
//
// ProductIDs is an explicit list and never a filter, which is the rule
// POST /maps/outreach established: a filter would let one short string spend a
// whole catalog's worth of tokens, and the operator would find out from the
// bill rather than from the board.
type Params struct {
	ImportID   string   `json:"import_id"`
	ProductIDs []string `json:"product_ids"`
	Fields     []string `json:"fields,omitempty"`
	// Research defaults to true when the key is absent: a rewrite with no
	// market behind it is the cheaper thing, and quietly giving somebody the
	// cheaper thing because they left a field out is how a tool ends up
	// producing worse copy than it advertises.
	Research *bool `json:"research,omitempty"`
	// Lang is the language this card writes. Absent is the file's own, which
	// is what every card written before languages existed asked for.
	//
	// One card is one language, deliberately. A card is the unit an operator
	// watches, parks and resumes, and folding two languages into one makes
	// "40/200 yazıldı" ambiguous and doubles what a rate-limit park loses.
	Lang string `json:"lang,omitempty"`

	// Provider and Model are the card's own model, and they are the answer to
	// "I changed the model and nothing changed".
	//
	// Before this, a pass spent whatever internal/settings held at the moment
	// each call was made. Two things followed, and both were reported from a
	// real run. A settings change made while a pass was in flight moved it
	// mid-pass: the pre-flight capability check cleared under the old value and
	// the first draft call landed on the new one, which is how a card gets past
	// the "this provider cannot return structured output" gate and then fails
	// on it per product. And the model shown on the card was the *coding*
	// model, which no catalog pass has ever spent, so the one control an
	// operator could find changed nothing they could see.
	//
	// A card carries the choice instead. Empty is still the settings value —
	// that is what every card written before this field asked for — but a card
	// that names a pair is that pair for its whole life, including when it is
	// re-run tomorrow under different settings.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// Selection is the card's own model choice, or the zero value when it names
// none. The pair travels together: a model without a provider is not a
// selection, it is half of one, and the route that writes these params refuses
// it before it can get here.
func (p Params) Selection() llm.Selection {
	provider := strings.TrimSpace(p.Provider)
	if provider == "" {
		return llm.Selection{}
	}
	return llm.Selection{Provider: provider, Model: strings.TrimSpace(p.Model)}
}

// lang resolves the card's target language, refusing one this binary cannot
// write rather than quietly writing the source language instead.
func (p Params) lang() (catalog.Lang, error) {
	l := catalog.Lang(p.Lang)
	if !catalog.KnownLang(l) {
		return "", fmt.Errorf("%w: %q", ErrUnknownLang, p.Lang)
	}
	return l, nil
}

// ErrNoProducts refuses a card that names nothing to work on.
var ErrNoProducts = errors.New("catalogjob: this card names no products")

// ErrUnknownModel refuses a card naming a provider/model pair this daemon will
// not run. Both names become argv to a subprocess, so the allow-list is the
// whole argument for letting a card choose at all.
var ErrUnknownModel = errors.New("catalogjob: unknown provider/model pair")

// ErrUnknownLang refuses a card naming a language this binary does not write.
var ErrUnknownLang = errors.New("catalogjob: unknown target language")

// ErrSkillUnavailable refuses a card whose language's standing instructions
// could not be loaded. It mirrors coderunner's own gate and exists separately
// because the runner composes an agent's declared skills, and which skills a
// catalog pass needs depends on the card's language rather than on the agent.
var ErrSkillUnavailable = errors.New("catalogjob: the target language's instructions could not be loaded")

// Parse reads a row's params.
//
// Unlike lead-gen's, there is no fallback to the prompt: a region is a sentence
// somebody can type, and a set of product ids is not. A card that arrived
// without them was not written by the screen and cannot be guessed at.
func Parse(row store.RunRow) (Params, error) {
	var p Params
	raw := strings.TrimSpace(row.Params)
	if raw == "" {
		return Params{}, fmt.Errorf("%w: no params on the card", ErrNoProducts)
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return Params{}, fmt.Errorf("catalogjob: unreadable params: %w", err)
	}
	if strings.TrimSpace(p.ImportID) == "" {
		return Params{}, errors.New("catalogjob: the card names no import")
	}
	if len(p.ProductIDs) == 0 {
		return Params{}, ErrNoProducts
	}
	return p, nil
}

// Research reports what the card asked for, defaulting to true.
func (p Params) DoResearch() bool { return p.Research == nil || *p.Research }

// Fields resolves the writable fields this card names.
func (p Params) fields() ([]catalog.Field, error) {
	out := make([]catalog.Field, 0, len(p.Fields))
	for _, name := range p.Fields {
		f := catalog.Field(name)
		if !f.Writable() {
			return nil, fmt.Errorf("%w: %s", catalog.ErrNotWritable, name)
		}
		out = append(out, f)
	}
	return out, nil
}

// Studio is the engine this executor drives.
type Studio interface {
	Rewrite(ctx context.Context, req catalog.RewriteRequest, out catalog.Sink) (catalog.Report, error)
}

// Selector is the operator's saved model choice. An interface so this package
// does not import internal/settings, whose file reads are not its business.
type Selector interface{ Selection() llm.Selection }

// Executor runs one catalog card.
type Executor struct {
	studio  Studio
	sel     Selector
	skills  skills.BodySource
	cfg     config.Config
	recheck time.Duration
	now     func() time.Time
}

// New wires the executor. sel may be nil, which means class routing.
func New(cfg config.Config, s Studio, sel Selector) *Executor {
	return &Executor{
		studio: s, sel: sel,
		cfg:     cfg,
		recheck: cfg.CodingLimitRecheck,
		now:     time.Now,
	}
}

// UseSkills gives the executor the source its language files come from.
//
// It is here rather than left to coderunner because the runner composes the
// skills an *agent* declares, and which ones a catalog pass actually needs
// depends on the card: an Arabic pass runs under the method plus the Arabic
// file, a Turkish one under the method alone. Declaring all three on the agent
// would change the version string a Turkish pass runs under — and that string
// is half a draft's cache key, so every draft an operator already approved
// would stop being found.
func (e *Executor) UseSkills(src skills.BodySource) { e.skills = src }

// standingInstructions resolves what this card's language is written under,
// falling back to what the runner already composed when nothing was wired.
func (e *Executor) standingInstructions(job coderunner.Job, lang catalog.Lang) (body, version string, err error) {
	if e.skills == nil {
		return job.Skill, job.SkillVersion, nil
	}
	body, version, ok := skills.Require(e.skills, agents.CatalogSkills(string(lang)))
	if !ok {
		return "", "", fmt.Errorf("%w: %s", ErrSkillUnavailable, lang.Label())
	}
	return body, version, nil
}

// UseClock is for tests only.
func (e *Executor) UseClock(fn func() time.Time) { e.now = fn }

// resetOf is when the pause should lift.
//
// The provider's own time whenever it gave one — its usage-limit sentence
// carries a `…|<unix>` suffix, and coderunner already knows how to read it, so
// there is one parser of that wording rather than two. When it gave none, the
// fallback is a recheck interval, and it is treated as the guess it is: the
// queue pauses for a while rather than claiming to know when the budget
// returns.
func (e *Executor) resetOf(err error, recheck time.Duration) time.Time {
	if at, ok := coderunner.ResetFromMessage(err.Error()); ok {
		return at
	}
	if recheck <= 0 {
		recheck = 15 * time.Minute
	}
	return e.now().UTC().Add(recheck)
}

func (e *Executor) Agent() string { return Agent }

// Lane is the worker lane. A catalog pass spends the daemon's own model budget
// through internal/llm, not a claude credential slot held for the life of a
// session, so gating it on a connected coding account would refuse work for a
// reason that does not apply to it.
func (e *Executor) Lane() coderunner.Lane { return coderunner.LaneWorker }

// Prepare refuses a card before anything is claimed.
//
// The refusal therefore lands on the card rather than in the middle of a pass
// that has already spent money — which is the whole reason this method is
// separate from Execute.
func (e *Executor) Prepare(_ context.Context, row store.RunRow) error {
	p, err := Parse(row)
	if err != nil {
		return err
	}
	if _, err := p.fields(); err != nil {
		return err
	}
	if _, err := p.lang(); err != nil {
		return err
	}
	return e.allows(p.Selection())
}

// allows is the allow-list, asked again here rather than trusted from the route
// that wrote the card.
//
// The route does check — and that check is the friendly one, which answers 400
// while the operator is looking at the screen. This is the load-bearing one: a
// card's params are a row in a database that outlives the request, and both
// halves of a selection end up as argv to a subprocess. Anything that can write
// a row — the edit route, a future importer, a hand-edited database — passes
// through here, and a pair this daemon will not run is refused before the pass
// is claimed instead of being handed to internal/llm.
func (e *Executor) allows(sel llm.Selection) error {
	if sel.IsZero() {
		return nil
	}
	if !e.cfg.HasLLMModel(sel.Provider, sel.Model) {
		return fmt.Errorf("%w: %s", ErrUnknownModel, sel.Key())
	}
	return nil
}

// selectionFor is which model this card spends: its own if it names one, and
// the operator's saved choice if it does not.
//
// The card wins, and the order is the whole point. A saved setting is this
// machine's answer to "who serves my work by default"; a card's pair is an
// instruction about *this* pass, given while looking at it, and a default that
// could override it would make the control a decoration.
func (e *Executor) selectionFor(p Params) llm.Selection {
	if sel := p.Selection(); !sel.IsZero() {
		return sel
	}
	if e.sel != nil {
		return e.sel.Selection()
	}
	return llm.Selection{}
}

// ModelFor is what the runner announces when the card starts.
//
// The runner's own answer is the row's model column, which for a worker-lane
// card is the daemon's model rather than a coding one — and for a card that
// names no pair at all it is empty, where the honest answer is whatever the
// settings resolve to right now. Both are read here, so the "run started ·"
// line names the model the pass is about to spend instead of one it will not.
func (e *Executor) ModelFor(row store.RunRow) string {
	p, err := Parse(row)
	if err != nil {
		return ""
	}
	return e.selectionFor(p).Model
}

// Execute runs the pass and narrates it.
func (e *Executor) Execute(ctx context.Context, job coderunner.Job) coderunner.Outcome {
	p, err := Parse(job.Row)
	if err != nil {
		return coderunner.Outcome{Status: store.RunStatusFailed, Err: err}
	}
	fields, err := p.fields()
	if err != nil {
		return coderunner.Outcome{Status: store.RunStatusFailed, Err: err}
	}

	sel := e.selectionFor(p)

	lang, err := p.lang()
	if err != nil {
		return coderunner.Outcome{Err: err}
	}
	skill, skillVersion, err := e.standingInstructions(job, lang)
	if err != nil {
		return coderunner.Outcome{Err: err}
	}

	rep, rerr := e.studio.Rewrite(ctx, catalog.RewriteRequest{
		ImportID:     p.ImportID,
		ProductIDs:   p.ProductIDs,
		Fields:       fields,
		Research:     p.DoResearch(),
		Selection:    sel,
		Lang:         lang,
		Skill:        skill,
		SkillVersion: skillVersion,
	}, job.Out)

	// The pipeline degrades rather than failing (SD-6), and Notes is the only
	// place that says a partial answer is partial. It is streamed whether the
	// pass ended well or badly.
	for _, n := range rep.Notes {
		job.Out.Say(n)
	}
	job.Out.Say(summarize(rep))

	if rerr != nil {
		out := coderunner.Outcome{
			Status: store.RunStatusFailed,
			Err:    resumable(rerr, rep),
			Model:  sel.Model,
		}
		// A spent budget is a pause, not a failure: the row goes back to the
		// queue with its reason on it and the window's own wake-up restarts it.
		// Everything already written is already written, so what resumes pays
		// only for what is left.
		if errors.Is(rerr, llm.ErrRateLimited) {
			out.ParkUntil = e.resetOf(rerr, e.recheck)
		}
		return out
	}
	return coderunner.Outcome{Status: store.RunStatusCompleted, Model: sel.Model}
}

func summarize(rep catalog.Report) string {
	return fmt.Sprintf("%d ürün: %d yazıldı, %d önbellekten, %d atlandı, %d başarısız.",
		rep.Requested, rep.Written, rep.Cached, rep.Skipped, rep.Failed)
}

// resumable is what the card says when a pass stopped early.
//
// It names the two things the operator has to know and cannot see: that nothing
// already written was lost, and that carrying on costs only what is left.
// Without that sentence a stopped card looks like a run to start over, and
// starting a 400-product catalog over is exactly the bill this design exists to
// avoid.
//
// The rate-limit case says "by itself" because since task-89 it is true: the
// row is parked back to the queue and the window's own wake-up restarts it. The
// other two need a hand, and say so.
func resumable(err error, rep catalog.Report) error {
	done := rep.Written + rep.Cached
	if errors.Is(err, llm.ErrRateLimited) {
		return fmt.Errorf("model limiti doldu — %d/%d ürün yazıldı ve kaydedildi. "+
			"Limit penceresi dönünce bu kart kendiliğinden sürüyor ve yalnız kalan "+
			"%d ürünü harcıyor. (%w)",
			done, rep.Requested, rep.Requested-done, err)
	}
	if errors.Is(err, llm.ErrProviderUnavailable) {
		// Not a partial result to resume from — the machine could not run the
		// model at all. The message is the CLI's own, which already names the
		// fix (SD-6), and repeating "retry costs only what is left" over it
		// would suggest the problem is the catalog's.
		return fmt.Errorf("%w — %d/%d ürün yazıldı", err, done, rep.Requested)
	}
	if errors.Is(err, llm.ErrNoStructuredOutput) {
		// Refused at the front door, so nothing was written and nothing was
		// spent — which is exactly what has to be said, because this used to
		// arrive six minutes in with one product marked failed and a sentence
		// about JSON. It is not a retry: re-running under the same model gives
		// the same answer, so the message names the setting instead.
		return fmt.Errorf("bu pas hiç başlamadı: seçili model yapılandırılmış çıktı "+
			"veremiyor, katalog yazımı ise onu zorunlu kılıyor. Ayarlar'dan yapılandırılmış "+
			"çıktı veren bir model seçip kartı yeniden çalıştırın. (%w)", err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("koşu yarıda kesildi — %d/%d ürün yazıldı ve kaydedildi. "+
			"Yeniden çalıştırmak yalnız kalan %d ürünü harcar. (%w)",
			done, rep.Requested, rep.Requested-done, err)
	}
	return fmt.Errorf("%w — %d/%d ürün yazıldı ve kaydedildi; yeniden çalıştırmak "+
		"yalnız kalanları harcar", err, done, rep.Requested)
}

// FromSettings adapts the operator's saved model choice into a Selector.
//
// The allow-list check is the point, not the plumbing: both strings become argv
// to a subprocess, so a value that is no longer offered must resolve to "route
// by class" rather than reach internal/llm. task-70 moved this decision out of
// the search bar and into one place; a card that names no pair of its own still
// reads it from that same place — see Params.Provider for why a card may now
// name one, and selectionFor for which wins.
func FromSettings(cfg config.Config, v ValuesReader) Selector {
	return &settingsSelector{cfg: cfg, v: v}
}

// ValuesReader is internal/settings.Store, narrowed to the one read this
// package makes.
type ValuesReader interface {
	Get() (settings.Values, error)
}

type settingsSelector struct {
	cfg config.Config
	v   ValuesReader
}

func (s *settingsSelector) Selection() llm.Selection {
	if s.v == nil {
		return llm.Selection{}
	}
	vals, err := s.v.Get()
	if err != nil || vals.IsZero() {
		return llm.Selection{}
	}
	if !s.cfg.HasLLMModel(vals.Provider, vals.Model) {
		return llm.Selection{}
	}
	return llm.Selection{Provider: vals.Provider, Model: vals.Model}
}
