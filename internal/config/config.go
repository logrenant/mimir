// Package config provides configuration management for mimir-mcp.
package config

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Config represents the single source of truth for operational values.
type Config struct {
	// Endpoints
	Crawl4AIBaseURL   string
	DuckDuckGoHTMLURL string
	DuckDuckGoLiteURL string

	// Refiner — the local `claude` CLI (Claude Code), invoked headless via
	// exec, not an HTTP dependency. No API key required (uses the operator's
	// existing Claude Code login).
	ClaudeCLIPath string
	ClaudeModel   string

	// Provider routing (ROADMAP §B.1, amended 2026-09-02). Work is routed by
	// class, not by taste: one-shot compression goes to the cheap tier, and
	// synthesis stays where the capability is worth paying for. Both tiers are
	// a local CLI riding an existing login — still no SDK and no API key — and
	// both models are pinned to an exact version (SD-5).
	//
	// DistillFallback is **empty on purpose** (task-51). It was availability
	// only — a spare provider for when `agy` is missing or out of quota — and
	// that is exactly what made it wrong: the operator asked for a machine-wide
	// scan through agy, and a silent hand-off to claude turns "the free tier
	// ran out" into a bill nobody chose. An empty name means no fallback at
	// all; `llm.NewRouter` reads it that way and the distil tier then fails
	// loudly, which is the honest answer. Putting "claude" back here is the one
	// line that restores the old behaviour.
	//
	// DistillModelChain is the other kind of fallback, and the distinction is
	// the whole reason it is a separate field. DistillFallback moves work to
	// another *provider*, which is what task-51 refused because it moves the
	// bill with it. This one stays on `agy` and only changes the *model*, and
	// agy meters its models in two independent free pools: the Gemini tiers
	// draw on one, the Claude and GPT-OSS tiers on a second. Exhausting the
	// first therefore says nothing about the second, so trying it is not a
	// hand-off to a paid tier — it is the same free login, spending a bucket
	// that was already full. Ordered cheapest first; an empty slice is no
	// chain, and the distil tier fails loudly exactly as before.
	AgyCLIPath        string
	AgyPrintTimeout   time.Duration
	DistillProvider   string
	DistillModel      string
	DistillFallback   string
	DistillModelChain []string
	ReasonProvider    string

	// LLMProviders is what an operator may route a run to by hand, and the
	// only thing they may route it to: the daemon rejects a provider or model
	// that is not in this table, because both names become argv to a
	// subprocess.
	//
	// The class routing above is still the default and still the right answer
	// for everything the daemon starts on its own. This exists for the one
	// case the routing cannot serve — a lead-gen run somebody is watching,
	// where which tier spends the minutes and the money is the operator's call
	// and not a property of the work.
	LLMProviders []LLMProviderChoice

	// Brain — the node core (task-41). Nodes live in the same store as the
	// project memory, keyed by (project_path, kind, source_key) so re-ingesting
	// a source updates one row instead of minting another.
	//
	// There is no vector index and no embedding model here on purpose
	// (ROADMAP §A.4 stays parked): semantic neighbours come from one relation
	// pass over the FTS candidates, and semantic recall from alias terms the
	// distil writes into the index.
	//
	// Invalidation: BrainPromptVersion. Bumping it makes every stored
	// assessment stale and the distiller re-derives it; the deterministic
	// columns and the edges survive.
	BrainRelateCandidates    int
	BrainRelateMinWeight     float64
	BrainTagJaccardMin       float64
	BrainNeighborCap         int
	BrainSearchLimit         int
	BrainAssessmentMaxTokens int
	BrainBodyMaxChars        int
	BrainSearchMaxTokens     int
	BrainPromptVersion       string

	// Autonomous capture (task-45). Brain records what happens rather than
	// waiting to be told, and none of these three paths costs a model call:
	// episodes are already distilled by M8, commits are read from git, and the
	// agy spool is JSON a hook wrote. The batches exist so a first pass over a
	// long backlog is many short ticks rather than one long one.
	BrainSpoolDir     string
	BrainPromoteBatch int
	BrainCommitBatch  int

	// The repository scan (task-47). The one deliberately expensive operation
	// in this package: a distil per file, paid once per repository. The batch
	// is small because the caller is a tool with a request timeout — a pass
	// that returns "twelve done, three hundred left" is resumable, where a
	// four-hour call is not.
	BrainScanBatch        int
	BrainScanConcurrency  int
	BrainScanMaxFileBytes int

	// BrainScanDepth bounds how far under a root cmd/mimir-scan looks for
	// projects. It is a guard, not a tuning knob: a mis-typed root — `/`, or a
	// home directory — would otherwise become an hours-long directory walk
	// before the first file is ever read.
	BrainScanDepth int

	// The daemon's resident scanner (task-51). The roots are a constant and not
	// a setting for the same reason the coding runner has no MaxConcurrentRuns:
	// "which of my folders is the assistant allowed to read" is a decision, and
	// a decision that can be changed from a text field is one nobody remembers
	// making. Both are derived from the home directory in Load.
	//
	// IdleInterval is how long the supervisor waits after a full cycle before
	// starting the next one. A cycle over unchanged files makes no model call —
	// it is a hash comparison per file — so the interval is short enough that a
	// file saved at lunch is known about by the afternoon.
	//
	// The backoff is what stands between "agy is signed out" and a thousand
	// failed subprocesses an hour. It doubles from Min to Max and resets on the
	// first pass that distils anything.
	// BrainVersionsPerNode is how much of a node's history the store keeps: one
	// row per distinct content hash it has carried, oldest pruned on insert. A
	// bound rather than none because a file edited every minute for a year
	// would otherwise become the largest table in the database.
	BrainVersionsPerNode int

	// BrainNodeVersionsInline is how much history rides node detail, so the
	// panel does not need a second call for the common case.
	BrainNodeVersionsInline int

	BrainScanRoots        []string
	BrainScanIdleInterval time.Duration
	BrainScanBackoffMin   time.Duration
	BrainScanBackoffMax   time.Duration

	// PDFs are the reason ~/Documents is worth scanning at all: on this machine
	// it is six technical manuals and nothing else. pdftotext is optional by
	// construction — absent, PDFs are skipped and every other file still gets
	// read — so it is a dependency the way the Maps sidecar is, not the way the
	// store is.
	//
	// The page ceiling is what keeps a 400-page manual from costing a minute of
	// wall clock for a summary that only ever reads the first 8 000 characters
	// anyway (BrainBodyMaxChars).
	// MinChars is how "this PDF is a scan, not a document" is decided, and it is
	// a measured number rather than a guess: on this machine
	// rigid-frame-erection-manual.pdf is 8 MB of page images that extracts to 47
	// bytes, every one of them a form feed — which -nopgbrk turns into nothing
	// at all.
	BrainScanPDFPath     string
	BrainScanPDFPages    int
	BrainScanPDFMinChars int
	BrainScanPDFMaxChars int
	BrainScanPDFTimeout  time.Duration
	BrainScanMaxPDFBytes int

	// The graph the desktop draws. The default is what fits a screen and a
	// force layout that has to settle in front of a person; the ceiling is what
	// the store will hand over at all, so a hand-written query string cannot
	// ask for the whole machine and get an answer measured in megabytes.
	BrainGraphDefaultNodes int
	BrainGraphMaxNodes     int

	// GitHubToken is the second operator-provisioned credential, and it earns
	// that category the same way PlacesAPIKey does: empty is a valid, normal
	// state (brain_ingest_github then reads public repositories only), and it
	// is read once in Load and nowhere else.
	GitHubToken string

	// Timeouts.
	//
	// These are network budgets, not preferences, and they are sized for a
	// link that loses packets: a retransmit on a lossy connection routinely
	// costs several seconds, and the original values were tight enough that a
	// single one of them turned a working fetch into a failed stage.
	//
	// Constants, not knobs (SD-1): the right number is a property of a lossy
	// link in general, not of one operator's afternoon, and an environment
	// variable that could disable a timeout is a worse failure than one that
	// is too short. Raising any of these is a one-line change here.
	//
	// LLMHealthTimeout is deliberately not one of them. A health probe is
	// asking "is this CLI installed and signed in", and the answer arrives
	// promptly or not at all; giving it the same patience as real work would
	// let one unusable provider hold /diagnostics open for minutes.
	SearchTimeout    time.Duration
	CrawlTimeout     time.Duration
	RefineTimeout    time.Duration
	ResearchTimeout  time.Duration
	LLMHealthTimeout time.Duration

	// Concurrency
	MaxConcurrentCrawls  int
	MaxConcurrentRefines int
	GlobalCrawlSlots     int
	GlobalRefineSlots    int

	// Caps
	SearchDefaultCount int
	SearchMaxCount     int
	TopNForResearch    int

	// Output ceilings (token estimates)
	FetchPageMaxTokens     int
	ResearchBriefMaxTokens int
	WebSearchMaxTokens     int
	SummaryMaxTokens       int

	// Politeness
	PerHostMinInterval time.Duration

	// Persistence — the local SQLite cache (Phase 2 / M1). Losing it costs
	// time, never correctness: the pipeline runs uncached if it cannot open.
	StorePath           string
	PageCacheTTL        time.Duration
	RefinePromptVersion string

	// Stage F — free scraper providers (ecommerce/tiktok/gmaps/instagram).
	// These tools skip refine entirely (MetadataOnly response, like
	// web_search) so there is no per-tool refine token budget here — only
	// output ceilings and free-text clamp lengths.
	EcommerceLookupMaxTokens   int
	TikTokProfileMaxTokens     int
	GMapsLookupMaxTokens       int
	InstagramProfileMaxTokens  int
	ProductDescriptionMaxChars int
	BioMaxChars                int
	BusinessAboutMaxChars      int
	GMapsPageTimeout           time.Duration
	GMapsWaitForSelector       string

	// Coding-task runner (Phase 2 / M2) — a deliberately different `claude`
	// invocation profile from the refiner above. The refiner is headless,
	// tool-less and `--restricted` because it handles untrusted scraped text;
	// this one has file tools enabled because it does work in the operator's
	// own repo, scoped to one directory by internal/project.
	CodingModel          string
	CodingPermissionMode string
	CodingRunTimeout     time.Duration
	TranscriptDir        string

	// CodingModels is what the operator may pick from, and the only thing
	// --model is ever handed. A constant list rather than a knob (SD-1): it
	// describes the model lineup, which changes when Anthropic ships one, not
	// when an operator has a preference. CodingModel is the entry used when a
	// task names none, and Validate requires it to be in here — a default
	// nobody can choose is a bug, not a policy.
	CodingModels []CodingModelChoice

	// How long a stopped run is given to write its own result line before it
	// is killed, and how much of a run's stderr is carried as live events.
	// Constants for the same reason the rest of this struct is (SD-1): they
	// describe the machine's behaviour, not an operator's preference.
	//
	// There is deliberately no "max concurrent runs" here. Capacity is one run
	// per registered credential slot, which is a fact about the accounts the
	// operator has, not a number to tune: two runs sharing one Claude Code
	// identity share its rate limit and its session state.
	CodingStopGrace          time.Duration
	CodingStderrMaxBytes     int
	CodingAttachmentMaxBytes int64
	AttachmentDir            string

	// ClaudeAccountsDir is the directory whose subdirectories are credential
	// slots. It is the same convention the operator's shell already uses, so a
	// slot registered there is a slot Mimir spends without being told twice.
	ClaudeAccountsDir string

	// Project memory (M8). Long-lived per-project context distilled from
	// session transcripts, so a new session is told what this repository
	// already learned instead of rediscovering it. Losing it costs tokens,
	// never correctness: the memory tools are simply not registered when the
	// store cannot open.
	ClaudeProjectsDir      string
	MemoryRecapMaxTokens   int
	MemoryRecapConcurrency int
	MemoryRecapMaxAttempts int
	MemoryIngestBatch      int
	MemoryLazyCatchup      int
	MemoryBriefMaxTokens   int
	MemoryRecallMaxTokens  int
	MemoryRecentEpisodes   int
	MemoryRecallLimit      int
	MemoryNoteLimit        int
	MemoryHotFileDays      int
	MemoryOpenEpisodeGrace time.Duration
	MemoryIngestInterval   time.Duration
	MemoryPromptVersion    string

	// Maps lead-gen (Phase 2 / M4). PlacesAPIKey is the one
	// operator-provisioned credential in the repo — its own category in
	// internal/config/AGENTS.md, and an exception to docs/SECURITY.md's "zero
	// API keys" stance that docs/ROADMAP.md §B.6 makes explicitly. Empty is a
	// valid state: the binary runs, and simply does not offer maps_search.
	//
	// The three numbers below are ordinary SD-1 constants; only the key comes
	// from outside, and it decides *whether* a provider is reachable, never
	// what the process does with a request.
	PlacesAPIKey           string
	MapsSearchDefaultCount int
	MapsSearchMaxCount     int
	MapsSearchMaxTokens    int

	// Maps scrape fallback (Phase 2 / M5) — the Playwright sidecar in
	// deploy/playwright-maps, used when Places coverage or cost is not worth
	// it. Same posture as the Crawl4AI row above: a loopback container address
	// with a test-only override, plus bounds that are ours, not the caller's.
	MapScrapeBaseURL      string
	MapScrapeTimeout      time.Duration
	MapScrapeWaitSelector string
	MapScrapeMaxResults   int
	MapScrapeMaxScrolls   int
	// MapScrapeStartTimeout bounds bringing the sidecar up, image build
	// included. MapScrapeComposeFile is where that is started from; it is
	// derived, not chosen — see defaultMapScrapeComposeFile.
	MapScrapeStartTimeout time.Duration
	MapScrapeComposeFile  string
	// MapScrapeModelMaxChars bounds what the model fallback is fed when the
	// selectors read nothing. A rendered feed is megabytes of script and
	// base64 imagery; this is the slice of it that is worth paying to read.
	MapScrapeModelMaxChars  int
	MapScrapeModelMaxItems  int
	MapScrapeModelMaxTokens int

	// Contact enrichment: one page per company, read for a phone number and an
	// email address. The char bound is what a front page needs for its footer
	// to be in scope; the token bound is three short fields and nothing else.
	ContactModelMaxChars  int
	ContactModelMaxTokens int

	// ExportDir is where lead-gen workbooks are written. Derived beside the
	// store, like TranscriptDir: an operator looking for one file should find
	// every file this app writes in the same place.
	ExportDir string

	// Lead-gen categorization (Phase 2 / M5). The normalized category
	// vocabulary and the Google-type rule table are code, in internal/leadgen —
	// only these three numbers live here.
	//
	// LeadgenCategoryVersion is the cache key half that makes invalidation a
	// deliberate act: it covers BOTH the rule table and the classify prompt, so
	// editing either one without bumping this serves answers derived from a
	// taxonomy that no longer exists.
	LeadgenCategoryVersion   string
	LeadgenBatchSize         int
	LeadgenClassifyMaxTokens int

	// Lead-gen gap analysis (Phase 2 / M6). Stage 3 of internal/leadgen:
	// synthesize the common gaps/needs across one category of companies from
	// pre-computed facts (never raw pages). Prose output, so the SD-7 ceiling
	// is LeadgenGapMaxTokens enforced by internal/refine.clampOutput.
	//
	// LeadgenGapVersion is its own cache-key half, independent of
	// LeadgenCategoryVersion: it invalidates only the gap-analysis prompt, not
	// the categorization taxonomy. LeadgenGapMinCompanies is the floor below
	// which a category has too few signals to synthesize a pattern worth
	// paying for.
	LeadgenGapVersion      string
	LeadgenGapMaxTokens    int
	LeadgenGapMinCompanies int

	// Lead-gen outreach email drafting (Phase 2 / M6). Stage 4 of
	// internal/leadgen: one refine call per company, fed that company's facts
	// plus its category's stage-3 gap analysis. Prose output, so the SD-7
	// ceiling is LeadgenEmailMaxTokens via internal/refine.clampOutput.
	//
	// LeadgenEmailVersion is its own cache-key half. Cached in outreach_emails
	// with a status (draft/sent/skipped); a "sent" row is never regenerated by
	// a region re-run.
	LeadgenEmailVersion   string
	LeadgenEmailMaxTokens int

	// Lead-gen pipeline (Phase 2 / M6, task-34). The orchestrator that threads
	// region search → categorize → gap analysis → email into one call.
	//
	// LeadgenRegionTTL is how long a cached region search stays fresh — long,
	// because a miss is a billed Places request and a business list changes on
	// a scale of weeks, not hours. It is the ttl passed to
	// store.GetRegionSearch / GetCompany.
	LeadgenRegionTTL time.Duration

	// The lead ledger (task-63). Page bounds for the ledger reads, not a
	// behaviour knob: the ledger grows without limit by design, and a client
	// that asks for all of it would be asking the daemon to hold a table it
	// cannot render anyway.
	LeadsPageDefault int
	LeadsPageMax     int
	LeadRunsMax      int

	// The chat archive (task-65). Page bounds only: the archive grows without
	// limit by design, and these say how much of it one request may carry.
	ChatSessionsMax   int
	ChatTurnsPage     int
	ChatTurnsMax      int
	ChatSearchDefault int
	ChatSearchMax     int

	// Daemon plumbing (Phase 2 / M2) — process plumbing, parent-provided.
	//
	// These are not behaviour knobs (SD-1). They answer "where do I listen and
	// what secret do I accept", which only the parent process that spawned this
	// one can know: the Tauri shell reserves the port and mints the token, then
	// hands both down. Nothing here changes what the daemon does with a
	// request. See internal/config/AGENTS.md for the category.
	DaemonHost              string
	DaemonPort              int
	DaemonAuthToken         string
	DaemonReadHeaderTimeout time.Duration
	DaemonShutdownTimeout   time.Duration
	DaemonMaxRequestBytes   int64
}

