package memory

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/logrenant/goat-mcp/internal/refine"
	"github.com/logrenant/goat-mcp/internal/sessionlog"
	"github.com/logrenant/goat-mcp/internal/store"
)

// maxRunsPerIngest bounds how far back into this daemon's own coding runs one
// pass looks. Runs are already summarized by their own row; the memory wants
// the recent ones, not the archive.
const maxRunsPerIngest = 50

// IngestStats is what one pass did. It is returned rather than logged so the
// daemon's backfill loop can tell "caught up" from "more to do" without
// guessing.
type IngestStats struct {
	Sources     int `json:"sources"`
	Episodes    int `json:"episodes"`
	Recapped    int `json:"recapped"`
	Rejected    int `json:"rejected"`
	Remaining   int `json:"remaining"`
	Unavailable int `json:"unavailable,omitempty"`
}

// Ingest brings a project's memory up to date, in two phases.
//
// Phase 1 parses whatever is new in the transcripts and writes the
// deterministic half of every episode. It costs nothing but I/O and it always
// runs to completion, which is why search and the timeline work from the first
// pass — before a single model call has been paid for.
//
// Phase 2 spends maxRecaps model calls on the highest-signal episodes that
// still lack a summary. It is bounded so a first run over months of history
// becomes a series of cheap passes rather than one long, expensive, easily
// interrupted one.
//
// A failure in either phase is reported, not fatal: a memory that ingested most
// of a project is worth more than one that refused to ingest any of it.
func (m *Memory) Ingest(ctx context.Context, p Project, maxRecaps int) (IngestStats, error) {
	var stats IngestStats
	if p.Path == "" {
		return stats, errors.New("memory: ingest needs a resolved project path")
	}

	sources := m.discover(ctx, p)
	stats.Sources = len(sources)

	for _, src := range sources {
		n, err := m.ingestSource(ctx, p, src)
		stats.Episodes += n
		if err != nil {
			stats.Unavailable++
		}
		if ctx.Err() != nil {
			return stats, ctx.Err()
		}
	}

	recapped, rejected, err := m.recapPending(ctx, p, maxRecaps)
	stats.Recapped, stats.Rejected = recapped, rejected
	if err != nil {
		return stats, err
	}

	remaining, err := m.store.CountPendingRecap(ctx, p.Path,
		m.cfg.MemoryPromptVersion, m.cfg.MemoryRecapMaxAttempts)
	if err != nil {
		return stats, err
	}
	stats.Remaining = remaining

	return stats, nil
}

// source is one transcript file waiting to be read.
type source struct {
	sessionlog.Source
	size    int64
	modTime time.Time
	run     sessionlog.RunMeta
}

// discover finds every transcript that belongs to this project.
//
// Nothing here fails the pass: a directory that cannot be listed, a file that
// cannot be statted, a home directory that does not exist — each costs the
// memory one source. There is no configuration in which having fewer
// transcripts is worth refusing to remember the rest.
func (m *Memory) discover(ctx context.Context, p Project) []source {
	var out []source
	out = append(out, m.discoverClaudeCode(p)...)
	out = append(out, m.discoverRuns(ctx, p)...)
	return out
}

// discoverClaudeCode locates the interactive sessions for this project.
//
// Claude Code names a project's directory by mangling its path, but that
// mangling is an undocumented internal detail. So the directory name is only a
// hint that narrows the search, and the actual test is the `cwd` each
// transcript records — which is data, not a naming convention, and cannot
// silently change meaning.
func (m *Memory) discoverClaudeCode(p Project) []source {
	root := m.cfg.ClaudeProjectsDir
	if root == "" {
		return nil
	}

	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	hint := slugHint(p.Path)
	var out []source
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		// The hint is a cheap filter, not the decision: a directory that does
		// not look like this project is skipped without opening anything, and
		// a directory that does still has to prove it by its cwd.
		if d.Name() != hint {
			continue
		}
		files, err := filepath.Glob(filepath.Join(root, d.Name(), "*.jsonl"))
		if err != nil {
			continue
		}
		for _, f := range files {
			st, err := os.Stat(f)
			if err != nil || st.Size() == 0 {
				continue
			}
			if !transcriptBelongsTo(f, p.Path) {
				continue
			}
			out = append(out, source{
				Source:  sessionlog.Source{Kind: sessionlog.SourceClaudeCode, Path: f, ProjectPath: p.Path},
				size:    st.Size(),
				modTime: st.ModTime(),
			})
		}
	}
	return out
}

