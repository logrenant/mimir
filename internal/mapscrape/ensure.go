package mapscrape

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ErrDockerUnavailable means the sidecar is down and this process could not
// start it: no docker on PATH, or no compose file to start it from.
//
// Separate from ErrSidecarUnavailable because the fix is different — that one
// means "the container is not answering", this one means "and I could not do
// anything about it" (SD-6).
var ErrDockerUnavailable = errors.New("mapscrape: the playwright sidecar is not running and could not be started")

// readyPollInterval is how often EnsureRunning re-asks a starting container
// whether it is up. The container boots a browser image; seconds, not
// milliseconds, is the honest granularity.
const readyPollInterval = time.Second

// EnsureRunning brings the sidecar up if it is not already answering.
//
// Region search is the one capability that works with no credential at all, so
// "run `make maps-up` first" would put a terminal step in front of the only
// free path in the product. This is that step, taken by the daemon, once, at
// the moment the work needs it.
//
// It is idempotent by construction: `docker compose up -d` on a running
// service is a no-op, and the health probe short-circuits before docker is
// even consulted. A caller that is already healthy pays one loopback GET.
func (c *Client) EnsureRunning(ctx context.Context) error {
	if ok, _ := c.Health(ctx); ok {
		return nil
	}

	compose := c.cfg.MapScrapeComposeFile
	if compose == "" {
		return fmt.Errorf("%w: no compose file is configured", ErrDockerUnavailable)
	}
	if _, err := os.Stat(compose); err != nil {
		return fmt.Errorf("%w: %s is not readable (%v) — run `make maps-up` from the repository instead",
			ErrDockerUnavailable, compose, err)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("%w: docker is not on PATH", ErrDockerUnavailable)
	}

	startCtx, cancel := context.WithTimeout(ctx, c.cfg.MapScrapeStartTimeout)
	defer cancel()

	slog.Info("starting the maps sidecar", "compose", compose)
	// Detached, and the image is built here when it does not exist yet — which
	// is the first run on a fresh machine. That build is minutes, which is why
	// MapScrapeStartTimeout is generous and why this is logged.
	cmd := exec.CommandContext(startCtx, "docker", "compose", "-f", compose, "up", "-d")
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(out.String())
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("%w: `docker compose up -d` failed: %s", ErrDockerUnavailable, detail)
	}

	// Up is not ready: compose returns when the container is created, and the
	// browser image takes seconds more to serve /health.
	for {
		if ok, _ := c.Health(startCtx); ok {
			slog.Info("the maps sidecar is ready", "url", c.cfg.MapScrapeBaseURL)
			return nil
		}
		select {
		case <-startCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%w: it was started but did not answer within %s",
				ErrDockerUnavailable, c.cfg.MapScrapeStartTimeout)
		case <-time.After(readyPollInterval):
		}
	}
}
