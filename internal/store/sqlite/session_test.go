package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
)

func newSession(userID int64, ttl time.Duration) *model.Session {
	return &model.Session{
		ID:        uuid.NewString(),
		UserID:    userID,
		Data:      []byte(`{"flash":"welcome"}`),
		ExpiresAt: time.Now().UTC().Add(ttl),
	}
}

func TestSession_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("hsession", "hsession@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	sess := newSession(u.ID, 24*time.Hour)
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := s.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got == nil {
		t.Fatal("GetSession returned nil for valid session")
	}
	if got.UserID != u.ID {
		t.Errorf("UserID: got %d, want %d", got.UserID, u.ID)
	}
	if string(got.Data) != string(sess.Data) {
		t.Errorf("Data: got %s, want %s", got.Data, sess.Data)
	}
}

func TestSession_NotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.GetSession(ctx, "nonexistent-id")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSession_Expired(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("expired_user", "expired@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Session that expired 1 hour ago.
	sess := newSession(u.ID, -1*time.Hour)
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := s.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for expired session, got %+v", got)
	}
}

func TestSession_Delete(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("del_user", "del@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	sess := newSession(u.ID, 24*time.Hour)
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := s.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	_, err := s.GetSession(ctx, sess.ID)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestSession_DeleteUserSessions(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("multi_sess", "multi@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	ids := make([]string, 3)
	for i := range ids {
		sess := newSession(u.ID, 24*time.Hour)
		if err := s.CreateSession(ctx, sess); err != nil {
			t.Fatalf("CreateSession %d: %v", i, err)
		}
		ids[i] = sess.ID
	}

	if err := s.DeleteUserSessions(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUserSessions: %v", err)
	}

	for _, id := range ids {
		_, err := s.GetSession(ctx, id)
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("session %s: expected ErrNotFound after DeleteUserSessions, got %v", id, err)
		}
	}
}
