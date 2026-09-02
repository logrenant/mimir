.PHONY: build test race vet lint check e2e scan scan-dry install-mcp uninstall-mcp install-agent uninstall-agent agent-status agent-logs agent-restart install-app crawl-up crawl-down crawl-logs maps-up maps-down maps-logs desktop-sidecar desktop-dev desktop-build desktop-check

build:
	go build -o bin/mimir-mcp ./cmd/mimir-mcp
	go build -o bin/mimir-daemon ./cmd/mimir-daemon
	go build -o bin/mimir-scan ./cmd/mimir-scan

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

lint:
	tools/lint/check_stdout.sh
	tools/lint/check_context.sh
	golangci-lint run
	sh -n scripts/install-agent.sh
	sh -n scripts/install-mcp.sh
	sh -n scripts/mimir-preflight.sh
	sh -n scripts/uninstall-agent.sh

e2e:
	@echo "Running E2E test harness..."
	go test -v ./test/e2e/...

check: build vet lint test race

ci: check e2e

# The version stamped into both binaries is the git tag, not a build date:
# /healthz reports it, and an operator comparing a running daemon against a
# GitHub release needs the two strings to be the same string.
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT)

release:
	@echo "Building release binaries ($(VERSION))..."
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/mimir-mcp-darwin-arm64 ./cmd/mimir-mcp
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/mimir-mcp-darwin-amd64 ./cmd/mimir-mcp
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/mimir-daemon-darwin-arm64 ./cmd/mimir-daemon
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/mimir-daemon-darwin-amd64 ./cmd/mimir-daemon
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/mimir-scan-darwin-arm64 ./cmd/mimir-scan
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/mimir-scan-darwin-amd64 ./cmd/mimir-scan
	@echo "Release binaries built in bin/"

# --- machine-wide scan -------------------------------------------------------
#
# Reads every project under ROOT into Brain: one gemini-3.7-flash-low call per
# file through agy, hash-skipped so a re-run is free. `scan-dry` costs nothing
# and reports how many files are pending.

ROOT ?= $(HOME)/development

scan: build
	bin/mimir-scan $(ROOT)

scan-dry: build
	bin/mimir-scan -n $(ROOT)

# --- always-on daemon (launchd) ---------------------------------------------
#
# Installs mimir-daemon as a per-user LaunchAgent so it runs at login and is
# restarted if it dies, whether or not the desktop app is open. The daemon's
# own contract is unchanged: launchd is simply a second legitimate parent that
# hands it MIMIR_DAEMON_PORT and MIMIR_DAEMON_TOKEN. See scripts/AGENTS.md.

AGENT_LABEL := studio.mimir.daemon
AGENT_DOMAIN := gui/$(shell id -u)

# Register mimir-mcp with every MCP client on this machine and install the
# session preflight. Separate from install-agent because they are separate
# things: this one is what a Claude Code / agy / gemini / VS Code session talks
# to, the other is the long-running daemon behind the desktop app.
install-mcp:
	scripts/install-mcp.sh

uninstall-mcp:
	scripts/install-mcp.sh --uninstall

install-agent:
	scripts/install-agent.sh

uninstall-agent:
	scripts/uninstall-agent.sh

agent-status:
	@launchctl print $(AGENT_DOMAIN)/$(AGENT_LABEL) | grep -E "state = |pid = |path = " || \
		echo "$(AGENT_LABEL) is not loaded — run 'make install-agent'"

agent-restart:
	launchctl kickstart -k $(AGENT_DOMAIN)/$(AGENT_LABEL)

agent-logs:
	tail -f "$(HOME)/Library/Logs/mimir-daemon.log"

# The menu-bar app itself. Copied rather than distributed: the bundle is
# ad-hoc-signed, and a .dmg needs Finder automation permission for its layout
# step (see desktop/AGENTS.md).
install-app: desktop-build
	rm -rf /Applications/Mimir.app
	cp -R desktop/src-tauri/target/release/bundle/macos/Mimir.app /Applications/
	@echo "Mimir.app installed — open it once; it lives in the menu bar, not the Dock"

clean:
	@echo "not implemented"
	@exit 0

crawl-up:
	docker compose -f deploy/crawl4ai/docker-compose.yml up -d

crawl-down:
	docker compose -f deploy/crawl4ai/docker-compose.yml down

crawl-logs:
	docker compose -f deploy/crawl4ai/docker-compose.yml logs -f

# The Maps scrape sidecar (deploy/playwright-maps) is the Places API fallback.
# Unlike Crawl4AI it is built locally, so the first `maps-up` pulls the pinned
# Playwright image and installs the matching library — expect a slow first run.
maps-up:
	docker compose -f deploy/playwright-maps/docker-compose.yml up -d --build

maps-down:
	docker compose -f deploy/playwright-maps/docker-compose.yml down

maps-logs:
	docker compose -f deploy/playwright-maps/docker-compose.yml logs -f

# --- desktop (Tauri shell) --------------------------------------------------
#
# `make check` deliberately does NOT run any of these: a Go contributor without
# Node or a Rust toolchain must still be able to run it. The desktop app has
# its own gate, `make desktop-check`.

# Tauri resolves a sidecar by target triple, so the binary has to be named for
# the host it will run on.
TAURI_TRIPLE := $(shell rustc -vV 2>/dev/null | awk '/^host:/{print $$2}')

desktop-sidecar:
	@test -n "$(TAURI_TRIPLE)" || { echo "rustc not found — install Rust (https://rustup.rs) so the sidecar can be named for its target triple"; exit 1; }
	mkdir -p desktop/src-tauri/binaries
	go build -o desktop/src-tauri/binaries/mimir-daemon-$(TAURI_TRIPLE) ./cmd/mimir-daemon

desktop-dev: desktop-sidecar
	cd desktop && npm run tauri dev

desktop-build: desktop-sidecar
	cd desktop && npm run tauri build

desktop-check: desktop-sidecar
	cd desktop && npm run typecheck && npm test
	cd desktop/src-tauri && cargo fmt --check && cargo clippy -- -D warnings && cargo test
