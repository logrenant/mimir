package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
)

func TestConfigDefaults(t *testing.T) {
	c := config.Load()

	if c.Crawl4AIBaseURL != "http://127.0.0.1:11235" {
		t.Errorf("expected %s, got %s", "http://127.0.0.1:11235", c.Crawl4AIBaseURL)
	}
	if c.DuckDuckGoHTMLURL != "https://html.duckduckgo.com/html/" {
		t.Errorf("expected %s, got %s", "https://html.duckduckgo.com/html/", c.DuckDuckGoHTMLURL)
	}
	if c.DuckDuckGoLiteURL != "https://lite.duckduckgo.com/lite/" {
		t.Errorf("expected %s, got %s", "https://lite.duckduckgo.com/lite/", c.DuckDuckGoLiteURL)
	}

	if c.ClaudeModel != "claude-haiku-4-5-20251001" {
		t.Errorf("expected %s, got %s", "claude-haiku-4-5-20251001", c.ClaudeModel)
	}
	if c.ClaudeCLIPath != "claude" {
		t.Errorf("expected %s, got %s", "claude", c.ClaudeCLIPath)
	}

	// Sized for a link that loses packets, not for a healthy one: a retransmit
	// costs seconds, and the earlier values were tight enough that one of them
	// turned a working fetch into a failed stage.
	if c.SearchTimeout != 30*time.Second {
		t.Errorf("expected %v, got %v", 30*time.Second, c.SearchTimeout)
	}
	if c.CrawlTimeout != 150*time.Second {
		t.Errorf("expected %v, got %v", 150*time.Second, c.CrawlTimeout)
	}
	if c.RefineTimeout != 180*time.Second {
		t.Errorf("expected %v, got %v", 180*time.Second, c.RefineTimeout)
	}
	if c.ResearchTimeout != 360*time.Second {
		t.Errorf("expected %v, got %v", 360*time.Second, c.ResearchTimeout)
	}
	// A liveness probe is not work, and is deliberately not scaled with the
	// budgets above: one unusable provider must not hold /diagnostics open.
	if c.LLMHealthTimeout != 20*time.Second {
		t.Errorf("expected %v, got %v", 20*time.Second, c.LLMHealthTimeout)
	}

	if c.MaxConcurrentCrawls != 4 {
		t.Errorf("expected %d, got %d", 4, c.MaxConcurrentCrawls)
	}
	if c.MaxConcurrentRefines != 2 {
		t.Errorf("expected %d, got %d", 2, c.MaxConcurrentRefines)
	}
	if c.GlobalCrawlSlots != 6 {
		t.Errorf("expected %d, got %d", 6, c.GlobalCrawlSlots)
	}
	if c.GlobalRefineSlots != 3 {
		t.Errorf("expected %d, got %d", 3, c.GlobalRefineSlots)
	}

	if c.SearchDefaultCount != 8 {
		t.Errorf("expected %d, got %d", 8, c.SearchDefaultCount)
	}
	if c.SearchMaxCount != 30 {
		t.Errorf("expected %d, got %d", 30, c.SearchMaxCount)
	}
	if c.TopNForResearch != 5 {
		t.Errorf("expected %d, got %d", 5, c.TopNForResearch)
	}

	if c.FetchPageMaxTokens != 1500 {
		t.Errorf("expected %d, got %d", 1500, c.FetchPageMaxTokens)
	}
	if c.ResearchBriefMaxTokens != 2000 {
		t.Errorf("expected %d, got %d", 2000, c.ResearchBriefMaxTokens)
	}
	if c.WebSearchMaxTokens != 1200 {
		t.Errorf("expected %d, got %d", 1200, c.WebSearchMaxTokens)
	}
	if c.SummaryMaxTokens != 200 {
		t.Errorf("expected %d, got %d", 200, c.SummaryMaxTokens)
	}

	if c.PerHostMinInterval != 1500*time.Millisecond {
		t.Errorf("expected %v, got %v", 1500*time.Millisecond, c.PerHostMinInterval)
	}

	if c.EcommerceLookupMaxTokens <= 0 || c.TikTokProfileMaxTokens <= 0 ||
		c.GMapsLookupMaxTokens <= 0 || c.InstagramProfileMaxTokens <= 0 {
		t.Error("expected scraper tool output ceilings to default > 0")
	}
	if c.GMapsPageTimeout <= 0 || c.GMapsPageTimeout > c.CrawlTimeout {
		t.Errorf("expected GMapsPageTimeout to default > 0 and <= CrawlTimeout, got %v (CrawlTimeout %v)", c.GMapsPageTimeout, c.CrawlTimeout)
	}
	if c.GMapsWaitForSelector == "" {
		t.Error("expected GMapsWaitForSelector to default non-empty")
	}

	if err := c.Validate(); err != nil {
		t.Fatalf("defaults should validate, got %v", err)
	}
}