// defaultStorePath derives the cache database location. It is computed, not
// configurable (SD-1); MIMIR_STORE_PATH overrides it for tests only.
func defaultStorePath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return filepath.Join(os.TempDir(), "mimir", "mimir.db")
	}
	return filepath.Join(dir, "mimir", "mimir.db")
}

// defaultClaudeProjectsDir is where Claude Code keeps its session transcripts.
// Computed like defaultStorePath, not configurable (SD-1);
// MIMIR_CLAUDE_PROJECTS_DIR points it at a fixture tree for tests only.
//
// An empty result is a defined state: the memory then has one fewer source and
// still works from this daemon's own coding-run transcripts.
func defaultClaudeProjectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// defaultClaudeAccountsDir is where a Claude Code credential slot lives: one
// directory per identity, hashed by the CLI into a keychain entry name. It
// mirrors the operator's shell convention (`CLAUDE_ACCOUNTS_DIR`, default
// `~/.claude-accounts`) so both spend the same slots.
//
// The shell variable itself is deliberately not read. The daemon runs under
// launchd, which hands it PATH and HOME and nothing else, so reading it would
// make discovery depend on who happened to start the daemon. Computed like
// defaultClaudeProjectsDir; MIMIR_CLAUDE_ACCOUNTS_DIR points it at a temp tree
// for tests only.
//
// An empty result is a defined state: discovery then finds the CLI's own slot
// and nothing else, which is exactly what a machine with one account has.
func defaultClaudeAccountsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude-accounts")
}

