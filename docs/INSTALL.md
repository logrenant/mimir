# Installation Guide

GOAT-MCP is a zero-cost, local Model Context Protocol (MCP) server that empowers your Claude Code session with the ability to search and read the web.

## Prerequisites

Before running the server, ensure you have the required local infrastructure running:

1. **Docker** (for Crawl4AI)
2. **Claude Code CLI** (`claude`), logged in — used headless for local text refinement
3. **Go 1.25+** (for building the server; required by the MCP Go SDK)
4. *(desktop app only)* **Node 20+** and **Rust** (https://rustup.rs) plus Xcode
   Command Line Tools — not needed for `make check` or the MCP server itself

### 1. Start Crawl4AI

Crawl4AI runs in a Docker container to safely render arbitrary web pages. From the root of this repository, start it via `docker-compose`:

```bash
make crawl-up
```
*(To stop it later, run `make crawl-down`)*

### 1b. (Optional) Start the Maps scrape sidecar

Only needed for the Maps lead-gen fallback — the path taken when the Google
Places API's coverage or cost is not worth it. Everything else runs without it,
and `diagnostics` marks it `optional`.

```bash
make maps-up
```
*(`make maps-down` to stop, `make maps-logs` to follow it.)*

The first run is slow: unlike Crawl4AI this image is built locally, pulling the
pinned Playwright image and installing the matching library. It is published on
loopback only and accepts Google Maps URLs alone — see
`deploy/playwright-maps/AGENTS.md` for why that check is load-bearing.

### 2. Prepare the `claude` CLI

Install [Claude Code](https://claude.com/claude-code) if you haven't already, and make sure `claude` is on your `$PATH`.

Log in once, interactively:

```bash
claude login
```

goat-mcp calls `claude` headlessly (`-p`, no tools, no session persistence) to refine scraped pages — it rides this login, so no separate API key is needed. Model `claude-haiku-4-5-20251001` is pinned in `internal/config`.

## Building the Binary

Compile the GOAT-MCP binary:

```bash
make build
```

This will produce the executable at `bin/goat-mcp`.

## The desktop app (dev)

The Tauri shell starts `goat-daemon` itself: it picks a free loopback port,
mints a per-launch bearer token, passes both to the daemon in its environment,
and reaps it on quit. There is nothing to configure and no port to remember.

```bash
make desktop-sidecar   # builds goat-daemon into the app's sidecar slot
make desktop-dev       # runs the app (first run compiles Rust: a few minutes)
```

`make desktop-check` is the app's own gate (typecheck, vitest, `cargo fmt`,
`cargo clippy`, `cargo test`). It is deliberately **not** part of `make check`,
so contributing to the Go side never requires a Node or Rust toolchain.

## Registering with Claude Code

To add the GOAT-MCP server to your Claude Code session, use the absolute path to the binary you just built.

```bash
claude mcp add goat-mcp -- /absolute/path/to/goat-remastered/bin/goat-mcp
```
*(Replace `/absolute/path/to` with your actual path)*

## Verification

Once registered, start or reload your Claude Code session. You can verify that GOAT-MCP is healthy by asking Claude to run the `diagnostics` tool:

> **You:** "Please run the goat-mcp diagnostics tool to check if the services are healthy."
> **Claude:** *Runs tool and confirms DuckDuckGo, Crawl4AI, and the claude CLI are reachable.*

## Troubleshooting

If the diagnostics tool reports an error or if searches fail, check the table below:

| Error Message | Likely Cause | Solution |
|---------------|--------------|----------|
| `DuckDuckGo unavailable` | Network issues or DDG rate limiting. | Wait a moment or check your internet connection. |
| `Crawl4AI not reachable` | The Crawl4AI Docker container isn't running. | Run `make crawl-up` in the repository root. |
| `mapscrape: playwright sidecar unavailable` | The optional Maps sidecar isn't running. | Run `make maps-up` — or ignore it if you are not using the Maps fallback. |
| `mapscrape: google served a consent or block page` | Google answered with a consent wall or captcha rather than results. | Not a code failure and not something GOAT works around. Try again later, or use the Places API path. |
| `claude CLI unavailable` | `claude` isn't on `$PATH`, or isn't logged in. | Run `claude login`, and confirm `which claude` resolves. |
| `mcp: response not refined` | Internal pipeline failure. | Re-run `diagnostics`; check the `claude` CLI isn't rate-limited or out of quota. |
