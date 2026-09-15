package llm

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// The subprocess dance, written once.
//
// This package exists because there were two hand-written copies of it — one in
// `internal/refine` and one in the first Brain layer — and they had already
// drifted: the second re-invented the exec, the flags, the retry loop and the
// JSON envelope, and got the parsing wrong in a way that silently produced
// nodes with no tags at all.
//
// By the third provider that same loop was written three times *inside* this
// package, which is the same mistake with a shorter blast radius. So the loop
// is here and the providers keep only what genuinely differs: their argv, their
// environment, and the envelope they decode. Those are not shared because they
// are not the same thing — three CLIs' JSON output shapes are three shapes, and
// flattening them into a table would be inventing a commonality that is not
// there.

// cliRun is one subprocess invocation.
type cliRun struct {
	path string
	args []string
	// env replaces the process environment when non-nil. Nil inherits, which is
	// what a CLI riding an ambient login needs.
	env []string
	// dir is the working directory. Empty inherits. Every provider in this
	// package sets it to an empty scratch dir rather than a repository: the
	// subprocess's entire input is untrusted text, and these CLIs read their
	// own instruction files from wherever they start.
	dir string
	// stdin carries the content. Never argv: page bodies and node contents run
	// past what an argument list can hold (ARG_MAX is 1 MiB).
	stdin   string
	timeout time.Duration
}

// runCLI runs the command, once, and again if the first attempt failed.
//
// Two attempts rather than a retry policy: a CLI that failed to *start* is
// worth one more try, and a CLI that answered badly is not something a second
// identical call is a remedy for. The caller decides what a non-zero exit
// means — for `claude` it is often not the end of the story, because the reason
// is on stdout.
//
// A cancelled or expired caller context is returned as itself and never as a
// provider failure: the caller went away, and starting a second subprocess on
// their behalf is work nobody is waiting for. That distinction is what keeps a
// cancellation from triggering the router's fallback.
func runCLI(ctx context.Context, run cliRun) (stdout, stderr []byte, err error) {
	var out, errBuf bytes.Buffer

	for attempt := 1; attempt <= 2; attempt++ {
		out.Reset()
		errBuf.Reset()

		runCtx, cancel := context.WithTimeout(ctx, run.timeout)
		cmd := exec.CommandContext(runCtx, run.path, run.args...)
		if run.env != nil {
			cmd.Env = run.env
		}
		if run.dir != "" {
			cmd.Dir = run.dir
		}
		cmd.Stdin = strings.NewReader(run.stdin)
		cmd.Stdout = &out
		cmd.Stderr = &errBuf

		err = cmd.Run()
		cancel()

		if err == nil {
			break
		}
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, nil, ctx.Err()
		}
		if attempt < 2 {
			time.Sleep(retryPause)
			continue
		}
	}

	return out.Bytes(), errBuf.Bytes(), err
}

// retryPause is long enough to be past a transient spawn failure and short
// enough that a caller with a deadline does not notice it.
const retryPause = 100 * time.Millisecond