// slugHint reproduces Claude Code's directory naming well enough to narrow a
// listing. Being wrong here costs a scan, never correctness, because
// transcriptBelongsTo has the final say.
func slugHint(projectPath string) string {
	return strings.ReplaceAll(strings.TrimSuffix(projectPath, "/"), "/", "-")
}

// transcriptBelongsTo reads only as far as the first record carrying a cwd.
func transcriptBelongsTo(path, projectPath string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	// A transcript's first records are small headers; the cwd appears on the
	// first real message. This is a bounded read, not a scan of the file.
	buf := make([]byte, 64*1024)
	n, err := f.Read(buf)
	if n == 0 && err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(buf[:n]), "\n") {
		var rec struct {
			CWD string `json:"cwd"`
		}
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec.CWD != "" {
			return rec.CWD == projectPath
		}
	}
	return false
}

// discoverRuns locates this daemon's own coding-run transcripts. Absent a run
// source — which is the normal case for goat-mcp, which has no daemon behind
// it — there simply are none.
func (m *Memory) discoverRuns(ctx context.Context, p Project) []source {
	if m.runs == nil || p.ID == "" {
		return nil
	}
	rows, err := m.runs.ListRunsByProject(ctx, p.ID, maxRunsPerIngest)
	if err != nil {
		return nil
	}

	var out []source
	for _, r := range rows {
		if r.TranscriptPath == "" || r.Status == store.RunStatusRunning {
			// A run still in flight will be read on a later pass, when its
			// transcript is complete and its cost is known.
			continue
		}
		st, err := os.Stat(r.TranscriptPath)
		if err != nil || st.Size() == 0 {
			continue
		}
		out = append(out, source{
			Source:  sessionlog.Source{Kind: sessionlog.SourceGoatRun, Path: r.TranscriptPath, ProjectPath: p.Path},
			size:    st.Size(),
			modTime: st.ModTime(),
			run: sessionlog.RunMeta{
				RunID: r.ID, ProjectPath: p.Path, Prompt: r.Prompt,
				SessionID: r.SessionID, Model: r.Model, CostUSD: r.CostUSD,
			},
		})
	}
	return out
}

// ingestSource reads whatever is new in one transcript and stores it.
func (m *Memory) ingestSource(ctx context.Context, p Project, src source) (int, error) {
	if src.Kind == sessionlog.SourceGoatRun {
		return m.ingestRun(ctx, src)
	}
	return m.ingestSession(ctx, p, src)
}

func (m *Memory) ingestSession(ctx context.Context, p Project, src source) (int, error) {
	st, _, err := m.store.GetIngestState(ctx, src.Path)
	if err != nil {
		return 0, err
	}

	offset := st.ByteOffset
	// A transcript that shrank was replaced, not appended to, and the stored
	// offset now points into the middle of a different file.
	if src.size < st.SizeSeen {
		offset = 0
	}
	if offset > src.size {
		offset = 0
	}
	if offset == src.size {
		return 0, nil
	}

	f, err := os.Open(src.Path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return 0, err
		}
	}

	episodes, resume, parseErr := sessionlog.ParseClaudeCode(f, src.Source, offset)

	// A transcript written to in the last few minutes is very likely the
	// session running right now. Its trailing episode is mid-flight, so it is
	// stored — the newest work is the most useful — but held back from the
	// recap queue until the file goes quiet, so a model call is not spent on
	// half a task.
	hot := time.Since(src.modTime) < m.cfg.MemoryOpenEpisodeGrace

	stored := 0
	for _, e := range episodes {
		if e.ProjectPath != "" && e.ProjectPath != p.Path {
			continue
		}
		e.ProjectPath = p.Path
		if err := m.store.PutEpisode(ctx, m.toRow(e, hot)); err != nil {
			return stored, err
		}
		stored++
	}

	if err := m.store.PutIngestState(ctx, store.IngestState{
		SourcePath: src.Path, ProjectPath: p.Path, ByteOffset: resume, SizeSeen: src.size,
	}); err != nil {
		return stored, err
	}
	return stored, parseErr
}

