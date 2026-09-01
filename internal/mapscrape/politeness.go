package mapscrape

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

// waitPolite blocks until it is safe to hit targetURL's host again.
//
// A process-wide map, like internal/crawl's, because politeness is a property
// of the host being visited, not of one client value: two Clients must not each
// think they are the only visitor. The sidecar makes this doubly worth keeping
// — a browser fetches a page's images, fonts and tiles too, so one search here
// is far more requests than one HTTP GET.
func waitPolite(ctx context.Context, minInterval time.Duration, targetURL string) error {
	if minInterval <= 0 {
		return nil
	}

	u, err := url.Parse(targetURL)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	host := u.Hostname()

	politeMu.Lock()
	now := time.Now()
	var delay time.Duration
	if last, ok := politeHosts[host]; ok {
		if elapsed := now.Sub(last); elapsed < minInterval {
			delay = minInterval - elapsed
		}
	}
	// Record the projected hit time, not the current one, so concurrent callers
	// queue behind each other instead of all waiting the same interval and then
	// arriving together.
	politeHosts[host] = now.Add(delay)
	politeMu.Unlock()

	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
