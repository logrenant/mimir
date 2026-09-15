package settings

import (
	"os"
	"path/filepath"
	"testing"
)

// Default on, and "never asked" is a state of its own: an operator who installs
// Graphify because the tab suggested it should not then have to find a switch.
func TestStructural_DefaultsToOnAndUnconfigured(t *testing.T) {
	s := New(t.TempDir())

	got, configured, err := s.StructuralSettings()
	if err != nil {
		t.Fatal(err)
	}
	if configured {
		t.Error("configured = true before anything was saved")
	}
	if !got.Enabled {
		t.Error("the shipped default is on")
	}
}

// Off has to survive a read, for the same reason an empty root list does:
// seeding the default back over it would turn the pass on behind the operator.
func TestStructural_OffSurvivesARead(t *testing.T) {
	s := New(t.TempDir())

	if _, err := s.PutStructural(Structural{Enabled: false}); err != nil {
		t.Fatal(err)
	}

	got, configured, err := s.StructuralSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !configured {
		t.Error("configured = false after a save")
	}
	if got.Enabled {
		t.Error("the pass turned itself back on")
	}
	if s.EffectiveStructural().Enabled {
		t.Error("EffectiveStructural disagreed with what was stored")
	}
}

func TestStructural_RemembersTheInterpreter(t *testing.T) {
	s := New(t.TempDir())

	if _, err := s.PutStructural(Structural{Enabled: true, Python: "  /opt/homebrew/bin/python3.12  "}); err != nil {
		t.Fatal(err)
	}
	if got := s.EffectiveStructural().Python; got != "/opt/homebrew/bin/python3.12" {
		t.Errorf("python = %q, want it trimmed", got)
	}
}

// An interpreter is a program, not a command line: accepting one would let a
// setting file become a place to run arbitrary arguments.
func TestStructural_RefusesACommandLine(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.PutStructural(Structural{Enabled: true, Python: "python3 -c import os"}); err == nil {
		t.Fatal("want an error for an interpreter with arguments")
	}
}

// A file with a stray comma reads as not-configured rather than turning the
// feature into an error — the same failure direction the scan policy chose.
func TestStructural_UnreadableFileFallsBackToTheDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "structural.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(dir)

	got, configured, err := s.StructuralSettings()
	if err == nil {
		t.Error("the parse error was swallowed; the caller should be told")
	}
	if configured {
		t.Error("an unreadable file must not read as configured")
	}
	if !got.Enabled {
		t.Error("the fallback is the shipped default")
	}
}
