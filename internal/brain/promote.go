package brain

import (
	"context"
	"encoding/json"
	"path"
	"sort"
	"strings"

	"github.com/logrenant/mimir/internal/store"
)

// EpisodeSource is the slice of the project memory this package promotes from.
// Narrow on purpose: Brain reads episodes, it never writes them.
type EpisodeSource interface {
	RecentEpisodes(ctx context.Context, projectPath string, limit int) ([]store.EpisodeRow, error)
}

// PromoteStats is what one promotion pass did.
type PromoteStats struct {
	Sessions int
	Files    int
	Edges    int
}

// Promote turns distilled memory episodes into nodes.
//
// # Why this makes no model call
//
// The episode already carries a title and a summary that M8 paid for. Deriving
// a second assessment would be paying twice for the same paragraph, and — more
// to the point — it would make running this over an existing backlog a bill
// rather than a migration. Tags come from the paths and commands the episode
// already recorded, which is real retrieval vocabulary for free. Aliases stay
// empty; a node that later wants them is re-distilled by bumping
// BrainPromptVersion.
//
// # Why there is no cursor
//
// The obvious design is to remember the last episode promoted and start after
// it. It is wrong here: a recap arrives *after* its episode row is stored, so a
// cursor that has moved past an episode would never come back for it once it
// finally got a summary. Instead this re-reads a bounded recent window every
// pass and upserts. Node identity is derived from the episode key, so a
// re-promotion updates one row rather than adding another, and an episode that
// was distilled late is picked up on the next tick.
func (c *Core) Promote(ctx context.Context, projectPath string, episodes EpisodeSource, limit int) (PromoteStats, error) {
	var stats PromoteStats
	if !c.Available() || episodes == nil || projectPath == "" {
		return stats, nil
	}
	if limit <= 0 {
		limit = c.cfg.BrainPromoteBatch
	}

	rows, err := episodes.RecentEpisodes(ctx, projectPath, limit)
	if err != nil {
		return stats, err
	}

	for _, e := range rows {
		if ctx.Err() != nil {
			return stats, ctx.Err()
		}
		// An episode with no recap has nothing a reader would want in a result
		// list. It stays in the memory, searchable there, and becomes a node if
		// and when it gets a summary.
		if strings.TrimSpace(e.Title) == "" || strings.TrimSpace(e.Summary) == "" {
			continue
		}

		f := parseEpisodeFacts(e.FactsJSON)

		// Tags come from the *relative* paths. An episode records absolute
		// ones, and deriving from those makes the first two segments the
		// machine's directory layout — `repo-internal` rather than
		// `internal-store` — which is a tag nothing will ever search for.
		rels := make([]string, 0, len(f.Files))
		for _, p := range f.Files {
			if rel := relativeTo(projectPath, p); rel != "" {
				rels = append(rels, rel)
			}
		}
		tags := deriveTags(rels)

		session := store.BrainNodeRow{
			ID:            NodeID(projectPath, KindSession, e.Key),
			ProjectPath:   projectPath,
			Kind:          KindSession,
			SourceKey:     e.Key,
			Title:         truncateRunes(e.Title, maxTitleRunes),
			Assessment:    e.Summary,
			Body:          sanitizeBody(episodeBody(f), c.cfg.BrainBodyMaxChars),
			Tags:          tags,
			Provider:      "memory",
			Model:         e.PromptVersion,
			PromptVersion: c.cfg.BrainPromptVersion,
			CreatedAt:     e.StartedAt,
		}
		if err := c.store.UpsertBrainNode(ctx, session); err != nil {
			return stats, err
		}
		stats.Sessions++

		edges := make([]store.BrainEdgeRow, 0, len(rels))
		for _, rel := range rels {
			file := store.BrainNodeRow{
				ID:            NodeID(projectPath, KindFile, rel),
				ProjectPath:   projectPath,
				Kind:          KindFile,
				SourceKey:     rel,
				Title:         rel,
				Tags:          deriveTags([]string{rel}),
				PromptVersion: c.cfg.BrainPromptVersion,
				CreatedAt:     e.StartedAt,
			}
			// A file node is only ever created, never overwritten with less
			// than it has: a scan (task-47) gives it a real assessment, and a
			// session promotion must not wipe that out.
			if existing, ok, err := c.store.BrainNode(ctx, file.ID); err == nil && ok {
				file.Title = existing.Title
				file.Assessment = existing.Assessment
				file.Body = existing.Body
				file.ContentHash = existing.ContentHash
				file.Provider, file.Model = existing.Provider, existing.Model
				file.PromptVersion = existing.PromptVersion
				file.CreatedAt = existing.CreatedAt
				file.Tags = mergeTerms(existing.Tags, file.Tags)
				file.Aliases = existing.Aliases
			}
			if err := c.store.UpsertBrainNode(ctx, file); err != nil {
				return stats, err
			}
			stats.Files++

			edges = append(edges, store.BrainEdgeRow{
				Src: session.ID, Dst: file.ID, Kind: "provenance", Weight: 1,
			})
		}
		if len(edges) > 0 {
			if err := c.store.UpsertBrainEdges(ctx, edges); err != nil {
				return stats, err
			}
			stats.Edges += len(edges)
		}
	}
	return stats, nil
}

