package llm

// The catalogue of ways to reach models: the ones this build can run, and the
// ones it is shaped to run.
//
// ---------------------------------------------------------------------------
// Why "coming soon" is data rather than a stub.
// ---------------------------------------------------------------------------
// A provider adapter is a claim about somebody else's interface. This
// repository has been bitten by writing such a claim from memory — an IKAS
// dialect profile that passed its own fixture and matched nothing anyone
// actually downloads — and the rule that came out of it is that a spec is only
// written from something somebody has in front of them.
//
// Every API provider here is in exactly that position: there is no key to test
// against, so an adapter written now would be written from documentation and
// shipped unexercised. The failure mode is not a compile error. It is a
// connection an operator adds, configures and selects, which then fails at the
// first real call — or worse, half-works and returns prose where a schema was
// asked for.
//
// So the catalogue ships and the adapters do not. An entry with
// `Status: StatusComingSoon` is a promise about the *shape* of the product, not
// about the code: the picker draws it, the operator sees what is coming, and
// `POST /llm/connections` refuses it by name with a reason. When a customer
// needs one, implementing it changes this table's `Status` and adds an adapter
// — and changes nothing else, which is the whole point of writing it down now.
//
// ---------------------------------------------------------------------------
// What a coming-soon entry deliberately does NOT carry.
// ---------------------------------------------------------------------------
// No base URL and no docs URL. Those are facts about somebody else's service,
// and an unverified fact in a table is the exact thing this comment is about.
// They arrive with the adapter that was tested against them.

// Status is whether this build can actually run an entry.
type Status string

const (
	// StatusAvailable means there is an adapter and it has been exercised.
	StatusAvailable Status = "available"
	// StatusComingSoon means the shape is settled and the adapter is not
	// written. The API refuses it rather than half-running it.
	StatusComingSoon Status = "coming-soon"
)

// Auth is what an operator has to supply.
type Auth string

const (
	// AuthCLILogin means the CLI carries its own session and signs in through
	// its own flow; Mimir never sees a credential. `claude` is this.
	AuthCLILogin Auth = "cli-login"
	// AuthCLIKey means a CLI that is installed locally but authenticates with a
	// key or a cloud identity rather than a session of its own.
	//
	// The Gemini CLI is this, and calling it a subscription was wrong: it
	// refuses until one of `GEMINI_API_KEY`, `GOOGLE_GENAI_USE_VERTEXAI` or
	// `GOOGLE_GENAI_USE_GCA` is configured — its own words, printed on this
	// machine. The binary is local; the credential is not a session.
	AuthCLIKey Auth = "cli-key"
	// AuthAPIKey means the operator pastes a key and `internal/secrets` keeps
	// it, with no local binary involved.
	AuthAPIKey Auth = "api-key"
)

// CatalogueEntry is one way to reach models, whether or not this build can.
type CatalogueEntry struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Vendor    string    `json:"vendor"`
	Adapter   Adapter   `json:"adapter"`
	Transport Transport `json:"transport"`
	Auth      Auth      `json:"auth"`
	Status    Status    `json:"status"`
	// Note says why an entry is not available yet, in the operator's terms.
	Note string `json:"note,omitempty"`
}

// notYetTested is the one reason every coming-soon entry has, said once.
const notYetTested = "Adaptör, gerçek bir hesaba karşı sınanabildiğinde eklenecek."

