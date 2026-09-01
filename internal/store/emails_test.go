package store

import (
	"context"
	"errors"
	"testing"
)

func TestOutreachEmailRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutOutreachEmail(ctx, "place-1", "email-v1", "hello there", true); err != nil {
		t.Fatalf("PutOutreachEmail: %v", err)
	}

	got, ok, err := s.GetOutreachEmail(ctx, "place-1", "email-v1")
	if err != nil || !ok {
		t.Fatalf("GetOutreachEmail: ok=%v err=%v", ok, err)
	}
	if got.Email != "hello there" || got.Status != EmailStatusDraft || !got.Truncated {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestOutreachEmailMissForEachKeyComponent(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutOutreachEmail(ctx, "place-1", "email-v1", "body", false); err != nil {
		t.Fatalf("PutOutreachEmail: %v", err)
	}

	if _, ok, _ := s.GetOutreachEmail(ctx, "place-2", "email-v1"); ok {
		t.Error("a different place_id must miss")
	}
	if _, ok, _ := s.GetOutreachEmail(ctx, "place-1", "email-v2"); ok {
		t.Error("a different prompt_version must miss")
	}
}

// A region re-run refreshes a draft but must never overwrite a sent email.
func TestOutreachEmailPutDoesNotOverwriteSent(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutOutreachEmail(ctx, "place-1", "email-v1", "first draft", false); err != nil {
		t.Fatalf("PutOutreachEmail: %v", err)
	}
	if err := s.SetOutreachEmailStatus(ctx, "place-1", "email-v1", EmailStatusSent); err != nil {
		t.Fatalf("SetOutreachEmailStatus: %v", err)
	}

	// A later re-run tries to replace the body.
	if err := s.PutOutreachEmail(ctx, "place-1", "email-v1", "regenerated draft", false); err != nil {
		t.Fatalf("PutOutreachEmail (2): %v", err)
	}

	got, _, err := s.GetOutreachEmail(ctx, "place-1", "email-v1")
	if err != nil {
		t.Fatalf("GetOutreachEmail: %v", err)
	}
	if got.Email != "first draft" || got.Status != EmailStatusSent {
		t.Fatalf("a sent email was overwritten: %+v", got)
	}
}

// A draft, by contrast, is refreshed in place.
func TestOutreachEmailPutRefreshesDraft(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutOutreachEmail(ctx, "place-1", "email-v1", "first draft", false); err != nil {
		t.Fatalf("PutOutreachEmail: %v", err)
	}
	if err := s.PutOutreachEmail(ctx, "place-1", "email-v1", "second draft", true); err != nil {
		t.Fatalf("PutOutreachEmail (2): %v", err)
	}

	got, _, err := s.GetOutreachEmail(ctx, "place-1", "email-v1")
	if err != nil {
		t.Fatalf("GetOutreachEmail: %v", err)
	}
	if got.Email != "second draft" || !got.Truncated {
		t.Fatalf("draft was not refreshed: %+v", got)
	}
}

func TestSetOutreachEmailStatus_Validation(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutOutreachEmail(ctx, "place-1", "email-v1", "body", false); err != nil {
		t.Fatalf("PutOutreachEmail: %v", err)
	}

	if err := s.SetOutreachEmailStatus(ctx, "place-1", "email-v1", "archived"); !errors.Is(err, ErrEmailStatusInvalid) {
		t.Fatalf("want ErrEmailStatusInvalid, got %v", err)
	}
	if err := s.SetOutreachEmailStatus(ctx, "missing", "email-v1", EmailStatusSent); !errors.Is(err, ErrEmailNotFound) {
		t.Fatalf("want ErrEmailNotFound for a row that does not exist, got %v", err)
	}
	if err := s.SetOutreachEmailStatus(ctx, "place-1", "email-v1", EmailStatusSkipped); err != nil {
		t.Fatalf("SetOutreachEmailStatus(skipped): %v", err)
	}
	got, _, _ := s.GetOutreachEmail(ctx, "place-1", "email-v1")
	if got.Status != EmailStatusSkipped {
		t.Fatalf("status = %q, want skipped", got.Status)
	}
}

func TestOutreachEmailNilStoreTolerant(t *testing.T) {
	ctx := context.Background()
	var s *Store

	if err := s.PutOutreachEmail(ctx, "place-1", "email-v1", "body", false); err != nil {
		t.Errorf("PutOutreachEmail on nil Store: %v", err)
	}
	if _, ok, err := s.GetOutreachEmail(ctx, "place-1", "email-v1"); err != nil || ok {
		t.Errorf("GetOutreachEmail on nil Store: ok=%v err=%v", ok, err)
	}
	// A nil Store cannot enforce the closed set, but an invalid status is a
	// programming error regardless of whether a database is attached.
	if err := s.SetOutreachEmailStatus(ctx, "place-1", "email-v1", "bogus"); !errors.Is(err, ErrEmailStatusInvalid) {
		t.Errorf("SetOutreachEmailStatus should still validate on a nil Store: %v", err)
	}
}

func TestOutreachEmailEmptyBodyNotStored(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutOutreachEmail(ctx, "place-1", "email-v1", "", false); err != nil {
		t.Fatalf("PutOutreachEmail: %v", err)
	}
	if _, ok, _ := s.GetOutreachEmail(ctx, "place-1", "email-v1"); ok {
		t.Error("an empty email body must not be stored")
	}
}
