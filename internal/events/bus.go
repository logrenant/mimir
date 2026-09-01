package events

import "sync"

// subBuffer is how many events a subscriber may fall behind before it starts
// losing them. Deliberately a package constant, not a config knob: it is an
// implementation detail of the delivery mechanism, not an operational value
// anyone should tune (SD-1).
//
// Sized for a burst of text deltas during a long assistant turn — well past
// what a local WebSocket client should ever be behind by.
const subBuffer = 512

// Bus fans run events out to zero or more watchers, per run.
//
// Delivery is BEST-EFFORT and that is deliberate. The publisher is the
// goroutine reading the `claude` subprocess's stdout; if a slow watcher could
// block it, a stalled UI would stall the actual run. So a subscriber whose
// buffer is full loses events instead. The complete record is the on-disk
// JSONL transcript, and Event.Seq lets a watcher notice it missed something
// and go read it.
type Bus struct {
	mu     sync.Mutex
	subs   map[string]map[int64]*subscription
	nextID int64
	closed bool
}

type subscription struct {
	ch     chan Event
	closed bool // guards against a double close between Unsubscribe and CloseRun
}

func NewBus() *Bus {
	return &Bus{subs: map[string]map[int64]*subscription{}}
}

// Publish delivers ev to every current subscriber of ev.RunID. It never
// blocks: a subscriber that cannot keep up drops this event.
func (b *Bus) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	for _, sub := range b.subs[ev.RunID] {
		if sub.closed {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
			// Dropped. See the type comment: the transcript is the record.
		}
	}
}

// Subscribe returns a channel of events for runID and a function that stops
// the subscription. The cancel function is idempotent and safe to call after
// CloseRun or Close.
//
// The channel is closed when the subscription ends, by cancel, CloseRun, or
// Close — so a range over it terminates cleanly in all three cases.
func (b *Bus) Subscribe(runID string) (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	sub := &subscription{ch: make(chan Event, subBuffer)}

	if b.closed {
		// Hand back an already-closed channel rather than a live one that will
		// never receive: a caller ranging over it exits immediately.
		sub.closed = true
		close(sub.ch)
		return sub.ch, func() {}
	}

	id := b.nextID
	b.nextID++
	if b.subs[runID] == nil {
		b.subs[runID] = map[int64]*subscription{}
	}
	b.subs[runID][id] = sub

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if subs, ok := b.subs[runID]; ok {
				delete(subs, id)
				if len(subs) == 0 {
					delete(b.subs, runID)
				}
			}
			b.closeSubLocked(sub)
		})
	}
	return sub.ch, cancel
}

// CloseRun ends every subscription for runID. Call it once a run has emitted
// its terminal event so watchers see their channel close instead of hanging.
func (b *Bus) CloseRun(runID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, sub := range b.subs[runID] {
		b.closeSubLocked(sub)
	}
	delete(b.subs, runID)
}

// Close ends every subscription on the bus and rejects further publishes.
// Idempotent.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.closed = true
	for runID, subs := range b.subs {
		for _, sub := range subs {
			b.closeSubLocked(sub)
		}
		delete(b.subs, runID)
	}
}

// closeSubLocked closes a subscription's channel exactly once. Callers must
// hold b.mu — that is what makes the closed flag a sufficient guard against
// Unsubscribe and CloseRun racing to close the same channel.
func (b *Bus) closeSubLocked(sub *subscription) {
	if sub.closed {
		return
	}
	sub.closed = true
	close(sub.ch)
}