func TestConfigOverrides(t *testing.T) {
	t.Setenv("MIMIR_CLAUDE_CLI_PATH", "/usr/local/bin/claude")

	c := config.Load()
	if c.ClaudeCLIPath != "/usr/local/bin/claude" {
		t.Errorf("expected %s, got %s", "/usr/local/bin/claude", c.ClaudeCLIPath)
	}

	// Empty override leaves the default untouched.
	t.Setenv("MIMIR_CLAUDE_CLI_PATH", "")
	c = config.Load()
	if c.ClaudeCLIPath != "claude" {
		t.Errorf("expected default after empty override, got %s", c.ClaudeCLIPath)
	}
}

func TestConfigValidateZero(t *testing.T) {
	var c config.Config
	if err := c.Validate(); err == nil {
		t.Error("expected zero config to fail validation")
	}
}

// --- daemon plumbing (task-22) ---------------------------------------------

func TestDaemonDefaults(t *testing.T) {
	// The daemon's own launchd environment leaks into the shells this is run
	// from, so an assertion about the *unset* default has to unset it itself or
	// it ends up testing the machine rather than Load. Empty reads as absent
	// (Load only takes the value when it is non-empty), which is the same
	// discipline the CLI-path and token cases above use.
	t.Setenv("MIMIR_DAEMON_PORT", "")

	c := config.Load()

	// Loopback is a security property of the daemon, not a preference, so it
	// is a constant with no override at all.
	if c.DaemonHost != "127.0.0.1" {
		t.Errorf("DaemonHost: got %q, want 127.0.0.1", c.DaemonHost)
	}
	if c.DaemonPort != 0 {
		t.Errorf("DaemonPort: got %d, want 0 (kernel-assigned unless the parent says otherwise)", c.DaemonPort)
	}
	if c.DaemonReadHeaderTimeout <= 0 || c.DaemonShutdownTimeout <= 0 || c.DaemonMaxRequestBytes <= 0 {
		t.Errorf("daemon timeouts and body cap should default > 0, got %+v", c)
	}
}

func TestDaemonPortOverride(t *testing.T) {
	t.Setenv("MIMIR_DAEMON_PORT", "51234")
	if got := config.Load().DaemonPort; got != 51234 {
		t.Errorf("DaemonPort: got %d, want 51234", got)
	}

	// Unparseable and out-of-range values keep the default rather than
	// failing — the same posture as every other override here.
	for _, bad := range []string{"not-a-port", "-1", "70000"} {
		t.Setenv("MIMIR_DAEMON_PORT", bad)
		if got := config.Load().DaemonPort; got != 0 {
			t.Errorf("port %q: got %d, want the default 0", bad, got)
		}
	}
}

func TestValidateDaemonRequiresAToken(t *testing.T) {
	t.Setenv("MIMIR_DAEMON_TOKEN", "")

	c := config.Load()
	if err := c.Validate(); err != nil {
		t.Fatalf("the stdio binary must not start failing over a daemon-only value: %v", err)
	}

	err := c.ValidateDaemon()
	if err == nil {
		t.Fatal("the daemon must refuse to start without a token")
	}
	// SD-6: the message has to name the fix, and the fix here is the parent
	// process, not something the operator types.
	if !strings.Contains(err.Error(), "MIMIR_DAEMON_TOKEN") {
		t.Errorf("error should name the missing variable, got %v", err)
	}

	t.Setenv("MIMIR_DAEMON_TOKEN", "a-token")
	if err := config.Load().ValidateDaemon(); err != nil {
		t.Errorf("a token should be all that is missing, got %v", err)
	}
}

// --- operator-provisioned credential (task-24) ------------------------------

func TestPlacesCredentialIsReadButNeverRequired(t *testing.T) {
	t.Setenv("MIMIR_DAEMON_TOKEN", "a-token")

	// Absent is the normal install: the binaries start, and RegisterAll simply
	// does not offer maps_search.
	t.Setenv("MIMIR_GOOGLE_PLACES_API_KEY", "")
	c := config.Load()
	if c.PlacesAPIKey != "" {
		t.Errorf("PlacesAPIKey: got %q, want empty", c.PlacesAPIKey)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("a missing Places key must not break Validate: %v", err)
	}
	if err := c.ValidateDaemon(); err != nil {
		t.Errorf("a missing Places key must not stop the daemon: %v", err)
	}

	t.Setenv("MIMIR_GOOGLE_PLACES_API_KEY", "test-key")
	if got := config.Load().PlacesAPIKey; got != "test-key" {
		t.Errorf("PlacesAPIKey: got %q, want test-key", got)
	}
}

