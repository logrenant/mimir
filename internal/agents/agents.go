// Package agents is the catalogue of sub-agents a job can be given to, and the
// routing decision that picks one.
//
// A sub-agent is not a setting. Which executor runs a card, what class of model
// it may spend, and which skills it cannot work without are all behaviour, so
// they are constants here (SD-1) — the operator owns the *body* of a skill
// (internal/skills), never the wiring that makes it mandatory.
//
// The registry is deliberately thin. It says what exists and what each one
// needs; it does not know how to run anything. internal/coderunner owns the
// executors, and the two meet on the Executor string alone, so adding an agent
// here cannot start a subprocess by accident.
package agents

import (
	"github.com/logrenant/mimir/internal/llm"
	"github.com/logrenant/mimir/internal/skills"
)

// Executor names the kind of machinery that runs a job. It is a wire string —
// it lands in a run row and in the dispatcher's routing table — so it is never
// renamed, only added to.
type Executor string

const (
	// ExecClaude is a folder-scoped claude CLI session: a subprocess that
	// spends one credential slot for its whole life.
	ExecClaude Executor = "claude"
	// ExecLeadgen is the lead-gen pipeline: Go code and HTTP calls, with no
	// identity to be signed out of and no token budget to run out of.
	ExecLeadgen Executor = "leadgen"
	// ExecCatalog is the product content studio: research and rewrite over one
	// import's products, one product at a time, each one checked against two
	// caches before it spends anything.
	ExecCatalog Executor = "catalog"
)

// Agent is one sub-agent, as both the router and the dispatcher read it.
type Agent struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// Desc is what the router shows the model. It is the only thing standing
	// between a free-text card and the right executor, so it describes the
	// work rather than the machinery.
	Desc string   `json:"desc"`
	Exec Executor `json:"executor"`
	// RequiredSkills may never be empty. That is the mandate: a job with no
	// instructions is a job nobody can review afterwards.
	RequiredSkills []string `json:"required_skills"`
	// NeedsProject is whether a card must name a registered folder.
	NeedsProject bool `json:"needs_project"`
	// ModelClass is the class of work this agent's own calls are, when it
	// makes any. It is not the card's coding model, which stays a per-card
	// choice from config.CodingModels.
	ModelClass llm.Class `json:"-"`
}

// Default is the agent a card lands on when nothing else decides. It is coding
// because that is what every row written before this package existed was.
const Default = "coding"

// KeyCatalog names the product content agent. It is a constant because two
// packages outside this one resolve the agent by key to learn which skills a
// catalog rewrite runs under — the version of those skills is half a cache key,
// and a magic string is how the two halves drifted apart the first time.
const KeyCatalog = "catalog"

// registry is the closed set. Order is the order a UI should offer them.
var registry = []Agent{
	{
		Key:  "coding",
		Name: "Kod",
		Desc: "Bir depoda kod yazmak, değiştirmek, hata ayıklamak, test eklemek, " +
			"bir derleme ya da bağımlılık sorununu çözmek.",
		Exec:           ExecClaude,
		RequiredSkills: []string{skills.CodeReview},
		NeedsProject:   true,
		ModelClass:     llm.Reason,
	},
	{
		Key:  "review",
		Name: "İnceleme",
		Desc: "Yazılmış kodu gözden geçirmek: bir diff'i, bir dalı, bir dosyayı " +
			"doğruluk, sınır durumları ve deponun kendi kuralları açısından denetlemek.",
		Exec:           ExecClaude,
		RequiredSkills: []string{skills.CodeReview},
		NeedsProject:   true,
		ModelClass:     llm.Reason,
	},
	{
		Key:  "marketing",
		Name: "Pazarlama",
		Desc: "Konumlandırma, mesaj, kanal seçimi, kampanya planı, landing page " +
			"metni, ürün anlatısı — kod değil, satılan şeyin nasıl anlatıldığı.",
		Exec:           ExecClaude,
		RequiredSkills: []string{skills.Marketing},
		NeedsProject:   false,
		ModelClass:     llm.Reason,
	},
	{
		Key:  "leadgen",
		Name: "Lead-gen",
		Desc: "Bir bölgede ve bir kategoride işletme bulmak, kategorize etmek, " +
			"iletişim bilgisi çıkarmak ve onlara ulaşmak için taslak yazmak.",
		Exec:           ExecLeadgen,
		RequiredSkills: []string{skills.LeadOutreach},
		NeedsProject:   false,
		ModelClass:     llm.Distill,
	},
	{
		Key:  KeyCatalog,
		Name: "Katalog",
		Desc: "Bir e-ticaret CSV'sindeki ürün içeriklerini markanın sesini ve " +
			"tasarım sözlüğünü koruyarak SEO ve üretken arama için yeniden yazmak.",
		Exec:           ExecCatalog,
		RequiredSkills: CatalogSkills(""),
		NeedsProject:   false,
		// Reason, not Distill: writing copy in a brand's voice from competitor
		// research is synthesis across sources. The research summaries it reads
		// were distilled — that half is the pipeline's, and it is cheap.
		ModelClass: llm.Reason,
	},
	{
		Key:  "graph",
		Name: "Grafik",
		Desc: "Bilgi grafiğine soru sormak: bir sembolü kim çağırıyor, iki şey " +
			"nasıl bağlanıyor, bu depoda mimari merkezler neresi.",
		Exec:           ExecClaude,
		RequiredSkills: []string{skills.GraphQuery},
		NeedsProject:   true,
		ModelClass:     llm.Distill,
	},
}

// CatalogSkills is the skill set one catalog pass runs under: the method
// always, plus the target language's own file when there is one.
//
// It is a function of the language rather than a fixed list on the agent
// because the composed version of an agent's skills is half a draft's cache
// key. Declaring all three unconditionally would change the version string a
// source-language pass runs under, and every draft an operator has already
// approved would stop being found — the same failure task-101 fixed, arriving
// through a different door. It would also feed Arabic typography rules into
// every Turkish rewrite, which is a page of instructions the pass cannot use.
//
// The empty language is the file's own, and its answer is byte-identical to
// what this agent declared before languages existed.
func CatalogSkills(lang string) []string {
	switch lang {
	case "ar":
		return []string{skills.ProductContent, skills.ProductContentAR}
	case "en":
		return []string{skills.ProductContent, skills.ProductContentEN}
	}
	return []string{skills.ProductContent}
}

// Registry is every agent, in offer order. It returns a copy of the slice
// header only; Agent values are read-only by convention and by the fact that
// nothing here exposes a pointer.
func Registry() []Agent {
	out := make([]Agent, len(registry))
	copy(out, registry)
	return out
}

// Lookup resolves one agent by key.
func Lookup(key string) (Agent, bool) {
	for _, a := range registry {
		if a.Key == key {
			return a, true
		}
	}
	return Agent{}, false
}

// Keys is every agent key, for the allow-list the router hands the model.
func Keys() []string {
	out := make([]string, 0, len(registry))
	for _, a := range registry {
		out = append(out, a.Key)
	}
	return out
}