// catalogue is the closed set, in the order a picker should offer it: what runs
// today first, then what is coming, vendor by vendor.
//
// Adding a row here does not make anything run. It makes the product say what
// it will be, which is a different and smaller promise.
var catalogue = []CatalogueEntry{
	// --- available: the CLIs on this machine, riding their own logins -------
	{ID: "claude", Label: "Claude Code CLI", Vendor: "anthropic",
		Adapter: AdapterClaudeCLI, Transport: TransportCLI, Auth: AuthCLILogin, Status: StatusAvailable},
	{ID: "agy", Label: "Antigravity CLI", Vendor: "google",
		Adapter: AdapterAgyCLI, Transport: TransportCLI, Auth: AuthCLILogin, Status: StatusAvailable},
	// Nothing here is free. Antigravity rides a Google AI plan and every call
	// spends its quota; a label that said "ücretsiz" was making a claim about
	// somebody's bill that this table has no business making.
	{ID: "gemini", Label: "Gemini CLI", Vendor: "google",
		Adapter: AdapterGeminiCLI, Transport: TransportCLI, Auth: AuthCLIKey, Status: StatusAvailable},
	{ID: "ollama", Label: "Ollama", Vendor: "local",
		Adapter: AdapterOllamaCLI, Transport: TransportCLI, Auth: AuthCLILogin, Status: StatusAvailable},

	// --- coming soon: API keys ----------------------------------------------
	//
	// Anthropic and Google appear twice on purpose, and that duplication is the
	// product decision this whole refactor exists for: a subscription and a key
	// to the same vendor are two budgets, and an operator must be able to hold
	// both.
	{ID: "anthropic-api", Label: "Anthropic API", Vendor: "anthropic",
		Adapter: AdapterAnthropicAPI, Transport: TransportAPI, Auth: AuthAPIKey,
		Status: StatusComingSoon, Note: notYetTested},
	{ID: "openai", Label: "OpenAI", Vendor: "openai",
		Adapter: AdapterOpenAICompat, Transport: TransportAPI, Auth: AuthAPIKey,
		Status: StatusComingSoon, Note: notYetTested},
	{ID: "gemini-api", Label: "Google AI Studio (Gemini API)", Vendor: "google",
		Adapter: AdapterGeminiAPI, Transport: TransportAPI, Auth: AuthAPIKey,
		Status: StatusComingSoon, Note: notYetTested},
	{ID: "deepseek", Label: "DeepSeek", Vendor: "deepseek",
		Adapter: AdapterOpenAICompat, Transport: TransportAPI, Auth: AuthAPIKey,
		Status: StatusComingSoon, Note: notYetTested},
	{ID: "xai", Label: "xAI (Grok)", Vendor: "xai",
		Adapter: AdapterOpenAICompat, Transport: TransportAPI, Auth: AuthAPIKey,
		Status: StatusComingSoon, Note: notYetTested},
	{ID: "mistral", Label: "Mistral", Vendor: "mistral",
		Adapter: AdapterOpenAICompat, Transport: TransportAPI, Auth: AuthAPIKey,
		Status: StatusComingSoon, Note: notYetTested},
	{ID: "groq", Label: "Groq", Vendor: "groq",
		Adapter: AdapterOpenAICompat, Transport: TransportAPI, Auth: AuthAPIKey,
		Status: StatusComingSoon, Note: notYetTested},
	{ID: "openrouter", Label: "OpenRouter", Vendor: "openrouter",
		Adapter: AdapterOpenAICompat, Transport: TransportAPI, Auth: AuthAPIKey,
		Status: StatusComingSoon, Note: notYetTested},
	{ID: "together", Label: "Together AI", Vendor: "together",
		Adapter: AdapterOpenAICompat, Transport: TransportAPI, Auth: AuthAPIKey,
		Status: StatusComingSoon, Note: notYetTested},
	{ID: "fireworks", Label: "Fireworks AI", Vendor: "fireworks",
		Adapter: AdapterOpenAICompat, Transport: TransportAPI, Auth: AuthAPIKey,
		Status: StatusComingSoon, Note: notYetTested},
	{ID: "cerebras", Label: "Cerebras", Vendor: "cerebras",
		Adapter: AdapterOpenAICompat, Transport: TransportAPI, Auth: AuthAPIKey,
		Status: StatusComingSoon, Note: notYetTested},
	{ID: "openai-compatible", Label: "OpenAI uyumlu (kendi uç noktanız)", Vendor: "other",
		Adapter: AdapterOpenAICompat, Transport: TransportAPI, Auth: AuthAPIKey,
		Status: StatusComingSoon, Note: notYetTested},
}

// Catalogue returns every entry, in offer order.
func Catalogue() []CatalogueEntry {
	out := make([]CatalogueEntry, len(catalogue))
	copy(out, catalogue)
	return out
}

// CatalogueEntryByID resolves one entry.
func CatalogueEntryByID(id string) (CatalogueEntry, bool) {
	for _, e := range catalogue {
		if e.ID == id {
			return e, true
		}
	}
	return CatalogueEntry{}, false
}
