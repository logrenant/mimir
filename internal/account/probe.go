package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// probeTimeout bounds `claude auth status`. It reads a keychain entry and
// prints JSON; anything slower than this is a stuck keychain prompt, not work.
const probeTimeout = 15 * time.Second

// Status is what `claude auth status` reports for one credential slot.
//
// It costs nothing — no API call, no tokens — which is why the desktop app can
// ask on demand rather than caching a guess. It is also the only honest answer
// to "which account is this?": the directory is just a hash input, and the
// identity behind it lives in the keychain.
type Status struct {
	LoggedIn         bool   `json:"logged_in"`
	Email            string `json:"email,omitempty"`
	OrgName          string `json:"org_name,omitempty"`
	SubscriptionType string `json:"subscription_type,omitempty"`
	AuthMethod       string `json:"auth_method,omitempty"`
	// Error carries why the probe could not answer. A slot that cannot be
	// read is a slot whose runs will fail, and saying so here is cheaper than
	// finding out one run at a time.
	Error string `json:"error,omitempty"`
}

// cliStatus is the subset of the CLI's JSON this needs.
type cliStatus struct {
	LoggedIn         bool   `json:"loggedIn"`
	Email            string `json:"email"`
	OrgName          string `json:"orgName"`
	SubscriptionType string `json:"subscriptionType"`
	AuthMethod       string `json:"authMethod"`
}

// Probe asks the CLI who is signed in to one slot.
//
// It never returns an error for "not signed in" — that is an answer, and the
// operator's to act on. An error here means the probe itself could not run.
func Probe(ctx context.Context, cliPath, configDir string) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, cliPath, "auth", "status")
	cmd.Env = Environ(os.Environ(), configDir)

	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Status{Error: "the account probe timed out"}, nil
		}
		// A non-zero exit is how the CLI reports a slot it cannot read. That
		// is diagnostic, not a failure of this call.
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return Status{Error: detail}, nil
	}

	var parsed cliStatus
	if err := json.Unmarshal(out, &parsed); err != nil {
		// Output this call cannot read is still an answer about the slot, not
		// a failure of the daemon: the operator needs "this one cannot be
		// read" on the account row, not a 500 on the whole screen.
		return Status{Error: fmt.Sprintf("`%s auth status` did not answer in JSON", cliPath)}, nil
	}
	return Status{
		LoggedIn:         parsed.LoggedIn,
		Email:            parsed.Email,
		OrgName:          parsed.OrgName,
		SubscriptionType: parsed.SubscriptionType,
		AuthMethod:       parsed.AuthMethod,
	}, nil
}

// Environ builds the environment a `claude` subprocess should be given for one
// slot.
//
// Two jobs, and both matter:
//
//   - point the CLI at the right credential slot. An empty configDir means the
//     default one, expressed by leaving the variable out. Setting it to the
//     empty string reaches the same slot — the operator's own shell function
//     does exactly that, and probing both ways returns the same identity — but
//     absent is the narrower claim of the two, so it is the one made here.
//   - strip the session variables of whatever launched the daemon. If the
//     daemon was started from inside a Claude Code session (which is how the
//     dev shell runs), the child inherits CLAUDECODE=1, a session id, a
//     messaging socket and a bridge token. A nested CLI reading those believes
//     it is resuming somebody else's session.
func Environ(parent []string, configDir string) []string {
	out := make([]string, 0, len(parent)+1)
	for _, kv := range parent {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if isInheritedSessionVar(key) {
			continue
		}
		out = append(out, kv)
	}
	if configDir != "" {
		out = append(out, "CLAUDE_SECURESTORAGE_CONFIG_DIR="+configDir)
	}
	return out
}

// isInheritedSessionVar reports whether a variable belongs to the Claude Code
// session that started this process rather than to the machine.
func isInheritedSessionVar(key string) bool {
	switch key {
	case "CLAUDECODE", "CLAUDE_PID", "CLAUDE_EFFORT",
		"CLAUDE_SECURESTORAGE_CONFIG_DIR", "CLAUDE_CONFIG_DIR", "AI_AGENT":
		return true
	}
	return strings.HasPrefix(key, "CLAUDE_CODE_")
}
