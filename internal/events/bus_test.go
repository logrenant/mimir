package events

import (
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func ev(runID string, seq int64) Event {
	return Event{Kind: KindTextDelta, RunID: runID, Seq: seq, At: time.Now(), Text: "x"}
}

func recv(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatal("channel closed, want an event")
		}
		return e
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for an event")
		return Event{}
	}
}

func expectClosed(t *testing.T, ch <-chan Event) {
	t.Helper()
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // closed, as wanted
			}
			// Drain any buffered events before the close.
		case <-time.After(time.Second):
			t.Fatal("channel was not closed")
		}
	}
}

func TestPublish_FansOutToEverySubscriber(t *testing.T) {
	b := NewBus()
	defer b.Close()

	a, cancelA := b.Subscribe("run1")
	defer cancelA()
	c, cancelC := b.Subscribe("run1")
	defer cancelC()

	b.Publish(ev("run1", 1))

	if got := recv(t, a); got.Seq != 1 {
		t.Errorf("subscriber A: got seq %d, want 1", got.Seq)
	}
	if got := recv(t, c); got.Seq != 1 {
		t.Errorf("subscriber C: got seq %d, want 1", got.Seq)
	}
}

func TestPublish_IsScopedToItsRun(t *testing.T) {
	b := NewBus()
	defer b.Close()

	one, cancelOne := b.Subscribe("run1")
	defer cancelOne()
	two, cancelTwo := b.Subscribe("run2")
	defer cancelTwo()

	b.Publish(ev("run1", 1))

	if got := recv(t, one); got.RunID != "run1" {
		t.Errorf("got run %q, want run1", got.RunID)
	}
	select {
	case e := <-two:
		t.Fatalf("run2's subscriber received a run1 event: %+v", e)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPublish_NoSubscribersIsANoOp(t *testing.T) {
	b := NewBus()
	defer b.Close()
	b.Publish(ev("nobody-listening", 1)) // must not panic or block
}

// The publisher is the goroutine reading the claude subprocess. If a slow
// watcher could block it, a stalled UI would stall the run itself.
func TestPublish_SlowSubscriberDropsRatherThanBlocks(t *testing.T) {
	b := NewBus()
	defer b.Close()

	ch, cancel := b.Subscribe("run1")
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Publish well past the buffer without anyone reading.
		for i := 0; i < subBuffer*3; i++ {
			b.Publish(ev("run1", int64(i)))
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a subscriber that was not reading")
	}

	// The subscriber kept the events it could hold and lost the rest.
	if got := len(ch); got != subBuffer {
		t.Errorf("buffered events: got %d, want %d", got, subBuffer)
	}
}

func TestCancel_StopsDeliveryAndClosesChannel(t *testing.T) {
	b := NewBus()
	defer b.Close()

	ch, cancel := b.Subscribe("run1")
	cancel()

	expectClosed(t, ch)
	b.Publish(ev("run1", 1)) // must not panic on a closed subscriber
}

func TestCancel_IsIdempotent(t *testing.T) {
	b := NewBus()
	defer b.Close()

	_, cancel := b.Subscribe("run1")
	cancel()
	cancel()
	cancel() // a double close would panic
}

func TestCloseRun_ClosesEverySubscriberOfThatRun(t *testing.T) {
	b := NewBus()
	defer b.Close()

	a, cancelA := b.Subscribe("run1")
	defer cancelA()
	c, cancelC := b.Subscribe("run1")
	defer cancelC()
	other, cancelOther := b.Subscribe("run2")
	defer cancelOther()

	b.CloseRun("run1")

	expectClosed(t, a)
	expectClosed(t, c)

	// run2 is untouched.
	b.Publish(ev("run2", 7))
	if got := recv(t, other); got.Seq != 7 {
		t.Errorf("run2 subscriber: got seq %d, want 7", got.Seq)
	}
}

// CloseRun and cancel race to close the same channel; the closed flag under
// the mutex is what makes that safe.
func TestCloseRun_ThenCancelDoesNotDoubleClose(t *testing.T) {
	b := NewBus()
	defer b.Close()

	ch, cancel := b.Subscribe("run1")
	b.CloseRun("run1")
	cancel() // would panic if it closed again

	expectClosed(t, ch)
}

func TestClose_EndsEverythingAndRejectsPublish(t *testing.T) {
	b := NewBus()

	a, cancelA := b.Subscribe("run1")
	defer cancelA()
	c, cancelC := b.Subscribe("run2")
	defer cancelC()

	b.Close()
	b.Close() // idempotent

	expectClosed(t, a)
	expectClosed(t, c)

	b.Publish(ev("run1", 1)) // must not panic
}

// Subscribing after Close hands back a closed channel, so a caller ranging
// over it exits immediately instead of hanging forever.
func TestSubscribe_AfterCloseReturnsClosedChannel(t *testing.T) {
	b := NewBus()
	b.Close()

	ch, cancel := b.Subscribe("run1")
	defer cancel()
	expectClosed(t, ch)
}

func TestBus_ConcurrentPublishSubscribeCancel(t *testing.T) {
	b := NewBus()
	defer b.Close()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for seq := int64(0); ; seq++ {
				select {
				case <-stop:
					return
				default:
				}
				b.Publish(ev("run1", seq))
			}
		}()
	}

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ch, cancel := b.Subscribe("run1")
				<-time.After(time.Millisecond)
				cancel()
				//nolint:revive // draining is the point
				for range ch {
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
				b.CloseRun("run1")
			}
		}
	}()

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
}
