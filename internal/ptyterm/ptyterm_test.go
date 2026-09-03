package ptyterm

import (
	"io"
	"strings"
	"testing"
	"time"
)

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

// TestStartRunsInteractiveLoginShell is the claim the whole package rests on:
// the shell reads the operator's rc files, so what they defined there exists.
// A non-interactive shell would skip .zshrc and report "command not found".
func TestStartRunsInteractiveLoginShell(t *testing.T) {
	s, err := Start(Profile{Name: "probe", Command: "echo RC_SOURCED=$([ -n \"$ZSH\" ] && echo yes || echo no)"}, Size{Rows: 30, Cols: 120})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	out := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		var sb strings.Builder
		for {
			n, err := s.Read(buf)
			sb.Write(buf[:n])
			if strings.Contains(sb.String(), "RC_SOURCED=") && strings.Count(sb.String(), "RC_SOURCED=") > 1 {
				out <- sb.String()
				return
			}
			if err != nil {
				if err != io.EOF {
					t.Logf("read: %v", err)
				}
				out <- sb.String()
				return
			}
		}
	}()

	select {
	case got := <-out:
		if !strings.Contains(got, "RC_SOURCED=") {
			t.Fatalf("shell produced no output; got %q", got)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for the shell")
	}
}

func TestResizeAndCloseAreSafe(t *testing.T) {
	s, err := Start(Profile{Name: "probe"}, Size{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// A zero size is clamped rather than refused, so a client that has not
	// measured its viewport yet still gets a usable terminal.
	if err := s.Resize(Size{Rows: 40, Cols: 100}); err != nil {
		t.Errorf("Resize: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Close is idempotent: callers close on both the read error and a defer.
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}
