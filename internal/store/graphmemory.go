package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// The graph's memory of its own answers.
//
// Everything else about the knowledge base is derived from files: scan a
// repository twice and you get the same graph. This is the one part that is
// not — it is what the people asking questions learned, and it cannot be
// recomputed from anything on disk. So it is a record rather than a cache: no
// TTL, no delete-on-read, and a node that has since been forgotten keeps its
// history, because "we asked about this and it went nowhere" stays true.

// Graph result outcomes. Wire strings — the desktop and the MCP tools both
// send them.
const (
	// GraphUseful means the answer led somewhere.
	GraphUseful = "useful"
	// GraphDeadEnd means it did not. Worth recording precisely because it is
	// the outcome nobody volunteers.
	GraphDeadEnd = "dead_end"
	// GraphCorrected means the answer was wrong and the right one is known.
	GraphCorrected = "corrected"
)

// GraphResult is one recorded answer.
type GraphResult struct {
	ProjectPath string
	Question    string
	Nodes       []string
	Outcome     string
	Correction  string
	At          time.Time
}

// GraphLesson is one node's standing in the aggregate.
type GraphLesson struct {
	NodeID string `json:"node_id"`
	// Score is the half-life-weighted balance of useful against dead-end. It
	// is a ranking number and nothing else; two lessons from different weeks
	// are comparable only through it.
	Score float64 `json:"score"`
	// Useful and DeadEnd are the raw counts behind the score, because a score
	// with no counts under it is a number nobody can argue with.
	Useful  int `json:"useful"`
	DeadEnd int `json:"dead_end"`
	// Corrections are the corrections themselves, most recent first. They are
	// the point of the whole table.
	Corrections []string `json:"corrections,omitempty"`
}

// RecordGraphResult saves what one answer turned out to be worth.
func (s *Store) RecordGraphResult(ctx context.Context, r GraphResult) error {
	if s == nil || s.db == nil {
		return unavailable(errors.New("store not open"))
	}
	switch r.Outcome {
	case GraphUseful, GraphDeadEnd, GraphCorrected:
	default:
		return errors.New("store: unknown graph outcome: " + r.Outcome)
	}
	nodes, err := json.Marshal(nonNil(r.Nodes))
	if err != nil {
		return err
	}
	at := r.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO graph_results (project_path, question, nodes, outcome, correction, at)
		VALUES (?,?,?,?,?,?)`,
		r.ProjectPath, r.Question, string(nodes), r.Outcome, r.Correction, at.Unix()); err != nil {
		return unavailable(err)
	}
	return nil
}

// GraphLessons aggregates the recorded outcomes.
//
// Deterministic, and that is the whole design: this is graphify's reflect
// without the model call. Signal halves every halfLifeDays, so a node that
// answered well last year does not outrank one that answered well last week,
// and a node needs corroboration from more than one question before it counts
// as trustworthy — one enthusiastic session is not evidence.
func (s *Store) GraphLessons(ctx context.Context, projectPath string, halfLifeDays float64, minCorroboration int, limit int) ([]GraphLesson, error) {
	if s == nil || s.db == nil {
		return nil, unavailable(errors.New("store not open"))
	}
	if halfLifeDays <= 0 {
		halfLifeDays = 30
	}
	if minCorroboration <= 0 {
		minCorroboration = 2
	}
	if limit <= 0 {
		limit = 50
	}

	query := `SELECT nodes, outcome, correction, at FROM graph_results`
	args := []any{}
	if projectPath != "" {
		query += ` WHERE project_path = ?`
		args = append(args, projectPath)
	}
	query += ` ORDER BY at DESC LIMIT 2000`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	type acc struct {
		score       float64
		useful      int
		deadEnd     int
		corrections []string
	}
	byNode := map[string]*acc{}
	now := time.Now().UTC()

	for rows.Next() {
		var nodesJSON, outcome, correction string
		var at int64
		if err := rows.Scan(&nodesJSON, &outcome, &correction, &at); err != nil {
			return nil, unavailable(err)
		}
		var nodes []string
		if err := json.Unmarshal([]byte(nodesJSON), &nodes); err != nil {
			continue
		}

		ageDays := now.Sub(time.Unix(at, 0)).Hours() / 24
		weight := halfLife(ageDays, halfLifeDays)

		for _, id := range nodes {
			a, ok := byNode[id]
			if !ok {
				a = &acc{}
				byNode[id] = a
			}
			switch outcome {
			case GraphUseful:
				a.useful++
				a.score += weight
			case GraphDeadEnd:
				a.deadEnd++
				a.score -= weight
			case GraphCorrected:
				// A correction is not a vote either way — it is the answer
				// somebody bothered to write down, which is worth more than
				// either. Kept out of the score so a well-corrected node is
				// not punished for having been wrong once.
				if correction != "" {
					a.corrections = append(a.corrections, correction)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}

	out := make([]GraphLesson, 0, len(byNode))
	for id, a := range byNode {
		if a.useful+a.deadEnd < minCorroboration && len(a.corrections) == 0 {
			continue
		}
		out = append(out, GraphLesson{
			NodeID: id, Score: a.score,
			Useful: a.useful, DeadEnd: a.deadEnd, Corrections: a.corrections,
		})
	}
	sortLessons(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// halfLife is 2^(-age/halfLife): 1.0 today, 0.5 one half-life ago.
func halfLife(ageDays, halfLifeDays float64) float64 {
	if ageDays <= 0 {
		return 1
	}
	w := 1.0
	for n := ageDays / halfLifeDays; n >= 1; n-- {
		w /= 2
	}
	// The fractional remainder, linearly — close enough for a ranking number
	// and cheaper to read than an exponential.
	frac := ageDays/halfLifeDays - float64(int(ageDays/halfLifeDays))
	return w * (1 - frac/2)
}

func sortLessons(all []GraphLesson) {
	for i := 1; i < len(all); i++ {
		for j := i; j > 0; j-- {
			if all[j].Score > all[j-1].Score ||
				(all[j].Score == all[j-1].Score && all[j].NodeID < all[j-1].NodeID) {
				all[j], all[j-1] = all[j-1], all[j]
				continue
			}
			break
		}
	}
}
