package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Outreach email status. 'draft' is refreshable on a region re-run; 'sent' and
// 'skipped' are human decisions and are left alone.
const (
	EmailStatusDraft   = "draft"
	EmailStatusSent    = "sent"
	EmailStatusSkipped = "skipped"
)

// ErrEmailStatusInvalid is returned by SetOutreachEmailStatus for a status
// outside the closed set above.
var ErrEmailStatusInvalid = errors.New("store: outreach email status must be draft, sent, or skipped")

// ErrEmailNotFound is returned by SetOutreachEmailStatus when there is no draft
// row for the given (place_id, prompt_version) to act on.
var ErrEmailNotFound = errors.New("store: no outreach email for that place_id and prompt version")

// OutreachEmail is one cached draft plus the human's decision on it.
type OutreachEmail struct {
	Email     string
	Status    string
	Truncated bool
}

func validEmailStatus(s string) bool {
	switch s {
	case EmailStatusDraft, EmailStatusSent, EmailStatusSkipped:
		return true
	default:
		return false
	}
}

// GetOutreachEmail returns the cached draft for (placeID, promptVersion), or
// ok=false on a miss. Nil-Store tolerant (SD-6).
func (s *Store) GetOutreachEmail(ctx context.Context, placeID, promptVersion string) (OutreachEmail, bool, error) {
	if s == nil || s.db == nil || placeID == "" || promptVersion == "" {
		return OutreachEmail{}, false, nil
	}

	var (
		oe        OutreachEmail
		truncated int
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT email, status, truncated
		FROM outreach_emails
		WHERE place_id = ? AND prompt_version = ?`,
		placeID, promptVersion,
	).Scan(&oe.Email, &oe.Status, &truncated)
	if errors.Is(err, sql.ErrNoRows) {
		return OutreachEmail{}, false, nil
	}
	if err != nil {
		return OutreachEmail{}, false, unavailable(err)
	}
	oe.Truncated = truncated != 0
	return oe, true, nil
}

// PutOutreachEmail stores a freshly generated draft. It writes status 'draft'
// on insert and, on conflict, replaces the body **only if the existing row is
// still a draft** — a 'sent' or 'skipped' row is a human decision and a region
// re-run must not overwrite it. That guard lives in the SQL, not just the
// caller, so a concurrent write cannot slip past it.
//
// An empty email is dropped, like PutRefined drops an unrefined output.
func (s *Store) PutOutreachEmail(ctx context.Context, placeID, promptVersion, email string, truncated bool) error {
	if s == nil || s.db == nil || placeID == "" || promptVersion == "" {
		return nil
	}
	if email == "" {
		return nil
	}

	tflag := 0
	if truncated {
		tflag = 1
	}
	now := time.Now().Unix()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO outreach_emails
			(place_id, prompt_version, email, status, truncated, created_at, updated_at)
		VALUES (?, ?, ?, 'draft', ?, ?, ?)
		ON CONFLICT(place_id, prompt_version) DO UPDATE SET
			email      = excluded.email,
			truncated  = excluded.truncated,
			updated_at = excluded.updated_at
		WHERE outreach_emails.status = 'draft'`,
		placeID, promptVersion, email, tflag, now, now)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

// SetOutreachEmailStatus records a human's decision on a draft. The row must
// already exist — there is no email to send or skip otherwise — and the status
// must be one of the closed set.
func (s *Store) SetOutreachEmailStatus(ctx context.Context, placeID, promptVersion, status string) error {
	if !validEmailStatus(status) {
		return ErrEmailStatusInvalid
	}
	if s == nil || s.db == nil || placeID == "" || promptVersion == "" {
		return nil
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE outreach_emails
		SET status = ?, updated_at = ?
		WHERE place_id = ? AND prompt_version = ?`,
		status, time.Now().Unix(), placeID, promptVersion)
	if err != nil {
		return unavailable(err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrEmailNotFound
	}
	return nil
}
