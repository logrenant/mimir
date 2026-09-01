# GOAT

Two macOS binaries over one runtime engine:

- **`bin/goat-mcp`** — a local, zero-cost **MCP server** for a Claude Code
  session. It searches the web, scrapes pages, and returns **compact, refined**
  results (never raw dumps), plus free no-login scrapers for
  e-commerce / TikTok / Google Maps / Instagram. Refinement rides your existing
  `claude` CLI login — no API key.
- **`bin/goat-daemon`** — a long-running, loopback-only HTTP service the Tauri
  desktop app (`desktop/`) talks to. It owns a folder-scoped coding-task runner
  with live streaming and the Google Maps lead-gen pipeline, and re-exposes the
  same MCP tools at `/mcp`.

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
| **MCP tools** (`bin/goat-mcp`) | `web_search`, `fetch_page`, `research`, `diagnostics`, `ecommerce_product_lookup`, `tiktok_profile_lookup`, `gmaps_business_lookup`, `instagram_profile_lookup`, `maps_search` (only with a Places key), and the project-memory tools `project_context`, `context_recall`, `context_remember` |
| **Daemon HTTP** (`bin/goat-daemon`) | `/healthz`, `/diagnostics`, `/projects`, `/coding-tasks`, `GET /ws/runs/{id}` (live run stream), `/maps/leadgen`, `/maps/emails/status`, `/mcp` |
| **Desktop app** (`desktop/`) | Connection handshake · Workspace (pick a folder, run a scoped `claude` coding task, watch its thought/action stream live) · Leadgen (region search → categorize → per-category gap analysis → drafted outreach emails) |

## Build & verify

```bash
make check      # build + vet + lint + test + race — must be green (Go only)
make e2e        # end-to-end MCP smoke test against mock services
make desktop-check   # desktop gate: typecheck + vitest + cargo fmt/clippy/test
```

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