func (m *Memory) ingestRun(ctx context.Context, src source) (int, error) {
	st, seen, err := m.store.GetIngestState(ctx, src.Path)
	if err != nil {
		return 0, err
	}
	// A finished run's transcript never grows again, so size is a complete
	// freshness test and re-reading it would be pure waste.
	if seen && st.SizeSeen == src.size {
		return 0, nil
	}

	f, err := os.Open(src.Path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	e, err := sessionlog.ParseGoatRun(f, src.Source, src.run)
	if err != nil {
		return 0, err
	}
	if err := m.store.PutEpisode(ctx, m.toRow(e, false)); err != nil {
		return 0, err
	}

	return 1, m.store.PutIngestState(ctx, store.IngestState{
		SourcePath: src.Path, ProjectPath: e.ProjectPath, ByteOffset: src.size, SizeSeen: src.size,
	})
}

// recapPending spends up to maxRecaps model calls on the episodes that most
// deserve one.
//
// A rejected recap is recorded as an attempt and otherwise ignored. That is the
// whole failure posture of this package in one place: the episode keeps its
// facts, stays searchable, and simply reads less well than its neighbours.
// Nothing about a bad generation propagates.
func (m *Memory) recapPending(ctx context.Context, p Project, maxRecaps int) (recapped, rejected int, err error) {
	if maxRecaps <= 0 || m.refiner == nil {
		return 0, 0, nil
	}

	rows, err := m.store.PendingRecap(ctx, p.Path,
		m.cfg.MemoryPromptVersion, m.cfg.MemoryRecapMaxAttempts, maxRecaps)
	if err != nil || len(rows) == 0 {
		return 0, 0, err
	}

	type result struct {
		key            string
		title, summary string
		ok             bool
	}
	results := make([]result, len(rows))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(m.cfg.MemoryRecapConcurrency)
	for i, row := range rows {
		g.Go(func() error {
			out, err := m.refiner.Recap(gctx, refine.RecapInput{
				Facts:     episodeFacts(row),
				MaxTokens: m.cfg.MemoryRecapMaxTokens,
			})
			results[i].key = row.Key
			if err != nil {
				// Only a refusal is absorbed. A CLI that is missing or
				// unauthenticated is not this episode's fault, and burning the
				// whole queue's attempt budget on it would leave the memory
				// permanently blank once the CLI came back.
				if errors.Is(err, refine.ErrRefineRejected) {
					return nil
				}
				return err
			}
			title, summary := splitRecap(out.Text)
			results[i].title, results[i].summary, results[i].ok = title, summary, true
			return nil
		})
	}
	waitErr := g.Wait()

	for _, r := range results {
		if r.key == "" {
			continue
		}
		if !r.ok {
			// Recorded as an attempt even though it produced nothing, so a
			// hopeless episode stops costing a call on every future pass.
			if waitErr == nil {
				rejected++
				if err := m.store.UpdateRecap(ctx, r.key, "", "", m.cfg.MemoryPromptVersion); err != nil {
					return recapped, rejected, err
				}
			}
			continue
		}
		if err := m.store.UpdateRecap(ctx, r.key, r.title, r.summary, m.cfg.MemoryPromptVersion); err != nil {
			return recapped, rejected, err
		}
		recapped++
	}

	return recapped, rejected, waitErr
}

// splitRecap separates the title line from the bullets.
//
// The prompt asks for a title then bullets, but a prompt is a request, not a
// guarantee. A response that ignored the shape becomes a summary with no title
// rather than a parse error — there is nothing here worth failing over.
//
// Text that opens with a bullet is treated as all bullets. "- Retry work\n- one"
// and "- one\n- two" are genuinely indistinguishable, so the rule that never
// invents a title out of a bullet is the one that cannot mislead.
func splitRecap(text string) (title, summary string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	if strings.HasPrefix(text, "- ") || strings.HasPrefix(text, "* ") {
		return "", text
	}

	head, rest, found := strings.Cut(text, "\n")
	head = cleanTitle(head)
	if !found {
		return head, ""
	}
	return head, strings.TrimSpace(rest)
}

// cleanTitle strips the markdown decoration a model reaches for out of habit.
//
// The title is rendered as a field, not as markdown, so "**Sync all docs**" and
// "# Sync all docs" arrive at the consumer with their punctuation showing. The
// prompt already asks for a plain line; this is for when it does not get one.
func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "#")
	s = strings.TrimSpace(s)
	for _, fence := range []string{"**", "__", "*", "_", "`"} {
		if len(s) > 2*len(fence) && strings.HasPrefix(s, fence) && strings.HasSuffix(s, fence) {
			s = strings.TrimSpace(s[len(fence) : len(s)-len(fence)])
			break
		}
	}
	return strings.TrimSpace(strings.TrimSuffix(s, ":"))
}
