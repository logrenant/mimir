package crawl

import (
	"context"
	"net/url"
	"sync"
	"time"
)

var (
	politeMu    sync.Mutex
	politeHosts = make(map[string]time.Time)
)

// waitPolite blocks until it's safe to hit the given URL's host again according to c.cfg.PerHostMinInterval.
// Uses a global map across the process for the crawl package to prevent hammering hosts.
func (c *Client) waitPolite(ctx context.Context, targetURL string) error {
	if c.cfg.PerHostMinInterval <= 0 {
		return nil
	}

	u, err := url.Parse(targetURL)
	if err != nil {
		return nil // if it can't parse, just don't delay
	}
	host := u.Hostname()
	if host == "" {
		return nil
	}

	politeMu.Lock()
	lastHit, ok := politeHosts[host]
	now := time.Now()

	var delay time.Duration
	if ok {
		elapsed := now.Sub(lastHit)
		if elapsed < c.cfg.PerHostMinInterval {
			delay = c.cfg.PerHostMinInterval - elapsed
		}
	}
	// Update the last hit to the projected time we'll hit it
	politeHosts[host] = now.Add(delay)
	politeMu.Unlock()

	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			// Waited politely
		}
	}

	return nil
}