// defaultMapScrapeComposeFile is where the daemon starts the Maps sidecar from.
//
// Two candidates, in order: the copy `scripts/install-agent.sh` places beside
// the installed binary, and the one in a repository checkout the process
// happens to be running from. The installed daemon has no idea where the
// repository is — launchd starts it from the home directory — so shipping the
// deploy assets next to it is what makes a key-free region search work on a
// machine that never opens the repo.
//
// An empty result is a defined state: the sidecar then has to be started with
// `make maps-up`, and mapscrape.EnsureRunning says exactly that.
func defaultMapScrapeComposeFile(storePath string) string {
	candidates := []string{
		filepath.Join(filepath.Dir(storePath), "deploy", "playwright-maps", "docker-compose.yml"),
		filepath.Join("deploy", "playwright-maps", "docker-compose.yml"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			abs, err := filepath.Abs(c)
			if err != nil {
				return c
			}
			return abs
		}
	}
	return ""
}

// CodingModelChoice is one entry in the coding-task model picker: the string
// `claude --model` receives, and the name a human reads. Two fields because
// the CLI's identifiers are not written for a dropdown.
type CodingModelChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// LLMProviderChoice is one provider the operator may route a run to, with the
// models that provider will actually accept.
//
// Nested rather than two flat lists because the pairing is the constraint that
// matters: `gemini-3.8-flash-high` is meaningless to the `claude` CLI and
// `claude-opus-5` is meaningless to `agy`, and a picker built from two
// independent lists is a picker that can produce a combination the daemon has
// to reject.
type LLMProviderChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// DefaultModel is what this provider runs when the operator picks the
	// provider and leaves the model alone. It is always one of Models.
	DefaultModel string              `json:"default_model"`
	Models       []CodingModelChoice `json:"models"`
}