// episodeFacts mirrors the JSON internal/memory stores. It is duplicated rather
// than imported because importing would make Brain a consumer of memory's
// internal shape, and this reads three fields of it.
type episodeFacts struct {
	Files    []string `json:"files"`
	Commands []string `json:"commands"`
	Failed   []string `json:"failed"`
	Prompt   string   `json:"prompt"`
	Outcome  string   `json:"outcome"`
}

func parseEpisodeFacts(raw string) episodeFacts {
	var f episodeFacts
	// A facts blob that will not parse costs this episode its tags and its file
	// links, not its node.
	_ = json.Unmarshal([]byte(raw), &f)
	return f
}

func episodeBody(f episodeFacts) string {
	var b strings.Builder
	if f.Prompt != "" {
		b.WriteString("Asked: ")
		b.WriteString(f.Prompt)
		b.WriteString("\n\n")
	}
	if f.Outcome != "" {
		b.WriteString("Outcome: ")
		b.WriteString(f.Outcome)
		b.WriteString("\n\n")
	}
	if len(f.Files) > 0 {
		b.WriteString("Files: ")
		b.WriteString(strings.Join(f.Files, ", "))
		b.WriteString("\n")
	}
	if len(f.Commands) > 0 {
		b.WriteString("Commands: ")
		b.WriteString(strings.Join(f.Commands, ", "))
		b.WriteString("\n")
	}
	if len(f.Failed) > 0 {
		b.WriteString("Failed: ")
		b.WriteString(strings.Join(f.Failed, ", "))
		b.WriteString("\n")
	}
	return b.String()
}

// relativeTo reduces an absolute path to one relative to the project, and
// rejects anything outside it.
//
// A node keyed by an absolute path would be a different node on a different
// machine, and the same file checked out twice would be two nodes.
func relativeTo(projectPath, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		return path.Clean(p)
	}
	prefix := strings.TrimSuffix(projectPath, "/") + "/"
	if !strings.HasPrefix(p, prefix) {
		return ""
	}
	return path.Clean(strings.TrimPrefix(p, prefix))
}

// deriveTags builds a retrieval vocabulary out of the paths a piece of work
// touched, with no model involved.
//
// The directory prefix is the useful half: `internal/brain/relate.go` yields
// `internal-brain`, which is what somebody actually searches for. The extension
// is the cheap half.
//
// Commands are deliberately *not* a source. internal/sessionlog stores them as
// human descriptions — "Check tasks dir and README", "Run full make check" —
// not command lines, so the first word of one is a verb. Tagging on those
// produced `check`, `find` and `read` on almost every session: terms that match
// everything, carry no signal, and would link every session to every other one
// through tag overlap. A tag that connects everything connects nothing.
func deriveTags(files []string) []string {
	seen := map[string]struct{}{}
	var out []string

	add := func(t string) {
		t = strings.ToLower(strings.TrimSpace(t))
		t = tagCleaner.ReplaceAllString(t, "-")
		t = versionJoin.ReplaceAllString(t, "$1$2")
		t = strings.Trim(t, "-")
		if t == "" || len([]rune(t)) < 2 || len([]rune(t)) > maxTagRunes {
			return
		}
		if _, dup := seen[t]; dup {
			return
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}

	for _, f := range files {
		dir := path.Dir(f)
		if dir != "." && dir != "/" && dir != "" {
			// Two levels is where the signal is: `internal/brain` says
			// something, `internal/brain/testdata/fixtures` says less than
			// `internal/brain` did.
			parts := strings.Split(strings.Trim(dir, "/"), "/")
			if len(parts) > 2 {
				parts = parts[:2]
			}
			add(strings.Join(parts, "-"))
		}
		if ext := strings.TrimPrefix(path.Ext(f), "."); ext != "" {
			add(ext)
		}
	}
	sort.Strings(out)
	if len(out) > maxTags {
		out = out[:maxTags]
	}
	return out
}

// mergeTerms unions two term lists without letting one grow without bound.
func mergeTerms(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, t := range list {
			if t == "" {
				continue
			}
			if _, dup := seen[t]; dup {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
			if len(out) == maxTags {
				return out
			}
		}
	}
	return out
}
