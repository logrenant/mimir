package mapscrape_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The sidecar's URL allowlist is the one security boundary of a loopback
// service with a browser attached: without it, anything that can POST to the
// port can make Chromium fetch the host's own network, a cloud metadata
// endpoint, or a file:// path.
//
// It is written in JavaScript, so this test drives it through node — which
// server.js supports by requiring Playwright lazily and only listening when run
// as a program. node is not a build dependency of this repo, so the test skips
// where it is absent rather than making `make check` need a second toolchain;
// the live integration test asserts the same thing against the real container.
func TestSidecarAllowlist(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; the same check runs in the integration test")
	}

	server, err := filepath.Abs(filepath.Join("..", "..", "deploy", "playwright-maps", "server.js"))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		url   string
		allow bool
	}{
		{"https://www.google.com/maps/search/dentists", true},
		{"https://maps.google.com/maps/place/x", true},
		{"https://google.com/maps", true},

		// Host-based matching, not substring: each of these contains a string
		// that looks like Google and is not Google.
		{"https://evil.example/?x=www.google.com/maps/", false},
		{"https://google.com.evil.example/maps/search/x", false},
		{"https://www.google.com.evil.example/maps", false},

		// The SSRF cases the allowlist exists for.
		{"http://127.0.0.1:11235/health", false},
		{"http://169.254.169.254/latest/meta-data/", false},
		{"file:///etc/passwd", false},

		// Right host, wrong product: this is a browser, not a general fetcher.
		{"https://www.google.com/search?q=x", false},

		// Plaintext is refused even for the right host.
		{"http://www.google.com/maps/search/x", false},

		{"not a url", false},
		{"", false},
	}

	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			script := `const s=require(process.argv[1]);process.stdout.write(String(s.isMapsURL(process.argv[2])));`
			out, err := exec.Command(node, "-e", script, server, tc.url).CombinedOutput()
			if err != nil {
				t.Fatalf("node: %v\n%s", err, out)
			}
			got := strings.TrimSpace(string(out)) == "true"
			if got != tc.allow {
				t.Errorf("isMapsURL(%q) = %v, want %v", tc.url, got, tc.allow)
			}
		})
	}
}
