package brain

import (
	"bytes"
	"context"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/store"
)

// Record and field separators for the git log format. ASCII 0x1e/0x1f exist for
// exactly this and cannot appear in a commit message, unlike any printable
// delimiter somebody would otherwise pick.
const (
	gitRecordSep = "\x1e"
	gitFieldSep  = "\x1f"

	// maxCommitFiles bounds a single commit's file list. A mass rename would
	// otherwise write hundreds of edges for one node and drown its real
	// neighbours.
	maxCommitFiles = 40
)

// CursorStore is the capture bookmark. Satisfied by *store.Store.
type CursorStore interface {
	BrainCursor(ctx context.Context, key string) (string, error)
	SetBrainCursor(ctx context.Context, key, projectPath, cursor string) error
}

// CaptureCommits turns new commits into nodes.
//
// This is what records terminal work. The alternative — a shell hook logging
// every command — was considered and deliberately not built: it is noisy, it
// captures secrets typed on a command line, and almost none of it is worth
// remembering a week later. A commit is the part somebody decided was worth
// keeping, and it already carries a message explaining why.
//
// No model call: the subject is the title, the body is the body, the tags come
// from the directories touched. A commit that deserves a real assessment gets
// one when a session about it is promoted and links to the same files.
func (c *Core) CaptureCommits(ctx context.Context, projectPath string, cursors CursorStore, limit int) (int, error) {
	if !c.Available() || cursors == nil || projectPath == "" {
		return 0, nil
	}
	if limit <= 0 {
		limit = c.cfg.BrainCommitBatch
	}

	key := "git:" + projectPath
	since, err := cursors.BrainCursor(ctx, key)
	if err != nil {
		return 0, err
	}

	commits, err := readCommits(ctx, projectPath, since, limit)
	if err != nil || len(commits) == 0 {
		// A directory that is not a repository, or a git that is not installed,
		// is not an error worth surfacing: it simply has no commits to capture.
		return 0, nil
	}

	stored := 0
	for _, cm := range commits {
		if ctx.Err() != nil {
			return stored, ctx.Err()
		}
		node := store.BrainNodeRow{
			ID:            NodeID(projectPath, KindCommit, cm.SHA),
			ProjectPath:   projectPath,
			Kind:          KindCommit,
			SourceKey:     cm.SHA,
			Title:         truncateRunes(cm.Subject, maxTitleRunes),
			Assessment:    strings.TrimSpace(cm.Body),
			Body:          sanitizeBody(commitBody(cm), c.cfg.BrainBodyMaxChars),
			Tags:          deriveTags(cm.Files),
			Provider:      "git",
			PromptVersion: c.cfg.BrainPromptVersion,
			CreatedAt:     cm.At,
		}
		if err := c.store.UpsertBrainNode(ctx, node); err != nil {
			return stored, err
		}

		edges := make([]store.BrainEdgeRow, 0, len(cm.Files))
		for _, f := range cm.Files {
			fileID := NodeID(projectPath, KindFile, f)
			// Only link to a file node that exists. Minting one here would fill
			// the graph with every path that ever appeared in history,
			// including the ones deleted years ago.
			if _, ok, err := c.store.BrainNode(ctx, fileID); err != nil || !ok {
				continue
			}
			edges = append(edges, store.BrainEdgeRow{
				Src: node.ID, Dst: fileID, Kind: "provenance", Weight: 1,
			})
		}
		if len(edges) > 0 {
			if err := c.store.UpsertBrainEdges(ctx, edges); err != nil {
				return stored, err
			}
		}
		stored++
	}

	// The cursor moves only after every commit in the batch is stored, so an
	// interrupted pass repeats work rather than skipping it.
	if err := cursors.SetBrainCursor(ctx, key, projectPath, commits[0].SHA); err != nil {
		return stored, err
	}
	return stored, nil
}

type commit struct {
	SHA     string
	At      time.Time
	Subject string
	Body    string
	Files   []string
}

// readCommits reads newest-first, stopping at `since`.
//
// `since..HEAD` is not used: a cursor pointing at a commit that no longer
// exists — a rebase, a reset, a branch that went away — makes git fail, and the
// fix would be a special case for every way history can be rewritten. Reading a
// bounded window and stopping when the known SHA appears degrades to
// "re-capture the window" instead, which is harmless because the upsert is
// idempotent.
func readCommits(ctx context.Context, projectPath, since string, limit int) ([]commit, error) {
	format := gitRecordSep + "%H" + gitFieldSep + "%aI" + gitFieldSep + "%s" + gitFieldSep + "%b" + gitFieldSep

	cmd := exec.CommandContext(ctx, "git",
		"-C", projectPath, "log", "--no-color", "--name-only",
		"--max-count="+strconv.Itoa(limit), "--pretty=format:"+format)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	var out []commit
	for _, block := range strings.Split(stdout.String(), gitRecordSep) {
		if strings.TrimSpace(block) == "" {
			continue
		}
		fields := strings.SplitN(block, gitFieldSep, 5)
		if len(fields) < 5 {
			continue
		}
		sha := strings.TrimSpace(fields[0])
		if sha == "" {
			continue
		}
		if sha == since {
			break
		}

		at, _ := time.Parse(time.RFC3339, strings.TrimSpace(fields[1]))
		var files []string
		for _, line := range strings.Split(fields[4], "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			files = append(files, path.Clean(line))
			if len(files) >= maxCommitFiles {
				break
			}
		}

		out = append(out, commit{
			SHA: sha, At: at.UTC(),
			Subject: strings.TrimSpace(fields[2]),
			Body:    strings.TrimSpace(fields[3]),
			Files:   files,
		})
	}
	return out, nil
}

func commitBody(cm commit) string {
	var b strings.Builder
	b.WriteString(cm.Subject)
	if cm.Body != "" {
		b.WriteString("\n\n")
		b.WriteString(cm.Body)
	}
	if len(cm.Files) > 0 {
		b.WriteString("\n\nFiles: ")
		b.WriteString(strings.Join(cm.Files, ", "))
	}
	return b.String()
}
