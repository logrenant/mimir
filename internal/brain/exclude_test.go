package brain

import "testing"

// The matcher has to be about path components, not string prefixes. `/a/b`
// covering `/a/bravo.go` is the bug that makes a denylist decorative, and it is
// the one worth a test of its own.
func TestExcluder_MatchesSubtreesNotStringPrefixes(t *testing.T) {
	e := NewExcluder([]string{"/Users/x/private", "/Users/x/notes/salary.md"})

	covered := []string{
		"/Users/x/private",
		"/Users/x/private/a.go",
		"/Users/x/private/deep/nested/b.txt",
		"/Users/x/notes/salary.md",
	}
	for _, p := range covered {
		if !e.Excludes(p) {
			t.Errorf("Excludes(%q) = false, want true", p)
		}
	}

	free := []string{
		"/Users/x/privateer.go",
		"/Users/x/private-notes/a.go",
		"/Users/x/notes/salary.md.bak",
		"/Users/x/notes",
		"/Users/x",
	}
	for _, p := range free {
		if e.Excludes(p) {
			t.Errorf("Excludes(%q) = true, want false", p)
		}
	}
}

func TestExcluder_ZeroValueExcludesNothing(t *testing.T) {
	var e Excluder
	if !e.Empty() {
		t.Error("the zero Excluder should be empty")
	}
	if e.Excludes("/anything/at/all") {
		t.Error("the zero Excluder should exclude nothing")
	}
}

// Relative entries are dropped rather than kept as a trap: a relative path
// cannot be compared against the absolute one a scan holds, so keeping it would
// be a rule that silently never fires.
func TestExcluder_DropsRelativeAndBlankEntries(t *testing.T) {
	e := NewExcluder([]string{"", "  ", "relative/path", "/abs/ok"})
	if got := e.List(); len(got) != 1 || got[0] != "/abs/ok" {
		t.Fatalf("List() = %v, want [/abs/ok]", got)
	}
}

func TestExcluder_NormalisesBeforeComparing(t *testing.T) {
	e := NewExcluder([]string{"/Users/x/private/"})
	if !e.Excludes("/Users/x/./private/../private/file.go") {
		t.Error("an uncleaned path should still be matched")
	}
}

// The whole point of B-1: these must never reach a model call, whatever the
// operator has or has not configured.
func TestIsSecret_CoversCredentialShapes(t *testing.T) {
	secret := []string{
		".env",
		".env.local",
		"config/.env.production",
		"app/.envrc",
		"secrets/id_rsa",
		"secrets/id_ed25519",
		"certs/server.pem",
		"certs/server.key",
		"certs/bundle.p12",
		".npmrc",
		".netrc",
		".pgpass",
		"kubeconfig",
		"credentials",
		"deploy/credentials.json",
		"gcp/my-service-account.json",
		"infra/terraform.tfstate",
		"infra/terraform.tfstate.backup",
		"infra/terraform.tfvars",
		"home/.ssh/known_hosts",
		"home/.aws/config",
		"vault/passwords.kdbx",
	}
	for _, p := range secret {
		if !isSecret(p) {
			t.Errorf("isSecret(%q) = false, want true", p)
		}
		if scannable(p) {
			t.Errorf("scannable(%q) = true — a credential reached the eligible set", p)
		}
	}
}

// The other half of a denylist: it has to leave ordinary work alone. A
// fragment list that also drops `secrets.md` or `keyboard.go` costs the
// knowledge base real content, and nobody notices for weeks.
func TestIsSecret_LeavesOrdinaryFilesAlone(t *testing.T) {
	fine := []string{
		"docs/secrets.md",
		"internal/keyboard.go",
		"src/environment.ts",
		"cmd/envelope/main.go",
		"README.md",
		"pkg/keys.go",
		"docs/monkey.txt",
		"internal/api/settings.go",
		"handbook/Onboarding.pdf",
	}
	for _, p := range fine {
		if isSecret(p) {
			t.Errorf("isSecret(%q) = true, want false", p)
		}
		if !scannable(p) {
			t.Errorf("scannable(%q) = false — an ordinary file was dropped", p)
		}
	}
}
