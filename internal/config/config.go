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

	// Timeouts
	SearchTimeout   time.Duration
	CrawlTimeout    time.Duration
	RefineTimeout   time.Duration
	ResearchTimeout time.Duration

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

// Load returns the fully-populated default Config.
// It parses specific environment variables for test overrides but ignores invalid values.
func Load() Config {
	c := Config{
		Crawl4AIBaseURL:        "http://127.0.0.1:11235",
		DuckDuckGoHTMLURL:      "https://html.duckduckgo.com/html/",
		DuckDuckGoLiteURL:      "https://lite.duckduckgo.com/lite/",
		ClaudeCLIPath:          "claude",
		ClaudeModel:            "claude-haiku-4-5-20251001",
		SearchTimeout:          10 * time.Second,
		CrawlTimeout:           45 * time.Second,
		RefineTimeout:          60 * time.Second,
		ResearchTimeout:        120 * time.Second,
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

		EcommerceLookupMaxTokens:   400,
		TikTokProfileMaxTokens:     400,
		GMapsLookupMaxTokens:       400,
		InstagramProfileMaxTokens:  300,
		ProductDescriptionMaxChars: 600,
		BioMaxChars:                300,
		BusinessAboutMaxChars:      400,
		GMapsPageTimeout:           30 * time.Second, // verified empirically: 20-25s was not enough for the h1 wait to succeed against a live container
		GMapsWaitForSelector:       "h1",

		CodingModel: "claude-sonnet-5",
		// In headless -p mode "default" does not prompt, it DENIES — a run
		// would finish having changed nothing. "acceptEdits" accepts file
		// edits while still withholding Bash; "bypassPermissions" would hand
		// over arbitrary shell execution and is deliberately not used. The
		// real boundary is the directory (internal/project), not this mode.
		CodingPermissionMode: "acceptEdits",
		CodingRunTimeout:     30 * time.Minute,

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
		MapScrapeTimeout:      120 * time.Second,
		MapScrapeWaitSelector: `div[role="feed"]`,
		MapScrapeMaxResults:   60,
		MapScrapeMaxScrolls:   12,

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
	if val := os.Getenv("MIMIR_CLAUDE_CLI_PATH"); val != "" {
		c.ClaudeCLIPath = val
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

	// Derived after the override above so a test pointing StorePath at a temp
	// directory gets an isolated transcript directory for free.
	c.TranscriptDir = filepath.Join(filepath.Dir(c.StorePath), "transcripts")

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

	if c.SearchTimeout <= 0 || c.CrawlTimeout <= 0 || c.RefineTimeout <= 0 || c.ResearchTimeout <= 0 {
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
	if c.CodingPermissionMode == "" {
		return errors.New("CodingPermissionMode is empty")
	}
	if c.CodingRunTimeout <= 0 {
		return errors.New("CodingRunTimeout must be > 0")
	}
	if c.TranscriptDir == "" {
		return errors.New("TranscriptDir is empty")
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
