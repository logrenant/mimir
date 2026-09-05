package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Outreach draft status. 'draft' is refreshable on a region re-run; 'sent' and
// 'skipped' are human decisions and are left alone.
const (
	OutreachStatusDraft   = "draft"
	OutreachStatusSent    = "sent"
	OutreachStatusSkipped = "skipped"
)

// ErrOutreachStatusInvalid is returned by SetOutreachStatus for a status
// outside the closed set above.
var ErrOutreachStatusInvalid = errors.New("store: outreach status must be draft, sent, or skipped")

// ErrOutreachNotFound is returned by SetOutreachStatus when there is no draft
// row for the given (place_id, prompt_version) to act on.
var ErrOutreachNotFound = errors.New("store: no outreach draft for that place_id and prompt version")

// OutreachMessage is one cached draft plus the human's decision on it.
//
// The channel is carried out of the table rather than derived from the version
// string: a caller that reads a page of drafts should not have to parse a cache
// key to find out whether it is looking at an email or a WhatsApp line.
type OutreachMessage struct {
	Channel   string
	Body      string
	Status    string
	Truncated bool
}

func validOutreachStatus(s string) bool {
	switch s {
	case OutreachStatusDraft, OutreachStatusSent, OutreachStatusSkipped:
		return true
	default:
		return false
	}
}

// GetOutreachMessage returns the cached draft for (placeID, promptVersion), or
// ok=false on a miss. The channel is part of promptVersion — see
// internal/leadgen's MessageRunner.version — so it is not a parameter here.
// Nil-Store tolerant (SD-6).
func (s *Store) GetOutreachMessage(ctx context.Context, placeID, promptVersion string) (OutreachMessage, bool, error) {
	if s == nil || s.db == nil || placeID == "" || promptVersion == "" {
		return OutreachMessage{}, false, nil
	}

	var (
		om        OutreachMessage
		truncated int
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT channel, email, status, truncated
		FROM outreach_emails
		WHERE place_id = ? AND prompt_version = ?`,
		placeID, promptVersion,
	).Scan(&om.Channel, &om.Body, &om.Status, &truncated)
	if errors.Is(err, sql.ErrNoRows) {
		return OutreachMessage{}, false, nil
	}
	if err != nil {
		return OutreachMessage{}, false, unavailable(err)
	}
	om.Truncated = truncated != 0
	return om, true, nil
}

// PutOutreachMessage stores a freshly generated draft. It writes status 'draft'
// on insert and, on conflict, replaces the body **only if the existing row is
// still a draft** — a 'sent' or 'skipped' row is a human decision and a region
// re-run must not overwrite it. That guard lives in the SQL, not just the
// caller, so a concurrent write cannot slip past it.
//
// An empty body is dropped, like PutRefined drops an unrefined output.
func (s *Store) PutOutreachMessage(ctx context.Context, placeID, channel, promptVersion, body string, truncated bool) error {
	if s == nil || s.db == nil || placeID == "" || promptVersion == "" {
		return nil
	}
	if body == "" {
		return nil
	}
	if channel == "" {
		channel = "email"
	}

	tflag := 0
	if truncated {
		tflag = 1
	}
	now := time.Now().Unix()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO outreach_emails
			(place_id, prompt_version, channel, email, status, truncated, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'draft', ?, ?, ?)
		ON CONFLICT(place_id, prompt_version) DO UPDATE SET
			channel    = excluded.channel,
			email      = excluded.email,
			truncated  = excluded.truncated,
			updated_at = excluded.updated_at
		WHERE outreach_emails.status = 'draft'`,
		placeID, promptVersion, channel, body, tflag, now, now)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

// The two reads below split on a rule worth stating once, because getting it
// wrong is invisible:
//
//	**Writing is keyed by the exact version; reading is keyed by the channel.**
//
// A draft is *written* under the full prompt version — constant, model, channel,
// rule-file hash — so that changing any of them produces a fresh draft rather
// than replaying one the current prompt never wrote. But an operator reading
// their ledger wants the message they have, and computing its version would mean
// the screen guessing which model and which rule file it was drafted under.
// So display and status take the newest row per (place_id, channel) and never
// mention a version at all.
//
// The consequence is deliberate: editing a rule file leaves the old drafts
// visible and markable, and re-drafting replaces them. Nothing disappears from
// the screen because a setting changed.

// SetOutreachStatus records a human's decision on the newest draft a company has
// on one channel. The row must already exist — there is no message to send or
// skip otherwise — and the status must be one of the closed set.
func (s *Store) SetOutreachStatus(ctx context.Context, placeID, channel, status string) error {
	if !validOutreachStatus(status) {
		return ErrOutreachStatusInvalid
	}
	if s == nil || s.db == nil || placeID == "" {
		return nil
	}
	if channel == "" {
		channel = "email"
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE outreach_emails
		SET status = ?, updated_at = ?
		WHERE place_id = ? AND channel = ? AND prompt_version = (
			SELECT prompt_version FROM outreach_emails
			WHERE place_id = ? AND channel = ?
			ORDER BY updated_at DESC, prompt_version
			LIMIT 1)`,
		status, time.Now().Unix(), placeID, channel, placeID, channel)
	if err != nil {
		return unavailable(err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrOutreachNotFound
	}
	return nil
}

// OutreachMessagesFor resolves the drafts of many leads at once, so the ledger
// listing can show what has been written and decided without one query per row.
// Each company gets its newest draft per channel; ids with none are absent.
func (s *Store) OutreachMessagesFor(ctx context.Context, placeIDs []string) (map[string][]OutreachMessage, error) {
	if s == nil || s.db == nil || len(placeIDs) == 0 {
		return nil, nil
	}

	args := make([]any, 0, len(placeIDs))
	holders := make([]string, 0, len(placeIDs))
	for _, id := range placeIDs {
		if id == "" {
			continue
		}
		holders = append(holders, "?")
		args = append(args, id)
	}
	if len(holders) == 0 {
		return nil, nil
	}

	// Newest-first, then one row per (place_id, channel) taken on the way
	// through. A GROUP BY with a correlated MAX would say the same thing in more
	// SQL, and this loop is where the "newest per channel" rule is legible.
	rows, err := s.db.QueryContext(ctx, `
		SELECT place_id, channel, email, status, truncated
		FROM outreach_emails
		WHERE place_id IN (`+strings.Join(holders, ",")+`)
		ORDER BY updated_at DESC, prompt_version`, args...)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string][]OutreachMessage, len(holders))
	seen := make(map[string]bool, len(holders))
	for rows.Next() {
		var (
			id        string
			om        OutreachMessage
			truncated int
		)
		if err := rows.Scan(&id, &om.Channel, &om.Body, &om.Status, &truncated); err != nil {
			return nil, unavailable(err)
		}
		if seen[id+"\x00"+om.Channel] {
			continue
		}
		seen[id+"\x00"+om.Channel] = true
		om.Truncated = truncated != 0
		out[id] = append(out[id], om)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}
