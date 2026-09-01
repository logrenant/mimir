.PHONY: build test race vet lint check e2e crawl-up crawl-down crawl-logs maps-up maps-down maps-logs desktop-sidecar desktop-dev desktop-build desktop-check

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
