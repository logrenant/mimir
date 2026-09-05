package store

import (
	"context"
	"errors"
	"testing"
)

func TestOutreachMessageRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutOutreachMessage(ctx, "place-1", "email", "email-v1#email", "hello there", true); err != nil {
		t.Fatalf("PutOutreachMessage: %v", err)
	}

	got, ok, err := s.GetOutreachMessage(ctx, "place-1", "email-v1#email")
	if err != nil || !ok {
		t.Fatalf("GetOutreachMessage: ok=%v err=%v", ok, err)
	}
	if got.Body != "hello there" || got.Status != OutreachStatusDraft || !got.Truncated {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Channel != "email" {
		t.Fatalf("channel = %q, want email", got.Channel)
	}
}

func TestOutreachMessageMissForEachKeyComponent(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutOutreachMessage(ctx, "place-1", "email", "email-v1#email", "body", false); err != nil {
		t.Fatalf("PutOutreachMessage: %v", err)
	}

	if _, ok, _ := s.GetOutreachMessage(ctx, "place-2", "email-v1#email"); ok {
		t.Error("a different place_id must miss")
	}
	if _, ok, _ := s.GetOutreachMessage(ctx, "place-1", "email-v2#email"); ok {
		t.Error("a different prompt_version must miss")
	}
}

// The two channels are two rows, not two versions of one: writing the WhatsApp
// message must not disturb the email that has already been sent.
func TestOutreachMessageChannelsAreIndependent(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutOutreachMessage(ctx, "p1", "email", "v1#email", "mektup", false); err != nil {
		t.Fatalf("PutOutreachMessage(email): %v", err)
	}
	if err := s.SetOutreachStatus(ctx, "p1", "email", OutreachStatusSent); err != nil {
		t.Fatalf("SetOutreachStatus(email): %v", err)
	}
	if err := s.PutOutreachMessage(ctx, "p1", "whatsapp", "v1#whatsapp", "kısa mesaj", false); err != nil {
		t.Fatalf("PutOutreachMessage(whatsapp): %v", err)
	}

	mail, _, _ := s.GetOutreachMessage(ctx, "p1", "v1#email")
	if mail.Status != OutreachStatusSent || mail.Body != "mektup" {
		t.Fatalf("the sent email changed when whatsapp was written: %+v", mail)
	}
	wa, ok, _ := s.GetOutreachMessage(ctx, "p1", "v1#whatsapp")
	if !ok || wa.Status != OutreachStatusDraft || wa.Channel != "whatsapp" {
		t.Fatalf("whatsapp draft wrong: %+v ok=%v", wa, ok)
	}
}

// A region re-run refreshes a draft but must never overwrite a sent message.
func TestOutreachMessagePutDoesNotOverwriteSent(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutOutreachMessage(ctx, "place-1", "email", "email-v1", "first draft", false); err != nil {
		t.Fatalf("PutOutreachMessage: %v", err)
	}
	if err := s.SetOutreachStatus(ctx, "place-1", "email", OutreachStatusSent); err != nil {
		t.Fatalf("SetOutreachStatus: %v", err)
	}

	// A later re-run tries to replace the body.
	if err := s.PutOutreachMessage(ctx, "place-1", "email", "email-v1", "regenerated draft", false); err != nil {
		t.Fatalf("PutOutreachMessage (2): %v", err)
	}

	got, _, err := s.GetOutreachMessage(ctx, "place-1", "email-v1")
	if err != nil {
		t.Fatalf("GetOutreachMessage: %v", err)
	}
	if got.Body != "first draft" || got.Status != OutreachStatusSent {
		t.Fatalf("a sent message was overwritten: %+v", got)
	}
}

// A draft, by contrast, is refreshed in place.
func TestOutreachMessagePutRefreshesDraft(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutOutreachMessage(ctx, "place-1", "email", "email-v1", "first draft", false); err != nil {
		t.Fatalf("PutOutreachMessage: %v", err)
	}
	if err := s.PutOutreachMessage(ctx, "place-1", "email", "email-v1", "second draft", true); err != nil {
		t.Fatalf("PutOutreachMessage (2): %v", err)
	}

	got, _, err := s.GetOutreachMessage(ctx, "place-1", "email-v1")
	if err != nil {
		t.Fatalf("GetOutreachMessage: %v", err)
	}
	if got.Body != "second draft" || !got.Truncated {
		t.Fatalf("draft was not refreshed: %+v", got)
	}
}

