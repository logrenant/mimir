package coderunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrTranscriptUnavailable means the run exists but its transcript does not —
// deleted, or never opened because the run failed before the CLI was spawned.
// Distinct from ErrRunNotFound: one is "no such run", the other is "that run
// left no record", and a watcher reacts differently to each (SD-6).
var ErrTranscriptUnavailable = errors.New("coderunner: transcript unavailable")

// OpenTranscript returns the complete on-disk record of a run: one
// JSON-encoded events.Event per line, oldest first.
//
// The transcript, not the bus, is the complete record — bus delivery is
// best-effort by contract, and every event is written here before it is
// published. A watcher that joins late replays this and then follows the bus.
//
// The path layout stays knowledge of this package. Callers ask for a run id;
// they must not rebuild a filename out of config, or the two definitions of
// where a transcript lives will drift apart.
func (r *Runner) OpenTranscript(ctx context.Context, runID string) (io.ReadCloser, error) {
	row, found, err := r.runs.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	if row.TranscriptPath == "" {
		return nil, fmt.Errorf("%w: run %s has no transcript path recorded", ErrTranscriptUnavailable, runID)
	}

	f, err := os.Open(row.TranscriptPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTranscriptUnavailable, err)
	}
	return f, nil
}
