# Mimir

Two macOS binaries over one runtime engine:

- **`bin/mimir-mcp`** — a local, zero-cost **MCP server** for a Claude Code
  session. It searches the web, scrapes pages, and returns **compact, refined**
  results (never raw dumps), plus free no-login scrapers for
  e-commerce / TikTok / Google Maps / Instagram. Refinement rides your existing
  `claude` CLI login — no API key.
- **`bin/mimir-daemon`** — a long-running, loopback-only HTTP service the Tauri
  desktop app (`desktop/`) talks to. It owns a folder-scoped coding-task runner
  with live streaming and the Google Maps lead-gen pipeline, and re-exposes the
  same MCP tools at `/mcp`. Installed as a **launchd agent** (`make
  install-agent`), it starts at login and is restarted if it dies — it runs
  whether or not the app is open.
- **`Mimir.app`** — a **menu-bar app** (no Dock icon) over that daemon. ⌘⇧G, or
  the menu-bar item, opens a quick-task window: pick a folder, type what Claude
  should do, `⏎`. The run keeps streaming if you dismiss the window and
  finishes with a notification.

Every roadmap milestone across both tracks is **shipped** (Track A MVP + free
scrapers; Track B M1–M8).

Since M8 it also keeps a **per-project memory**: it distils your own Claude Code
session transcripts into compact, searchable context and hands it back through
MCP, so a new session starts knowing what earlier ones established instead of
re-reading the repository to find out.

## What it can do

See **[`docs/CAPABILITIES.md`](docs/CAPABILITIES.md)** (Türkçe:
**[`docs/CAPABILITIES.tr.md`](docs/CAPABILITIES.tr.md)**) for the full feature
inventory — every MCP tool, every daemon route, the desktop screens, the
lead-gen pipeline, and what each one needs to run.

Quick map:

| Surface | Highlights |
|---|---|
| **MCP tools** (`bin/mimir-mcp`) | `web_search`, `fetch_page`, `research`, `diagnostics`, `ecommerce_product_lookup`, `tiktok_profile_lookup`, `gmaps_business_lookup`, `instagram_profile_lookup`, `maps_search` (only with a Places key), the project-memory tools `project_context`, `context_recall`, `context_remember`, and the knowledge-base tools `brain_ingest_data`, `brain_ingest_github`, `brain_query_nodes`, `brain_related` |
| **Daemon HTTP** (`bin/mimir-daemon`) | `/healthz`, `/diagnostics`, `/projects`, `/coding-tasks`, `GET /ws/runs/{id}` (live run stream), `/maps/leadgen`, `/maps/emails/status`, `/mcp` |
| **Desktop app** (`desktop/`) | Menu-bar item (daemon status, new task, login item) · Quick task (⌘⇧G) · Connection handshake · Workspace (pick a folder, run a scoped `claude` coding task, watch its thought/action stream live) · Leadgen (region search → categorize → per-category gap analysis → drafted outreach emails) |

## Build & verify

```bash
make check      # build + vet + lint + test + race — must be green (Go only)
make e2e        # end-to-end MCP smoke test against mock services
make desktop-check   # desktop gate: typecheck + vitest + cargo fmt/clippy/test
```

## Install it as a running system

```bash
make install-mcp      # registers mimir-mcp with every MCP client on the machine
make install-agent    # mimir-daemon under launchd: at login, restarted if it dies
make desktop-build    # builds Mimir.app
cp -R desktop/src-tauri/target/release/bundle/macos/Mimir.app /Applications/
```

`make install-mcp` covers the Claude Code CLI (user scope, so it follows you
between directories), `agy` and Antigravity IDE, the `gemini` CLI, and VS Code —
each through its own `mcp add`. It also installs a session preflight hook that
checks the binary, restarts the daemon if it is down, and reminds the model to
call `project_context` before re-reading a repository it already knows. Running
it again is a no-op; `make uninstall-mcp` reverses it.

`make agent-status` · `make agent-logs` · `make agent-restart` ·
`make uninstall-agent`. Details, including token handling and rotation:
[`docs/INSTALL.md`](docs/INSTALL.md) and [`docs/SECURITY.md`](docs/SECURITY.md).

Install, prerequisites (Docker for Crawl4AI, the `claude` CLI, the optional
Playwright Maps sidecar, the desktop toolchain): **[`docs/INSTALL.md`](docs/INSTALL.md)**.

## Docs

| File | What |
|---|---|
| [`AGENTS.md`](AGENTS.md) | Build rules, the four roles, the `do task-NN` protocol |
| [`docs/CAPABILITIES.md`](docs/CAPABILITIES.md) · [`.tr.md`](docs/CAPABILITIES.tr.md) | **Everything the built system does today** (EN · TR) |
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | Pipeline + package design, both tracks |
| [`docs/ROADMAP.md`](docs/ROADMAP.md) | The single roadmap: Track A + Track B, all milestones shipped |
| [`docs/AGENT_RULES.md`](docs/AGENT_RULES.md) | Detailed rules, strict directives, workflows |
| [`docs/SECURITY.md`](docs/SECURITY.md) | Context-isolation choke-point, loopback + token, the one credential exception |
| [`docs/INSTALL.md`](docs/INSTALL.md) | Setup and troubleshooting |
| [`tasks/README.md`](tasks/README.md) | Historical task board (all task files retired) |
| [`CHANGELOG.md`](CHANGELOG.md) | Released versions |
