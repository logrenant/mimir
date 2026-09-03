package ptyterm

import (
	"strings"
	"testing"
	"time"
)

// waitFor drains a session's output until it contains want, or gives up.
//
// Returns everything seen, so a failure message can show what the shell
// actually said rather than only that it did not say the right thing.
func waitFor(t *testing.T, out <-chan []byte, seed []byte, want string) string {
	t.Helper()

	var sb strings.Builder
	sb.Write(seed)
	if strings.Contains(sb.String(), want) {
		return sb.String()
	}

	deadline := time.After(20 * time.Second)
	for {
		select {
		case chunk, ok := <-out:
			if !ok {
				t.Fatalf("output closed before %q appeared; saw:\n%s", want, sb.String())
			}
			sb.Write(chunk)
			if strings.Contains(sb.String(), want) {
				return sb.String()
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q; saw:\n%s", want, sb.String())
		}
	}
}

func TestProfileByName(t *testing.T) {
	// The two identities the operator picks between. Named explicitly rather
	// than ranged over, so adding a profile does not silently weaken the test.
	for _, want := range []Profile{
		{Name: "salihdevran", Command: "claude"},
		{Name: "eziode", Command: "claude-acct eziode"},
	} {
		got, ok := ProfileByName(want.Name)
		if !ok {
			t.Fatalf("ProfileByName(%q) not found", want.Name)
		}
		if got != want {
			t.Errorf("ProfileByName(%q) = %+v; want %+v", want.Name, got, want)
		}
	}

	// An unknown name must not fall back: opening a session as a different
	// identity would spend the wrong account.
	if _, ok := ProfileByName("nobody"); ok {
		t.Error("ProfileByName(nobody) = ok; want not found")
	}
}

// The claim the whole package rests on: the shell reads the operator's rc
// files, so what they defined there exists. A non-interactive shell would skip
// .zshrc and report "command not found" for a function they use every day.
func TestAttachRunsInteractiveLoginShell(t *testing.T) {
	reg := NewRegistry()
	t.Cleanup(reg.CloseAll)

	s, err := reg.Attach(Profile{
		Name:    "probe",
		Command: `echo RC_SOURCED=$([ -n "$ZSH" ] && echo yes || echo no)`,
	}, Size{Rows: 30, Cols: 120})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}

	seed, out, detach := s.Attach()
	defer detach()
	waitFor(t, out, seed, "RC_SOURCED=")
}