// The caps around the credential are ordinary SD-1 constants: no override, and
// a default that cannot exceed the API's own ceiling.
func TestMapsSearchCapsAreConstants(t *testing.T) {
	c := config.Load()

	if c.MapsSearchDefaultCount != 20 || c.MapsSearchMaxCount != 60 || c.MapsSearchMaxTokens != 2000 {
		t.Errorf("unexpected maps search caps: %d/%d/%d", c.MapsSearchDefaultCount, c.MapsSearchMaxCount, c.MapsSearchMaxTokens)
	}
	if c.MapsSearchDefaultCount > c.MapsSearchMaxCount {
		t.Error("the default count must fit inside the ceiling")
	}

	c.MapsSearchDefaultCount = c.MapsSearchMaxCount + 1
	if err := c.Validate(); err == nil {
		t.Error("Validate should reject a default count above the ceiling")
	}
}

// The picker can only offer what the list holds, so a default outside it would
// leave every form unable to represent the run it is about to create.
func TestValidateRejectsADefaultModelThatIsNotOffered(t *testing.T) {
	c := config.Load()
	c.CodingModel = "claude-not-shipped"
	if err := c.Validate(); err == nil {
		t.Fatal("Validate accepted a CodingModel absent from CodingModels")
	}

	c = config.Load()
	c.CodingModels = nil
	if err := c.Validate(); err == nil {
		t.Fatal("Validate accepted an empty CodingModels")
	}
}

func TestDefaultModelIsOffered(t *testing.T) {
	c := config.Load()
	if !c.HasCodingModel(c.CodingModel) {
		t.Fatalf("CodingModel %q is not in CodingModels", c.CodingModel)
	}
	if c.HasCodingModel("") {
		t.Fatal(`"" must not be a model: it means "use the default"`)
	}
	for _, m := range c.CodingModels {
		if m.ID == "" || m.Label == "" {
			t.Errorf("incomplete model choice: %+v", m)
		}
	}
}

func TestHasLLMModel_AllowsOnlyPublishedPairs(t *testing.T) {
	c := config.Load()

	if !c.HasLLMModel("agy", "gemini-3.8-flash-high") {
		t.Error("agy/gemini-3.8-flash-high should be offerable")
	}
	// The same model id reached through the wrong CLI is not a near miss, it
	// is a subprocess that cannot run: the pairing is the constraint.
	if c.HasLLMModel("claude", "gemini-3.8-flash-high") {
		t.Error("a Gemini model must not be offerable through the claude CLI")
	}
	if c.HasLLMModel("gpt", "anything") {
		t.Error("an unknown provider must not be offerable")
	}
	// Empty model means "that provider's default", which is a valid request.
	if !c.HasLLMModel("claude", "") {
		t.Error("an empty model should mean the provider's default, not a rejection")
	}
	if got := c.LLMDefaultModel("claude"); got != "claude-haiku-4-5-20251001" {
		t.Errorf("LLMDefaultModel(claude) = %q", got)
	}
	if got := c.LLMDefaultModel("gpt"); got != "" {
		t.Errorf("LLMDefaultModel(gpt) = %q, want empty for an unknown provider", got)
	}
}

// Every provider's default must be one of its own models, or the picker offers
// a "varsayılan" the daemon then rejects.
func TestLLMProviders_DefaultsAreOfferable(t *testing.T) {
	c := config.Load()
	if len(c.LLMProviders) == 0 {
		t.Fatal("LLMProviders is empty")
	}
	for _, p := range c.LLMProviders {
		if !c.HasLLMModel(p.ID, p.DefaultModel) {
			t.Errorf("provider %s defaults to %q, which is not one of its models", p.ID, p.DefaultModel)
		}
	}
}

// A discovered provider's models are files on the machine, so there is no list
// to validate against and the check is on the shape of the name instead.
func TestHasLLMModel_DiscoveredProviderTakesAnyWellFormedTag(t *testing.T) {
	c := config.Load()

	for _, model := range []string{"qwen3:8b", "gpt-oss:20b", "nomic-embed-text:latest"} {
		if !c.HasLLMModel("ollama", model) {
			t.Errorf("gerçek bir ollama etiketi reddedildi: %q", model)
		}
	}
	// Empty is "that provider's default", which is valid everywhere.
	if !c.HasLLMModel("ollama", "") {
		t.Error("boş model reddedildi")
	}

	// The shape check is what stands in for the list. Nothing that could be
	// read as a flag, and nothing carrying a separator a name would not have.
	for _, bad := range []string{
		"--version",
		"-m",
		"qwen3 8b",
		"qwen3;rm -rf /",
		"qwen3\n8b",
		"$(whoami)",
	} {
		if c.HasLLMModel("ollama", bad) {
			t.Errorf("kabul edilmemesi gereken model kabul edildi: %q", bad)
		}
	}
}

// The exemption is narrow: a listed provider still has to name a model that is
// actually in its list.
func TestHasLLMModel_ListedProviderIsUnchanged(t *testing.T) {
	c := config.Load()
	if c.HasLLMModel("claude", "qwen3:8b") {
		t.Error("listeli sağlayıcı listede olmayan bir modeli kabul etti")
	}
	if !c.HasLLMModel("claude", "claude-sonnet-5") {
		t.Error("listeli sağlayıcı kendi modelini reddetti")
	}
}