// HasLLMModel reports whether (provider, model) is a pair the daemon will run.
//
// The allow-list is the whole security argument for letting a client choose:
// both names end up as argv to a subprocess, so nothing that did not come from
// this table is ever passed on. An empty provider is "route by class" and an
// empty model is "that provider's default" — both are valid, and both are
// decided by the caller before asking.
func (c Config) HasLLMModel(provider, model string) bool {
	for _, p := range c.LLMProviders {
		if p.ID != provider {
			continue
		}
		if model == "" {
			return true
		}
		for _, m := range p.Models {
			if m.ID == model {
				return true
			}
		}
		return false
	}
	return false
}

// LLMDefaultModel is the model a provider runs when none was named.
func (c Config) LLMDefaultModel(provider string) string {
	for _, p := range c.LLMProviders {
		if p.ID == provider {
			return p.DefaultModel
		}
	}
	return ""
}

// HasCodingModel reports whether id is offerable. The empty string is not a
// model, it is "use the default", and callers decide that before asking.
func (c Config) HasCodingModel(id string) bool {
	for _, m := range c.CodingModels {
		if m.ID == id {
			return true
		}
	}
	return false
}

// Load returns the fully-populated default Config.
// It parses specific environment variables for test overrides but ignores invalid values.
func Load() Config {
	c := Config{
		Crawl4AIBaseURL:   "http://127.0.0.1:11235",
		DuckDuckGoHTMLURL: "https://html.duckduckgo.com/html/",
		DuckDuckGoLiteURL: "https://lite.duckduckgo.com/lite/",
		ClaudeCLIPath:     "claude",
		ClaudeModel:       "claude-haiku-4-5-20251001",
		AgyCLIPath:        "agy",
		AgyPrintTimeout:   240 * time.Second,
		DistillProvider:   "agy",
		// -low, reversing the -high this held until 2026-09-03. The old note
		// argued the tier is "free either way", and that turned out to be the
		// wrong unit: free is not unmetered. agy's Gemini pool is a weekly
		// budget, the resident Brain scan distils continuously against it, and
		// the effort suffix is what decides how many thinking tokens each node
		// costs. The operator asked for the token-safest configuration, so the
		// budget is now the thing being optimised and -low is the answer.
		//
		// Still pinned to an exact tag (SD-5): 3.8 is the current generation.
		DistillModel: "gemini-3.8-flash-low",
		// Provider-level fallback stays off — see the field's comment.
		DistillFallback: "",
		// The second free pool, cheapest first. GPT-OSS leads because it is the
		// only one of the three that does not think before answering, so it is
		// the cheapest way to keep the tier alive once Gemini's weekly budget
		// is spent. Opus is deliberately absent: it is the most expensive model
		// agy offers, and a chain that reaches for it to compress one node
		// would spend the reserve this chain exists to protect.
		DistillModelChain: []string{"gpt-oss-120b-medium", "claude-sonnet-4-6"},
		ReasonProvider:    "claude",

		// Pinned, exactly like CodingModels and for the same reason: an alias
		// means "whatever is latest when this runs", and an operator who chose
		// a model is entitled to get that model. The price is that this list
		// is edited when a generation ships.
		//
		// `agy models` lists older generations too; they are left out because
		// a picker is a recommendation, and nothing here is served by offering
		// a superseded model. The effort suffix is part of the identifier, so
		// the three Flash tiers are three entries rather than a second control.
		LLMProviders: []LLMProviderChoice{
			{
				ID:           "agy",
				Label:        "Antigravity (agy) — ücretsiz",
				DefaultModel: "gemini-3.8-flash-low",
				Models: []CodingModelChoice{
					{ID: "gemini-3.8-flash-high", Label: "Gemini 3.8 Flash (High)"},
					{ID: "gemini-3.8-flash-medium", Label: "Gemini 3.8 Flash (Medium)"},
					{ID: "gemini-3.8-flash-low", Label: "Gemini 3.8 Flash (Low)"},
					{ID: "gemini-3.1-pro-high", Label: "Gemini 3.1 Pro (High)"},
					{ID: "gemini-3.1-pro-low", Label: "Gemini 3.1 Pro (Low)"},
					{ID: "claude-sonnet-4-6", Label: "Claude Sonnet 4.6 (agy)"},
					{ID: "claude-opus-4-6-thinking", Label: "Claude Opus 4.6 (agy)"},
					{ID: "gpt-oss-120b-medium", Label: "GPT-OSS 120B (agy)"},
				},
			},
			{
				ID:           "claude",
				Label:        "Claude Code — kotanızdan harcar",
				DefaultModel: "claude-haiku-4-5-20251001",
				Models: []CodingModelChoice{
					{ID: "claude-opus-5", Label: "Opus 5"},
					{ID: "claude-sonnet-5", Label: "Sonnet 5"},
					{ID: "claude-haiku-4-5-20251001", Label: "Haiku 4.5"},
				},
			},
		},
		SearchTimeout:          30 * time.Second,
		CrawlTimeout:           150 * time.Second,
		RefineTimeout:          180 * time.Second,
		ResearchTimeout:        360 * time.Second,
		LLMHealthTimeout:       20 * time.Second,
		MaxConcurrentCrawls:    4,
		MaxConcurrentRefines:   2,
		GlobalCrawlSlots:       6,
		GlobalRefineSlots:      3,
		SearchDefaultCount:     8,
		SearchMaxCount:         30,
		TopNForResearch:        5,
		FetchPageMaxTokens:     1500,
		ResearchBriefMaxTokens: 2000,
		WebSearchMaxTokens:     1200,
		SummaryMaxTokens:       200,
		PerHostMinInterval:     1500 * time.Millisecond,
		StorePath:              defaultStorePath(),
		PageCacheTTL:           24 * time.Hour,
		// v2: the refine prompt gained a separate no-query branch, so every
		// row refined under v1 replays text written to a different contract.
		RefinePromptVersion: "v2",

		// Project memory (M8). The recap ceiling is deliberately tiny: an
		// episode summary that needs more than a title and three bullets is
		// recording narrative rather than a decision, and narrative is what
		// makes a memory grow until it costs more than it saves.
		ClaudeProjectsDir:      defaultClaudeProjectsDir(),
		MemoryRecapMaxTokens:   120,
		MemoryRecapConcurrency: 2,
		MemoryRecapMaxAttempts: 2,
		MemoryIngestBatch:      40,
		MemoryLazyCatchup:      6,
		MemoryBriefMaxTokens:   1400,
		MemoryRecallMaxTokens:  1100,
		MemoryRecentEpisodes:   12,
		MemoryRecallLimit:      8,
		MemoryNoteLimit:        12,
		MemoryHotFileDays:      30,
		MemoryOpenEpisodeGrace: 10 * time.Minute,
		MemoryIngestInterval:   5 * time.Minute,
		MemoryPromptVersion:    "v1",

		BrainRelateCandidates:    20,
		BrainRelateMinWeight:     0.5,
		BrainTagJaccardMin:       0.34,
		BrainNeighborCap:         8,
		BrainSearchLimit:         8,
		BrainAssessmentMaxTokens: 160,
		BrainBodyMaxChars:        8000,
		BrainSearchMaxTokens:     1400,
		BrainPromptVersion:       "brain-v2",
		BrainPromoteBatch:        200,
		BrainCommitBatch:         100,
		BrainVersionsPerNode:     50,
		BrainNodeVersionsInline:  10,
		BrainScanBatch:           12,
		BrainScanConcurrency:     3,
		BrainScanMaxFileBytes:    96 << 10,
		BrainScanDepth:           6,
		BrainScanIdleInterval:    15 * time.Minute,
		BrainScanBackoffMin:      time.Minute,
		BrainScanBackoffMax:      30 * time.Minute,
		BrainScanPDFPath:         "pdftotext",
		BrainScanPDFPages:        120,
		BrainScanPDFMinChars:     40,
		BrainScanPDFMaxChars:     256 << 10,
		BrainScanPDFTimeout:      30 * time.Second,
		BrainScanMaxPDFBytes:     32 << 20,
		BrainGraphDefaultNodes:   1500,
		BrainGraphMaxNodes:       3000,

		EcommerceLookupMaxTokens:   400,
		TikTokProfileMaxTokens:     400,
		GMapsLookupMaxTokens:       400,
		InstagramProfileMaxTokens:  300,
		ProductDescriptionMaxChars: 600,
		BioMaxChars:                300,
		BusinessAboutMaxChars:      400,
		// 90s, not the 30s that was verified empirically against a live
		// container: that measurement was taken on a healthy link, and it was
		// already the tightest of the three (20-25s did not leave room for the
		// h1 wait). A lossy connection spends its budget on retransmits before
		// the page has begun rendering, so the headroom is the fix.
		GMapsPageTimeout:     90 * time.Second,
		GMapsWaitForSelector: "h1",

		CodingModel: "claude-sonnet-5",
		// Full names, not the CLI's `opus`/`sonnet` aliases. An alias means
		// "whatever is latest when this runs", which is the wrong contract for
		// a card that can sit in the backlog for a week: the operator picked a
		// model and the run should be that model. The price is that this list
		// is edited when a generation ships — the same price CodingModel and
		// ResearchModel above already pay.
		CodingModels: []CodingModelChoice{
			{ID: "claude-opus-5", Label: "Opus 5"},
			{ID: "claude-sonnet-5", Label: "Sonnet 5"},
			{ID: "claude-haiku-4-5-20251001", Label: "Haiku 4.5"},
		},
		// In headless -p mode "default" does not prompt, it DENIES — a run
		// would finish having changed nothing. "acceptEdits" accepts file
		// edits while still withholding Bash; "bypassPermissions" would hand
		// over arbitrary shell execution and is deliberately not used. The
		// real boundary is the directory (internal/project), not this mode.
		CodingPermissionMode: "acceptEdits",
		CodingRunTimeout:     30 * time.Minute,

		// A stopped run gets SIGINT first and this long to report its own
		// outcome; the CLI writes a `result` line on interrupt, and that line
		// carries the cost and turn count a SIGKILL would throw away.
		CodingStopGrace: 5 * time.Second,
		// stderr is diagnostic, not output. Enough to carry a stack trace or a
		// "run `claude login`", capped so a looping child cannot fill the bus.
		CodingStderrMaxBytes: 256 << 10,
		// An attachment is a screenshot, not a dataset. 10 MiB before base64
		// framing is generous for a retina screenshot and still small enough
		// that the daemon holds one in memory without thinking about it.
		CodingAttachmentMaxBytes: 10 << 20,

		// One directory per Claude Code identity, the same tree the operator's
		// shell already switches between.
		ClaudeAccountsDir: defaultClaudeAccountsDir(),

		// 60 is the Places API's own ceiling for places:searchText, not a
		// preference. The token ceiling is the same order as a research brief:
		// a lead list is read by an agent, and 60 companies of structured
		// fields do not fit in it — maps_search trims to fit and says so.
		MapsSearchDefaultCount: 20,
		MapsSearchMaxCount:     60,
		MapsSearchMaxTokens:    2000,

		// 20 companies per classify call amortizes the subprocess over a batch
		// without letting one failure cost a whole region: a lost batch is 20
		// rows re-asked, not 200. The token ceiling is per batch and small
		// because the answer is one word per company — the model chooses from a
		// closed list, it does not write prose.
		// A scrape is a browser render plus a dozen scrolls that each wait for
		// results to load, so its budget is minutes-adjacent, not the seconds a
		// Crawl4AI fetch takes. The scroll ceiling is what stops a query with
		// thousands of matches from scrolling until the timeout.
		MapScrapeBaseURL:      "http://127.0.0.1:11236",
		MapScrapeTimeout:      300 * time.Second,
		MapScrapeWaitSelector: `div[role="feed"]`,
		MapScrapeMaxResults:   60,
		MapScrapeMaxScrolls:   12,
		// Generous because the first start on a fresh machine builds the image
		// — a Playwright base plus npm install — and a build that is killed
		// half way leaves the operator with neither a container nor an answer.
		MapScrapeStartTimeout: 10 * time.Minute,
		// ~60k characters is roughly 15–20k input tokens after trimming — the
		// point where a feed's business names are all present but the page's
		// tail of markup is not being paid for. The item and token ceilings
		// match the feed's own cap: this recovers a page, it does not enlarge
		// one.
		MapScrapeModelMaxChars:  60000,
		MapScrapeModelMaxItems:  60,
		MapScrapeModelMaxTokens: 2000,

		ContactModelMaxChars:  12000,
		ContactModelMaxTokens: 200,

		LeadgenCategoryVersion:   "leadgen-v1",
		LeadgenBatchSize:         20,
		LeadgenClassifyMaxTokens: 800,

		// One refine call per category, fed a handful of booleans/counts per
		// company — a small structured input, so the token ceiling is smaller
		// than a research brief. Three companies is the floor: below it,
		// "the group has a common gap" is a claim about noise.
		LeadgenGapVersion:      "gap-v1",
		LeadgenGapMaxTokens:    700,
		LeadgenGapMinCompanies: 3,

		// A cold outreach email is short by nature; 600 tokens is generous and
		// the clamp trims anything past it. One draft per company, cached, and
		// never regenerated once a human marks it sent.
		LeadgenEmailVersion:   "email-v1",
		LeadgenEmailMaxTokens: 600,

		LeadgenRegionTTL: 30 * 24 * time.Hour,

		LeadsPageDefault: 200,
		LeadsPageMax:     1000,
		LeadRunsMax:      100,

		ChatSessionsMax:   200,
		ChatTurnsPage:     100,
		ChatTurnsMax:      500,
		ChatSearchDefault: 25,
		ChatSearchMax:     100,

		// Loopback is a security property of this daemon, not a preference:
		// there is deliberately no override for the host.
		DaemonHost:              "127.0.0.1",
		DaemonPort:              0,
		DaemonReadHeaderTimeout: 5 * time.Second,
		DaemonShutdownTimeout:   10 * time.Second,
		DaemonMaxRequestBytes:   1 << 20,
	}

	if val := os.Getenv("MIMIR_CRAWL4AI_URL"); val != "" {
		if _, err := url.ParseRequestURI(val); err == nil {
			c.Crawl4AIBaseURL = val
		}
	}
	if val := os.Getenv("MIMIR_AGY_CLI_PATH"); val != "" {
		c.AgyCLIPath = val
	}
	if val := os.Getenv("MIMIR_CLAUDE_CLI_PATH"); val != "" {
		c.ClaudeCLIPath = val
	}
	// The same category as the two CLI paths above: a test points it at a fake
	// so `make check` does not require poppler to be installed.
	if val := os.Getenv("MIMIR_PDFTOTEXT_PATH"); val != "" {
		c.BrainScanPDFPath = val
	}
	if val := os.Getenv("MIMIR_DDG_HTML_URL"); val != "" {
		if _, err := url.ParseRequestURI(val); err == nil {
			c.DuckDuckGoHTMLURL = val
		}
	}
	if val := os.Getenv("MIMIR_DDG_LITE_URL"); val != "" {
		if _, err := url.ParseRequestURI(val); err == nil {
			c.DuckDuckGoLiteURL = val
		}
	}
	if val := os.Getenv("MIMIR_MAPSCRAPE_URL"); val != "" {
		if _, err := url.ParseRequestURI(val); err == nil {
			c.MapScrapeBaseURL = val
		}
	}
	if val := os.Getenv("MIMIR_STORE_PATH"); val != "" {
		c.StorePath = val
	}
	if val := os.Getenv("MIMIR_CLAUDE_PROJECTS_DIR"); val != "" {
		c.ClaudeProjectsDir = val
	}
	if val := os.Getenv("MIMIR_CLAUDE_ACCOUNTS_DIR"); val != "" {
		c.ClaudeAccountsDir = val
	}

	// Process plumbing, parent-provided. An unparseable port keeps the default
	// (0 = kernel-assigned) rather than failing, like every other override
	// here. The token has no default on purpose: absent means the daemon
	// refuses to start, which is checked by ValidateDaemon, not here.
	if val := os.Getenv("MIMIR_DAEMON_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil && port >= 0 && port <= 65535 {
			c.DaemonPort = port
		}
	}
	c.DaemonAuthToken = os.Getenv("MIMIR_DAEMON_TOKEN")

	// Operator-provisioned credential, read once, here and nowhere else. Empty
	// is a defined state, not an error: RegisterAll then omits maps_search
	// rather than advertising a tool that cannot work.
	c.PlacesAPIKey = os.Getenv("MIMIR_GOOGLE_PLACES_API_KEY")

	// The second one, same category and same rules. It is MIMIR_-prefixed
	// rather than the bare GITHUB_TOKEN the wider ecosystem uses so that a
	// token exported for some unrelated tool is never spent here by accident.
	c.GitHubToken = os.Getenv("MIMIR_GITHUB_TOKEN")

	// Derived after the override above so a test pointing StorePath at a temp
	// directory gets an isolated transcript directory for free.
	c.TranscriptDir = filepath.Join(filepath.Dir(c.StorePath), "transcripts")
	c.ExportDir = filepath.Join(filepath.Dir(c.StorePath), "exports")
	c.MapScrapeComposeFile = defaultMapScrapeComposeFile(c.StorePath)
	if val := os.Getenv("MIMIR_MAPSCRAPE_COMPOSE"); val != "" {
		c.MapScrapeComposeFile = val
	}
	c.AttachmentDir = filepath.Join(filepath.Dir(c.StorePath), "attachments")
	// Where the agy Stop hook drops a conversation for the daemon to pick up.
	// A directory rather than an HTTP call: the hook then needs no token,
	// cannot block a session on the network, and a conversation that ended
	// while the daemon was down is still recorded when it comes back.
	c.BrainSpoolDir = filepath.Join(filepath.Dir(c.StorePath), "spool")

	// The scanner's roots. A home directory that cannot be determined leaves
	// the list empty, and an empty list is a supervisor that does nothing —
	// which is the right failure: there is no default project, ever, and
	// guessing at a path to read files from is not a recovery.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		c.BrainScanRoots = []string{
			filepath.Join(home, "development"),
			filepath.Join(home, "Documents"),
		}
	}

	return c
}

