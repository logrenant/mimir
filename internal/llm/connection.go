package llm

// A connection is one configured way to reach models, and it is the unit this
// package routes on.
//
// ---------------------------------------------------------------------------
// Why a connection and not a binary.
// ---------------------------------------------------------------------------
// Providers used to be identified by their CLI's name, and three things broke
// against that.
//
// **Two ways to the same vendor cannot coexist.** "Claude Code CLI" spends the
// operator's subscription; "Anthropic API" spends a key with its own bill. They
// are the same models and a completely different budget, and a map keyed by
// binary name holds one of them.
//
// **The quota does not belong to the binary.** Measured, not remembered:
// Antigravity has no subscription of its own — it rides a Google AI plan — and
// the Gemini CLI's quota pool is decided by its *auth method*, not by the
// binary: personal OAuth draws Code Assist's allowance (raised by Google AI
// Pro), an AI Studio key draws a separate one, Vertex and Workspace two more.
// So `agy` and an OAuth `gemini` signed into the same Google account spend one
// wallet, and an API-key `gemini` does not.
//
// **The operator cannot add one.** The list was fixed at compile time.
//
// ---------------------------------------------------------------------------
// What deliberately does NOT change.
// ---------------------------------------------------------------------------
// `Selection` stays a `(provider, model)` pair and `Selection.Key()` stays
// byte-identical. The only thing that changes is what the first string denotes:
// a connection id rather than a binary name, and the four built-in connections
// carry the ids the binaries already had.
//
// That one decision removes the migration entirely. A `settings.json` holding
// `provider: "agy"` still resolves, and every cache key derived from
// `Selection.Key()` — leadgen's categorisation and drafts, catalog's research
// and rewrites — is unchanged, so nothing an operator paid for is invalidated
// by a refactor they did not ask for.

// Adapter is how a connection actually reaches models. It is a closed set,
// fixed at compile time.
//
// Closed on purpose: an adapter decides which binary runs or which endpoint is
// called, and an operator naming one would be an operator naming a command. The
// operator picks from this list; they never spell a transport.
type Adapter string

const (
	AdapterClaudeCLI Adapter = "claude-cli"
	AdapterAgyCLI    Adapter = "agy-cli"
	AdapterGeminiCLI Adapter = "gemini-cli"
	AdapterOllamaCLI Adapter = "ollama-cli"

	// Declared, not implemented. They exist as names so the catalogue, the
	// wire shape and the picker are the final ones — see catalogue.go for why
	// the adapters themselves wait for an account to test against.
	AdapterAnthropicAPI Adapter = "anthropic-api"
	AdapterGeminiAPI    Adapter = "gemini-api"
	AdapterOpenAICompat Adapter = "openai-compatible"
)

// Transport is the coarse split a screen shows as a chip: a local subscription
// riding a CLI's own login, or an HTTP endpoint holding a key.
type Transport string

const (
	TransportCLI Transport = "cli"
	TransportAPI Transport = "api"
)

// Transport reports which half of the world this adapter lives in.
func (a Adapter) Transport() Transport {
	switch a {
	case AdapterClaudeCLI, AdapterAgyCLI, AdapterGeminiCLI, AdapterOllamaCLI:
		return TransportCLI
	}
	return TransportAPI
}

// Implemented reports whether this build can actually run this adapter.
//
// The API adapters are named but not written, and the distinction is deliberate
// rather than temporary scaffolding: naming them fixes the wire shape and the
// picker now, so adding one later changes a table and an adapter and nothing
// else. Routing to an unimplemented adapter is refused rather than attempted.
func (a Adapter) Implemented() bool {
	switch a {
	case AdapterClaudeCLI, AdapterAgyCLI, AdapterGeminiCLI, AdapterOllamaCLI:
		return true
	}
	return false
}

