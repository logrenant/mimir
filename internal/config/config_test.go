package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/logrenant/goat-mcp/internal/config"
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

	if c.SearchTimeout != 10*time.Second {
		t.Errorf("expected %v, got %v", 10*time.Second, c.SearchTimeout)
	}
	if c.CrawlTimeout != 45*time.Second {
		t.Errorf("expected %v, got %v", 45*time.Second, c.CrawlTimeout)
	}
	if c.RefineTimeout != 60*time.Second {
		t.Errorf("expected %v, got %v", 60*time.Second, c.RefineTimeout)
	}
	if c.ResearchTimeout != 120*time.Second {
		t.Errorf("expected %v, got %v", 120*time.Second, c.ResearchTimeout)
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
	t.Setenv("GOAT_CLAUDE_CLI_PATH", "/usr/local/bin/claude")

	c := config.Load()
	if c.ClaudeCLIPath != "/usr/local/bin/claude" {
		t.Errorf("expected %s, got %s", "/usr/local/bin/claude", c.ClaudeCLIPath)
	}

	// Empty override leaves the default untouched.
	t.Setenv("GOAT_CLAUDE_CLI_PATH", "")
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
	t.Setenv("GOAT_DAEMON_PORT", "51234")
	if got := config.Load().DaemonPort; got != 51234 {
		t.Errorf("DaemonPort: got %d, want 51234", got)
	}

	// Unparseable and out-of-range values keep the default rather than
	// failing — the same posture as every other override here.
	for _, bad := range []string{"not-a-port", "-1", "70000"} {
		t.Setenv("GOAT_DAEMON_PORT", bad)
		if got := config.Load().DaemonPort; got != 0 {
			t.Errorf("port %q: got %d, want the default 0", bad, got)
		}
	}
}

func TestValidateDaemonRequiresAToken(t *testing.T) {
	t.Setenv("GOAT_DAEMON_TOKEN", "")

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
	if !strings.Contains(err.Error(), "GOAT_DAEMON_TOKEN") {
		t.Errorf("error should name the missing variable, got %v", err)
	}

	t.Setenv("GOAT_DAEMON_TOKEN", "a-token")
	if err := config.Load().ValidateDaemon(); err != nil {
		t.Errorf("a token should be all that is missing, got %v", err)
	}
}

// --- operator-provisioned credential (task-24) ------------------------------

func TestPlacesCredentialIsReadButNeverRequired(t *testing.T) {
	t.Setenv("GOAT_DAEMON_TOKEN", "a-token")

	// Absent is the normal install: the binaries start, and RegisterAll simply
	// does not offer maps_search.
	t.Setenv("GOAT_GOOGLE_PLACES_API_KEY", "")
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

	t.Setenv("GOAT_GOOGLE_PLACES_API_KEY", "test-key")
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
