package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
)

func TestPM_SendAndGet(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	sender := newUser("pm-sender", "pmsender@example.com")
	if err := s.CreateUser(ctx, sender); err != nil {
		t.Fatalf("CreateUser sender: %v", err)
	}
	recipient := newUser("pm-recipient", "pmrecipient@example.com")
	if err := s.CreateUser(ctx, recipient); err != nil {
		t.Fatalf("CreateUser recipient: %v", err)
	}

	pm := &model.PrivateMessage{
		SenderID:    sender.ID,
		RecipientID: recipient.ID,
		Subject:     "Hello",
		Body:        "How are you?",
	}
	if err := s.CreatePM(ctx, pm); err != nil {
		t.Fatalf("CreatePM: %v", err)
	}
	if pm.ID == 0 {
		t.Fatal("expected non-zero ID after CreatePM")
	}

	got, err := s.GetPMByID(ctx, pm.ID)
	if err != nil {
		t.Fatalf("GetPMByID: %v", err)
	}
	if got.Subject != pm.Subject {
		t.Errorf("Subject: got %q, want %q", got.Subject, pm.Subject)
	}
	if got.Body != pm.Body {
		t.Errorf("Body: got %q, want %q", got.Body, pm.Body)
	}
	if got.IsRead {
		t.Error("expected IsRead to be false on new PM")
	}
	if got.SenderDeleted {
		t.Error("expected SenderDeleted to be false on new PM")
	}
	if got.RecipientDeleted {
		t.Error("expected RecipientDeleted to be false on new PM")
	}
}

func TestPM_GetNotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.GetPMByID(ctx, 9999)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestPM_Inbox(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	sender := newUser("inbox-sender", "inboxsender@example.com")
	if err := s.CreateUser(ctx, sender); err != nil {
		t.Fatalf("CreateUser sender: %v", err)
	}
	recipient := newUser("inbox-recipient", "inboxrecipient@example.com")
	if err := s.CreateUser(ctx, recipient); err != nil {
		t.Fatalf("CreateUser recipient: %v", err)
	}

	for i := 0; i < 3; i++ {
		pm := &model.PrivateMessage{
			SenderID:    sender.ID,
			RecipientID: recipient.ID,
			Subject:     fmt.Sprintf("Message %d", i+1),
			Body:        "body",
		}
		if err := s.CreatePM(ctx, pm); err != nil {
			t.Fatalf("CreatePM %d: %v", i, err)
		}
	}

	result, err := s.ListInbox(ctx, recipient.ID, store.PageRequest{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	if result.Total != 3 {
		t.Errorf("Total: got %d, want 3", result.Total)
	}
	if len(result.Items) != 3 {
		t.Errorf("Items len: got %d, want 3", len(result.Items))
	}

	// Sender's inbox should be empty.
	senderInbox, err := s.ListInbox(ctx, sender.ID, store.PageRequest{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatalf("ListInbox sender: %v", err)
	}
	if senderInbox.Total != 0 {
		t.Errorf("sender inbox total: got %d, want 0", senderInbox.Total)
	}
}

func TestPM_MarkRead(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	sender := newUser("read-sender", "readsender@example.com")
	if err := s.CreateUser(ctx, sender); err != nil {
		t.Fatalf("CreateUser sender: %v", err)
	}
	recipient := newUser("read-recipient", "readrecipient@example.com")
	if err := s.CreateUser(ctx, recipient); err != nil {
		t.Fatalf("CreateUser recipient: %v", err)
	}

	pm := &model.PrivateMessage{
		SenderID:    sender.ID,
		RecipientID: recipient.ID,
		Subject:     "Read Me",
		Body:        "please read",
	}
	if err := s.CreatePM(ctx, pm); err != nil {
		t.Fatalf("CreatePM: %v", err)
	}

	if err := s.MarkPMRead(ctx, pm.ID); err != nil {
		t.Fatalf("MarkPMRead: %v", err)
	}

	got, err := s.GetPMByID(ctx, pm.ID)
	if err != nil {
		t.Fatalf("GetPMByID: %v", err)
	}
	if !got.IsRead {
		t.Error("expected IsRead to be true after MarkPMRead")
	}
}

func TestPM_SoftDeletePerSide(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	sender := newUser("del-sender", "delsender@example.com")
	if err := s.CreateUser(ctx, sender); err != nil {
		t.Fatalf("CreateUser sender: %v", err)
	}
	recipient := newUser("del-recipient", "delrecipient@example.com")
	if err := s.CreateUser(ctx, recipient); err != nil {
		t.Fatalf("CreateUser recipient: %v", err)
	}

	pm := &model.PrivateMessage{
		SenderID:    sender.ID,
		RecipientID: recipient.ID,
		Subject:     "Delete Me",
		Body:        "please delete",
	}
	if err := s.CreatePM(ctx, pm); err != nil {
		t.Fatalf("CreatePM: %v", err)
	}

	// Delete for sender only — recipient should still see it in their inbox.
	if err := s.DeletePMForSender(ctx, pm.ID); err != nil {
		t.Fatalf("DeletePMForSender: %v", err)
	}

	got, err := s.GetPMByID(ctx, pm.ID)
	if err != nil {
		t.Fatalf("GetPMByID after sender delete: %v", err)
	}
	if !got.SenderDeleted {
		t.Error("expected SenderDeleted to be true")
	}
	if got.RecipientDeleted {
		t.Error("expected RecipientDeleted to remain false")
	}

	inbox, err := s.ListInbox(ctx, recipient.ID, store.PageRequest{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatalf("ListInbox: %v", err)
	}
	if inbox.Total != 1 {
		t.Errorf("recipient inbox total after sender delete: got %d, want 1", inbox.Total)
	}

	// Delete for recipient — message persists in DB but disappears from inbox.
	if err := s.DeletePMForRecipient(ctx, pm.ID); err != nil {
		t.Fatalf("DeletePMForRecipient: %v", err)
	}

	got2, err := s.GetPMByID(ctx, pm.ID)
	if err != nil {
		t.Fatalf("GetPMByID after both delete: %v", err)
	}
	if !got2.RecipientDeleted {
		t.Error("expected RecipientDeleted to be true")
	}

	inbox2, err := s.ListInbox(ctx, recipient.ID, store.PageRequest{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatalf("ListInbox after recipient delete: %v", err)
	}
	if inbox2.Total != 0 {
		t.Errorf("expected empty inbox after recipient delete, got %d", inbox2.Total)
	}
}