// ModelSpec is one model a connection offers.
type ModelSpec struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// ConnectionSpec is one connection, as the router reads it.
//
// It carries no secret and no executable path, and both omissions are load
// bearing:
//
//   - **No operator string ever becomes an executable or a flag.** A CLI
//     adapter takes its path from `internal/config`, never from a row. There is
//     no `cli_path` field, and adding one would turn an allow-list into a
//     shell.
//   - **No credential.** A secret is reached through a reference the registry
//     resolves, so this struct can be logged, marshalled and handed to a screen
//     without anybody having to remember not to.
type ConnectionSpec struct {
	// ID is the connection's identity and becomes Selection.Provider. It is a
	// wire string — it lands in settings.json and in every cache key derived
	// from Selection.Key() — so it is never renamed, only added to.
	ID      string  `json:"id"`
	Label   string  `json:"label"`
	Vendor  string  `json:"vendor"`
	Adapter Adapter `json:"adapter"`

	DefaultModel string      `json:"default_model"`
	Models       []ModelSpec `json:"models,omitempty"`
	// Discovered means the model list is asked for at runtime rather than
	// pinned, because it is a property of the machine (ollama's files) or of
	// an account rather than of this build.
	Discovered bool `json:"discovered,omitempty"`

	Caps Capabilities `json:"-"`

	Enabled bool `json:"enabled"`
	// Builtin means this connection ships with the binary. It cannot be
	// deleted, only disabled, and its CLI path and pinned model catalogue keep
	// coming from `internal/config` — the row contributes a label and an enable
	// flag and nothing else. That is what stops the connections table becoming
	// a back door around SD-1.
	Builtin bool `json:"builtin"`
}

// Transport is the chip a screen draws.
func (c ConnectionSpec) Transport() Transport { return c.Adapter.Transport() }

// ConnectKind is how an operator signs a connection in.
type ConnectKind string

const (
	// ConnectDaemon means the daemon can run the flow itself. Only `claude`
	// today: `internal/account` drives `claude auth login`, opens the browser
	// and watches for the callback.
	ConnectDaemon ConnectKind = "daemon"
	// ConnectManual means the operator has to do something in a terminal or a
	// file, and the honest thing is to say exactly what.
	//
	// Not a gap to be papered over. `agy` and `gemini` sign in through flows
	// this daemon cannot drive headlessly, and a button that pretended to would
	// either hang on a prompt nobody can answer or claim a success it did not
	// verify.
	ConnectManual ConnectKind = "manual"
)

// ConnectSpec is what an operator does to sign a connection in.
type ConnectSpec struct {
	Kind ConnectKind `json:"kind"`
	// Command is the exact thing to run, when there is one. Shown verbatim and
	// copyable: a paraphrase of a command is a command that does not work.
	Command string `json:"command,omitempty"`
	Hint    string `json:"hint"`
}

// Connect says how this connection is signed in.
//
// Per adapter rather than per vendor, because it is a property of the binary:
// the Gemini CLI is installed locally and still authenticates with a key or a
// cloud identity — calling it "its own session" was wrong, and its own refusal
// message is what corrected it.
func (c ConnectionSpec) Connect() ConnectSpec {
	switch c.Adapter {
	case AdapterClaudeCLI:
		return ConnectSpec{
			Kind: ConnectDaemon,
			Hint: "Mimir tarayıcıyı açar ve girişi kendi yuvasına kaydeder.",
		}
	case AdapterAgyCLI:
		return ConnectSpec{
			Kind:    ConnectManual,
			Command: "agy",
			Hint:    "Terminalde bir kez çalıştırın; kendi giriş akışını başlatır.",
		}
	case AdapterGeminiCLI:
		return ConnectSpec{
			Kind:    ConnectManual,
			Command: "gemini",
			Hint: "Bir kimlik yöntemi ister: GEMINI_API_KEY, " +
				"GOOGLE_GENAI_USE_VERTEXAI ya da GOOGLE_GENAI_USE_GCA. " +
				"Terminalde çalıştırıp seçin.",
		}
	case AdapterOllamaCLI:
		return ConnectSpec{
			Kind: ConnectManual,
			Hint: "Giriş gerektirmez — modeller bu makinede. Sunucu kapalıysa `ollama serve`.",
		}
	}
	return ConnectSpec{Kind: ConnectManual, Hint: ""}
}
