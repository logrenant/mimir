.PHONY: build test race vet lint check e2e install-agent uninstall-agent agent-status agent-logs agent-restart install-app crawl-up crawl-down crawl-logs maps-up maps-down maps-logs desktop-sidecar desktop-dev desktop-build desktop-check

build:
	go build -o bin/goat-mcp ./cmd/goat-mcp
	go build -o bin/goat-daemon ./cmd/goat-daemon

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
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/goat-mcp-darwin-arm64 ./cmd/goat-mcp
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/goat-mcp-darwin-amd64 ./cmd/goat-mcp
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/goat-daemon-darwin-arm64 ./cmd/goat-daemon
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/goat-daemon-darwin-amd64 ./cmd/goat-daemon
	@echo "Release binaries built in bin/"

# --- always-on daemon (launchd) ---------------------------------------------
#
# Installs goat-daemon as a per-user LaunchAgent so it runs at login and is
# restarted if it dies, whether or not the desktop app is open. The daemon's
# own contract is unchanged: launchd is simply a second legitimate parent that
# hands it GOAT_DAEMON_PORT and GOAT_DAEMON_TOKEN. See scripts/AGENTS.md.

AGENT_LABEL := com.goat.daemon
AGENT_DOMAIN := gui/$(shell id -u)

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
	tail -f "$(HOME)/Library/Logs/goat-daemon.log"

# The menu-bar app itself. Copied rather than distributed: the bundle is
# ad-hoc-signed, and a .dmg needs Finder automation permission for its layout
# step (see desktop/AGENTS.md).
install-app: desktop-build
	rm -rf /Applications/GOAT.app
	cp -R desktop/src-tauri/target/release/bundle/macos/GOAT.app /Applications/
	@echo "GOAT.app installed — open it once; it lives in the menu bar, not the Dock"

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
	go build -o desktop/src-tauri/binaries/goat-daemon-$(TAURI_TRIPLE) ./cmd/goat-daemon

desktop-dev: desktop-sidecar
	cd desktop && npm run tauri dev

desktop-build: desktop-sidecar
	cd desktop && npm run tauri build

desktop-check: desktop-sidecar
	cd desktop && npm run typecheck && npm test
	cd desktop/src-tauri && cargo fmt --check && cargo clippy -- -D warnings && cargo test
