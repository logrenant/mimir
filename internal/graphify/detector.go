package graphify

import (
	"context"
	"sync"
	"time"
)

// Detector remembers what Detect found.
//
// A sweep runs over every project on the machine, and asking "is Graphify
// installed" once per project would spend a Python start-up per project to
// learn the same thing eighteen times. Cached with a short life rather than
// forever, because the answer changes while the daemon runs: the whole point of
// the Brain tab telling an operator to `pip install graphifyy` is that the next
// sweep picks it up, without a restart.
//
// The negative answer is cached too, and for the same span. A machine that will
// never have Graphify is the common case, and it must cost one failed PATH
// lookup every few minutes rather than one per project.
type Detector struct {
	ttl time.Duration

	mu     sync.Mutex
	at     time.Time
	python string
	info   Info
	found  bool
}

// DefaultDetectTTL is short enough that installing Graphify shows up within a
// sweep or two, and long enough that it is not a subprocess per project.
const DefaultDetectTTL = 3 * time.Minute

func NewDetector(ttl time.Duration) *Detector {
	if ttl <= 0 {
		ttl = DefaultDetectTTL
	}
	return &Detector{ttl: ttl}
}

// Look returns what is installed, re-checking when the cached answer has
// expired or when the interpreter being asked about has changed.
func (d *Detector) Look(ctx context.Context, python string) (Info, bool) {
	if d == nil {
		return Info{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	fresh := time.Since(d.at) < d.ttl && d.python == python && !d.at.IsZero()
	if fresh {
		return d.info, d.found
	}

	info, err := Detect(ctx, python)
	d.at = time.Now()
	d.python = python
	d.info = info
	d.found = err == nil
	if err != nil {
		d.info = Info{}
	}
	return d.info, d.found
}

// Forget drops the cached answer, so the next Look asks again. The screen that
// offers "check again" is what this is for.
func (d *Detector) Forget() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.at = time.Time{}
}