// Validate sanity-checks that URLs parse and numeric fields are > 0.
func (c Config) Validate() error {
	urlsToValidate := []string{
		c.Crawl4AIBaseURL,
		c.DuckDuckGoHTMLURL,
		c.DuckDuckGoLiteURL,
	}
	for _, u := range urlsToValidate {
		if _, err := url.ParseRequestURI(u); err != nil {
			return errors.New("invalid URL in config: " + u)
		}
	}

	if c.ClaudeModel == "" {
		return errors.New("ClaudeModel is empty")
	}
	if c.ClaudeCLIPath == "" {
		return errors.New("ClaudeCLIPath is empty")
	}
	if c.AgyCLIPath == "" {
		return errors.New("AgyCLIPath is empty")
	}
	if c.DistillModel == "" {
		return errors.New("DistillModel is empty")
	}
	if c.AgyPrintTimeout <= 0 {
		return errors.New("AgyPrintTimeout must be > 0")
	}
	// A provider name that resolves to nothing would silently fall back to
	// claude for every class, which is a working system that quietly stopped
	// doing what the roadmap says it does.
	for name, field := range map[string]string{
		"DistillProvider": c.DistillProvider,
		"ReasonProvider":  c.ReasonProvider,
	} {
		if field != "claude" && field != "agy" {
			return errors.New(name + " must be \"claude\" or \"agy\", got: " + field)
		}
	}
	// The fallback is the one that may be empty, and empty is its default:
	// "no second provider" is a decision, not an unset field.
	if c.DistillFallback != "" && c.DistillFallback != "claude" && c.DistillFallback != "agy" {
		return errors.New("DistillFallback must be \"\", \"claude\" or \"agy\", got: " + c.DistillFallback)
	}

	// Every chain entry becomes argv to a subprocess, so it is checked against
	// the same table an operator's hand-picked model is checked against — a
	// typo here would otherwise reach the CLI as a model name and fail only
	// once the primary pool was already spent.
	for _, model := range c.DistillModelChain {
		if model == "" {
			return errors.New("DistillModelChain must not contain an empty model")
		}
		if !c.HasLLMModel(c.DistillProvider, model) {
			return errors.New("DistillModelChain has a model " + c.DistillProvider +
				" does not offer: " + model)
		}
	}

	if c.BrainRelateCandidates <= 0 || c.BrainNeighborCap <= 0 || c.BrainSearchLimit <= 0 {
		return errors.New("brain count fields must be > 0")
	}
	if c.BrainRelateMinWeight <= 0 || c.BrainRelateMinWeight > 1 {
		return errors.New("BrainRelateMinWeight must be in (0, 1]")
	}
	if c.BrainTagJaccardMin <= 0 || c.BrainTagJaccardMin > 1 {
		return errors.New("BrainTagJaccardMin must be in (0, 1]")
	}
	if c.BrainAssessmentMaxTokens <= 0 || c.BrainBodyMaxChars <= 0 || c.BrainSearchMaxTokens <= 0 {
		return errors.New("brain size fields must be > 0")
	}
	if c.BrainPromptVersion == "" {
		return errors.New("BrainPromptVersion is empty")
	}
	if c.BrainPromoteBatch <= 0 || c.BrainCommitBatch <= 0 {
		return errors.New("brain capture batch sizes must be > 0")
	}
	if c.BrainScanIdleInterval <= 0 || c.BrainScanBackoffMin <= 0 || c.BrainScanBackoffMax < c.BrainScanBackoffMin {
		return errors.New("brain scan intervals must be > 0 and the backoff ceiling must not be below its floor")
	}
	if c.BrainScanPDFPath == "" || c.BrainScanPDFPages <= 0 || c.BrainScanPDFTimeout <= 0 || c.BrainScanMaxPDFBytes <= 0 {
		return errors.New("brain scan pdf fields must be set and > 0")
	}
	if c.BrainScanPDFMinChars <= 0 || c.BrainScanPDFMaxChars <= c.BrainScanPDFMinChars {
		return errors.New("BrainScanPDFMinChars must be > 0 and below BrainScanPDFMaxChars")
	}
	if c.BrainGraphDefaultNodes <= 0 || c.BrainGraphMaxNodes < c.BrainGraphDefaultNodes {
		return errors.New("BrainGraphDefaultNodes must be > 0 and not above BrainGraphMaxNodes")
	}
	if c.BrainScanDepth <= 0 {
		return errors.New("BrainScanDepth must be > 0")
	}
	if c.BrainVersionsPerNode <= 0 || c.BrainNodeVersionsInline <= 0 {
		return errors.New("brain version bounds must be > 0")
	}
	if c.BrainScanBatch <= 0 || c.BrainScanConcurrency <= 0 || c.BrainScanMaxFileBytes <= 0 {
		return errors.New("brain scan fields must be > 0")
	}

	if c.SearchTimeout <= 0 || c.CrawlTimeout <= 0 || c.RefineTimeout <= 0 || c.ResearchTimeout <= 0 || c.LLMHealthTimeout <= 0 {
		return errors.New("timeout fields must be > 0")
	}

	if c.MaxConcurrentCrawls <= 0 || c.MaxConcurrentRefines <= 0 || c.GlobalCrawlSlots <= 0 || c.GlobalRefineSlots <= 0 {
		return errors.New("concurrency fields must be > 0")
	}

	if c.SearchDefaultCount <= 0 || c.SearchMaxCount <= 0 || c.TopNForResearch <= 0 {
		return errors.New("cap fields must be > 0")
	}

	if c.FetchPageMaxTokens <= 0 || c.ResearchBriefMaxTokens <= 0 ||
		c.WebSearchMaxTokens <= 0 || c.SummaryMaxTokens <= 0 {
		return errors.New("output ceiling fields must be > 0")
	}

	if c.PerHostMinInterval <= 0 {
		return errors.New("politeness fields must be > 0")
	}

	if c.StorePath == "" {
		return errors.New("StorePath is empty")
	}
	if c.PageCacheTTL <= 0 {
		return errors.New("PageCacheTTL must be > 0")
	}
	if c.RefinePromptVersion == "" {
		return errors.New("RefinePromptVersion is empty")
	}

	if c.EcommerceLookupMaxTokens <= 0 || c.TikTokProfileMaxTokens <= 0 ||
		c.GMapsLookupMaxTokens <= 0 || c.InstagramProfileMaxTokens <= 0 {
		return errors.New("scraper tool output ceilings must be > 0")
	}
	if c.ProductDescriptionMaxChars <= 0 || c.BioMaxChars <= 0 || c.BusinessAboutMaxChars <= 0 {
		return errors.New("scraper clamp lengths must be > 0")
	}
	if c.GMapsPageTimeout <= 0 {
		return errors.New("GMapsPageTimeout must be > 0")
	}
	if c.GMapsPageTimeout > c.CrawlTimeout && c.CrawlTimeout > 0 {
		return errors.New("GMapsPageTimeout must not exceed CrawlTimeout")
	}
	if c.CodingModel == "" {
		return errors.New("CodingModel is empty")
	}
	// Every provider must offer a model, and its default must be one of them —
	// a picker whose default is not in its own list is a picker that produces
	// a request the daemon then rejects.
	for _, prov := range c.LLMProviders {
		if prov.ID == "" || len(prov.Models) == 0 {
			return errors.New("every LLMProviders entry needs an ID and at least one model")
		}
		if !c.HasLLMModel(prov.ID, prov.DefaultModel) {
			return errors.New("LLMProviders entry " + prov.ID + " has a default model that is not one of its models")
		}
	}
	if len(c.LLMProviders) == 0 {
		return errors.New("LLMProviders is empty")
	}
	if len(c.CodingModels) == 0 {
		return errors.New("CodingModels is empty")
	}
	// A default nobody can pick would leave the picker unable to represent the
	// run it is about to create.
	if !c.HasCodingModel(c.CodingModel) {
		return errors.New("CodingModel is not one of CodingModels")
	}
	if c.CodingPermissionMode == "" {
		return errors.New("CodingPermissionMode is empty")
	}
	if c.CodingRunTimeout <= 0 {
		return errors.New("CodingRunTimeout must be > 0")
	}
	if c.TranscriptDir == "" {
		return errors.New("TranscriptDir is empty")
	}
	if c.AttachmentDir == "" {
		return errors.New("AttachmentDir is empty")
	}
	if c.CodingStopGrace <= 0 {
		return errors.New("CodingStopGrace must be > 0")
	}
	if c.CodingStderrMaxBytes <= 0 {
		return errors.New("CodingStderrMaxBytes must be > 0")
	}
	if c.CodingAttachmentMaxBytes <= 0 {
		return errors.New("CodingAttachmentMaxBytes must be > 0")
	}

	if c.GMapsWaitForSelector == "" {
		return errors.New("GMapsWaitForSelector is empty")
	}

	// PlacesAPIKey is deliberately not checked: a missing credential is a
	// normal install, not a broken config.
	if c.MapsSearchDefaultCount <= 0 || c.MapsSearchMaxCount <= 0 || c.MapsSearchMaxTokens <= 0 {
		return errors.New("maps search cap fields must be > 0")
	}
	if c.MapsSearchDefaultCount > c.MapsSearchMaxCount {
		return errors.New("MapsSearchDefaultCount must not exceed MapsSearchMaxCount")
	}

	if c.MapScrapeBaseURL == "" {
		return errors.New("MapScrapeBaseURL is empty")
	}
	if _, err := url.ParseRequestURI(c.MapScrapeBaseURL); err != nil {
		return errors.New("invalid URL in config: " + c.MapScrapeBaseURL)
	}
	if c.MapScrapeWaitSelector == "" {
		return errors.New("MapScrapeWaitSelector is empty")
	}
	// MapScrapeComposeFile is deliberately not checked: an absent compose file
	// costs the ability to *start* the sidecar, not the ability to use one that
	// is already running.
	if c.MapScrapeStartTimeout <= 0 {
		return errors.New("MapScrapeStartTimeout must be > 0")
	}
	if c.MapScrapeModelMaxChars <= 0 || c.MapScrapeModelMaxItems <= 0 || c.MapScrapeModelMaxTokens <= 0 {
		return errors.New("the mapscrape model-fallback bounds must be > 0")
	}
	if c.ContactModelMaxChars <= 0 || c.ContactModelMaxTokens <= 0 {
		return errors.New("the contact-enrichment bounds must be > 0")
	}
	if c.ExportDir == "" {
		return errors.New("ExportDir is empty")
	}
	if c.MapScrapeTimeout <= 0 {
		return errors.New("MapScrapeTimeout must be > 0")
	}
	if c.MapScrapeMaxResults <= 0 || c.MapScrapeMaxScrolls <= 0 {
		return errors.New("maps scrape bounds must be > 0")
	}

	if c.LeadgenCategoryVersion == "" {
		return errors.New("LeadgenCategoryVersion is empty")
	}
	if c.LeadgenBatchSize <= 0 || c.LeadgenClassifyMaxTokens <= 0 {
		return errors.New("leadgen categorization fields must be > 0")
	}

	if c.LeadgenGapVersion == "" {
		return errors.New("LeadgenGapVersion is empty")
	}
	if c.LeadgenGapMaxTokens <= 0 || c.LeadgenGapMinCompanies <= 0 {
		return errors.New("leadgen gap-analysis fields must be > 0")
	}

	if c.LeadgenEmailVersion == "" {
		return errors.New("LeadgenEmailVersion is empty")
	}
	if c.LeadgenEmailMaxTokens <= 0 {
		return errors.New("LeadgenEmailMaxTokens must be > 0")
	}
	if c.LeadgenRegionTTL <= 0 {
		return errors.New("LeadgenRegionTTL must be > 0")
	}
	if c.LeadsPageDefault <= 0 || c.LeadsPageMax < c.LeadsPageDefault || c.LeadRunsMax <= 0 {
		return errors.New("lead ledger page bounds must be > 0 with Max >= Default")
	}
	if c.ChatSessionsMax <= 0 || c.ChatTurnsPage <= 0 || c.ChatTurnsMax < c.ChatTurnsPage {
		return errors.New("chat archive page bounds must be > 0 with Max >= page")
	}
	if c.ChatSearchDefault <= 0 || c.ChatSearchMax < c.ChatSearchDefault {
		return errors.New("chat search bounds must be > 0 with Max >= Default")
	}

	// Project memory (M8). ClaudeProjectsDir is deliberately not checked: an
	// unreadable home directory costs the memory one of its two sources, which
	// is a smaller memory, not a broken config.
	if c.MemoryRecapMaxTokens <= 0 {
		return errors.New("MemoryRecapMaxTokens must be > 0")
	}
	if c.MemoryRecapConcurrency <= 0 {
		return errors.New("MemoryRecapConcurrency must be > 0")
	}
	if c.MemoryRecapMaxAttempts <= 0 {
		return errors.New("MemoryRecapMaxAttempts must be > 0")
	}
	if c.MemoryIngestBatch <= 0 || c.MemoryLazyCatchup <= 0 {
		return errors.New("memory ingest batch sizes must be > 0")
	}
	if c.MemoryBriefMaxTokens <= 0 || c.MemoryRecallMaxTokens <= 0 {
		return errors.New("memory response ceilings must be > 0")
	}
	if c.MemoryRecentEpisodes <= 0 || c.MemoryRecallLimit <= 0 || c.MemoryNoteLimit <= 0 {
		return errors.New("memory result caps must be > 0")
	}
	if c.MemoryHotFileDays <= 0 {
		return errors.New("MemoryHotFileDays must be > 0")
	}
	if c.MemoryOpenEpisodeGrace <= 0 || c.MemoryIngestInterval <= 0 {
		return errors.New("memory interval fields must be > 0")
	}
	if c.MemoryPromptVersion == "" {
		return errors.New("MemoryPromptVersion is empty")
	}

	return nil
}

// ValidateDaemon adds the checks only cmd/mimir-daemon needs. It is separate
// from Validate because cmd/mimir-mcp has no listen address and no token, and
// must not start failing over values that mean nothing to it.
func (c Config) ValidateDaemon() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.DaemonHost == "" {
		return errors.New("DaemonHost is empty")
	}
	if c.DaemonPort < 0 || c.DaemonPort > 65535 {
		return errors.New("DaemonPort must be within 0-65535")
	}
	// Fail closed. An unauthenticated local HTTP server with the coding-task
	// runner behind it is a shell on the operator's machine for anything that
	// can reach the port, so there is no "no token" mode to fall back to.
	if c.DaemonAuthToken == "" {
		return errors.New("MIMIR_DAEMON_TOKEN is empty — the daemon is started by its parent process, " +
			"which must generate a per-launch token and pass it down; it will not run unauthenticated")
	}
	if c.DaemonReadHeaderTimeout <= 0 || c.DaemonShutdownTimeout <= 0 {
		return errors.New("daemon timeout fields must be > 0")
	}
	if c.DaemonMaxRequestBytes <= 0 {
		return errors.New("DaemonMaxRequestBytes must be > 0")
	}
	return nil
}