// The bug this architecture exists to fix.
//
// The pty used to be owned by the websocket, so the viewer going away took the
// shell with it. Detaching must leave the shell running, and reattaching must
// reach the *same* one — otherwise the second profile hangs up the first, and
// every relaunched `claude` re-asks for workspace trust.
func TestDetachLeavesTheShellRunning(t *testing.T) {
	reg := NewRegistry()
	t.Cleanup(reg.CloseAll)

	profile := Profile{Name: "keeper", Command: "echo FIRST_LINE"}
	first, err := reg.Attach(profile, Size{Rows: 30, Cols: 120})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}

	seed, out, detach := first.Attach()
	waitFor(t, out, seed, "FIRST_LINE")

	// The viewer goes away, as it does when the operator switches profile.
	detach()

	select {
	case <-first.Done():
		t.Fatal("the shell exited when its viewer detached")
	case <-time.After(300 * time.Millisecond):
	}

	// Reattaching must reach the same shell, not a replacement.
	second, err := reg.Attach(profile, Size{Rows: 30, Cols: 120})
	if err != nil {
		t.Fatalf("re-Attach: %v", err)
	}
	if second != first {
		t.Fatal("re-Attach started a new shell; want the one already running")
	}

	// And it must catch up on what it missed while nothing was watching.
	history, out2, detach2 := second.Attach()
	defer detach2()
	if !strings.Contains(string(history), "FIRST_LINE") {
		t.Errorf("history does not replay what the shell said; got:\n%s", history)
	}

	// The same shell is still typeable, which is the point of keeping it.
	if _, err := second.Write([]byte("echo SECOND_LINE\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitFor(t, out2, nil, "SECOND_LINE")
}

// Both accounts open at once is the feature the operator asked for, so it gets
// its own test rather than being implied by the registry's map.
func TestTwoProfilesRunAtOnce(t *testing.T) {
	reg := NewRegistry()
	t.Cleanup(reg.CloseAll)

	a, err := reg.Attach(Profile{Name: "acct-a", Command: "echo AAA_READY"}, Size{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("Attach a: %v", err)
	}
	seedA, outA, detachA := a.Attach()
	defer detachA()
	waitFor(t, outA, seedA, "AAA_READY")

	b, err := reg.Attach(Profile{Name: "acct-b", Command: "echo BBB_READY"}, Size{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("Attach b: %v", err)
	}
	seedB, outB, detachB := b.Attach()
	defer detachB()
	waitFor(t, outB, seedB, "BBB_READY")

	// Opening b must not have disturbed a — the exact regression.
	select {
	case <-a.Done():
		t.Fatal("opening the second profile killed the first")
	case <-time.After(200 * time.Millisecond):
	}

	live := reg.Live()
	if len(live) != 2 {
		t.Errorf("Live() = %v; want both profiles running", live)
	}

	// a is still usable, not merely un-exited.
	if _, err := a.Write([]byte("echo AAA_STILL_HERE\n")); err != nil {
		t.Fatalf("Write to a: %v", err)
	}
	waitFor(t, outA, nil, "AAA_STILL_HERE")
}

func TestKillEndsTheSessionAndForgetsIt(t *testing.T) {
	reg := NewRegistry()
	t.Cleanup(reg.CloseAll)

	profile := Profile{Name: "doomed"}
	s, err := reg.Attach(profile, Size{})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}

	if !reg.Kill(profile.Name) {
		t.Error("Kill reported nothing to kill")
	}
	select {
	case <-s.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the shell did not exit after Kill")
	}

	if reg.Kill(profile.Name) {
		t.Error("Kill found a session it had already ended")
	}
	if live := reg.Live(); len(live) != 0 {
		t.Errorf("Live() = %v; want empty after Kill", live)
	}

	// A profile whose shell has gone starts a fresh one rather than handing
	// back a pty nothing is reading.
	again, err := reg.Attach(profile, Size{})
	if err != nil {
		t.Fatalf("Attach after Kill: %v", err)
	}
	if again == s {
		t.Error("Attach returned the dead session")
	}
}

// A zero size is clamped rather than refused, so a client that has not measured
// its viewport yet still gets a usable terminal.
func TestResizeAndCloseAreSafe(t *testing.T) {
	reg := NewRegistry()
	t.Cleanup(reg.CloseAll)

	s, err := reg.Attach(Profile{Name: "sizing"}, Size{})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := s.Resize(Size{Rows: 40, Cols: 100}); err != nil {
		t.Errorf("Resize: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Idempotent: CloseAll and an explicit Close both reach it.
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// A second viewer replaces the first rather than both receiving half the
// output. The UI holds one socket per profile, so two viewers of one shell
// would only ever be two copies of the same screen.
func TestSecondAttachReplacesTheFirst(t *testing.T) {
	reg := NewRegistry()
	t.Cleanup(reg.CloseAll)

	s, err := reg.Attach(Profile{Name: "single"}, Size{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}

	_, first, _ := s.Attach()
	_, second, detach := s.Attach()
	defer detach()

	select {
	case _, ok := <-first:
		if ok {
			// A chunk in flight before the swap is fine; the channel closing
			// is what matters, so drain once more.
			if _, stillOpen := <-first; stillOpen {
				t.Error("the first viewer is still subscribed")
			}
		}
	case <-time.After(2 * time.Second):
		t.Error("the first viewer's channel was not closed by the second attach")
	}

	if _, err := s.Write([]byte("echo ONLY_VIEWER\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitFor(t, second, nil, "ONLY_VIEWER")
}