func TestSetOutreachStatus_Validation(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutOutreachMessage(ctx, "place-1", "email", "email-v1", "body", false); err != nil {
		t.Fatalf("PutOutreachMessage: %v", err)
	}

	if err := s.SetOutreachStatus(ctx, "place-1", "email", "archived"); !errors.Is(err, ErrOutreachStatusInvalid) {
		t.Fatalf("want ErrOutreachStatusInvalid, got %v", err)
	}
	if err := s.SetOutreachStatus(ctx, "missing", "email", OutreachStatusSent); !errors.Is(err, ErrOutreachNotFound) {
		t.Fatalf("want ErrOutreachNotFound for a row that does not exist, got %v", err)
	}
	if err := s.SetOutreachStatus(ctx, "place-1", "whatsapp", OutreachStatusSent); !errors.Is(err, ErrOutreachNotFound) {
		t.Fatalf("want ErrOutreachNotFound for a channel with no draft, got %v", err)
	}
	if err := s.SetOutreachStatus(ctx, "place-1", "email", OutreachStatusSkipped); err != nil {
		t.Fatalf("SetOutreachStatus(skipped): %v", err)
	}
	got, _, _ := s.GetOutreachMessage(ctx, "place-1", "email-v1")
	if got.Status != OutreachStatusSkipped {
		t.Fatalf("status = %q, want skipped", got.Status)
	}
}

// Editing a rule file changes the prompt version, so the same company ends up
// with two email rows. Reading must answer with the newest one, and marking it
// must land on that one — otherwise editing the rules would empty the screen.
func TestOutreachStatusFollowsTheNewestVersion(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutOutreachMessage(ctx, "p1", "email", "v1#email:old", "eski", false); err != nil {
		t.Fatalf("PutOutreachMessage(old): %v", err)
	}
	// Distinct updated_at, which is what "newest" is resolved by.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE outreach_emails SET updated_at = updated_at - 60 WHERE prompt_version = 'v1#email:old'`); err != nil {
		t.Fatalf("ageing the old row: %v", err)
	}
	if err := s.PutOutreachMessage(ctx, "p1", "email", "v1#email:new", "yeni", false); err != nil {
		t.Fatalf("PutOutreachMessage(new): %v", err)
	}

	if err := s.SetOutreachStatus(ctx, "p1", "email", OutreachStatusSent); err != nil {
		t.Fatalf("SetOutreachStatus: %v", err)
	}

	fresh, _, _ := s.GetOutreachMessage(ctx, "p1", "v1#email:new")
	if fresh.Status != OutreachStatusSent {
		t.Fatalf("the newest draft was not marked: %+v", fresh)
	}
	stale, _, _ := s.GetOutreachMessage(ctx, "p1", "v1#email:old")
	if stale.Status != OutreachStatusDraft {
		t.Fatalf("an older version was marked too: %+v", stale)
	}
}

func TestOutreachMessageNilStoreTolerant(t *testing.T) {
	ctx := context.Background()
	var s *Store

	if err := s.PutOutreachMessage(ctx, "place-1", "email", "email-v1", "body", false); err != nil {
		t.Errorf("PutOutreachMessage on nil Store: %v", err)
	}
	if _, ok, err := s.GetOutreachMessage(ctx, "place-1", "email-v1"); err != nil || ok {
		t.Errorf("GetOutreachMessage on nil Store: ok=%v err=%v", ok, err)
	}
	// A nil Store cannot enforce the closed set, but an invalid status is a
	// programming error regardless of whether a database is attached.
	if err := s.SetOutreachStatus(ctx, "place-1", "email", "bogus"); !errors.Is(err, ErrOutreachStatusInvalid) {
		t.Errorf("SetOutreachStatus should still validate on a nil Store: %v", err)
	}
}

func TestOutreachMessageEmptyBodyNotStored(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutOutreachMessage(ctx, "place-1", "email", "email-v1", "", false); err != nil {
		t.Fatalf("PutOutreachMessage: %v", err)
	}
	if _, ok, _ := s.GetOutreachMessage(ctx, "place-1", "email-v1"); ok {
		t.Error("an empty message body must not be stored")
	}
}
