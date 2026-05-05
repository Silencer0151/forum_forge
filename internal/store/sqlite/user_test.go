package sqlite_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
	"github.com/nitro/forum_forge/internal/store/sqlite"
)

func newTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	migrationsFS := os.DirFS("../../../migrations")
	s, err := sqlite.New(":memory:", migrationsFS)
	if err != nil {
		t.Fatalf("newTestStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newUser(username, email string) *model.User {
	return &model.User{
		Username:     username,
		Email:        email,
		PasswordHash: "$2a$10$fakehash",
		DisplayName:  "",
		Role:         model.RoleMember,
	}
}

func TestUser_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("alice", "alice@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("expected non-zero ID after create")
	}

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Username != u.Username {
		t.Errorf("username: got %q, want %q", got.Username, u.Username)
	}
	if got.Email != u.Email {
		t.Errorf("email: got %q, want %q", got.Email, u.Email)
	}

	got2, err := s.GetUserByEmail(ctx, u.Email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if got2.ID != u.ID {
		t.Errorf("GetUserByEmail ID: got %d, want %d", got2.ID, u.ID)
	}

	got3, err := s.GetUserByUsername(ctx, u.Username)
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if got3.ID != u.ID {
		t.Errorf("GetUserByUsername ID: got %d, want %d", got3.ID, u.ID)
	}
}

func TestUser_GetNotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	_, err := s.GetUserByID(ctx, 9999)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	_, err = s.GetUserByEmail(ctx, "nobody@example.com")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	_, err = s.GetUserByUsername(ctx, "nobody")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUser_Update(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("bob", "bob@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	u.DisplayName = "Bob Smith"
	u.Signature = "Sent from my Go server"
	if err := s.UpdateUser(ctx, u); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.DisplayName != "Bob Smith" {
		t.Errorf("DisplayName: got %q, want %q", got.DisplayName, "Bob Smith")
	}
	if got.Signature != "Sent from my Go server" {
		t.Errorf("Signature: got %q, want %q", got.Signature, "Sent from my Go server")
	}
}

func TestUser_UniqueConstraints(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u1 := newUser("carol", "carol@example.com")
	if err := s.CreateUser(ctx, u1); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Duplicate email.
	u2 := newUser("carol2", "carol@example.com")
	if err := s.CreateUser(ctx, u2); err == nil {
		t.Error("expected error for duplicate email, got nil")
	}

	// Duplicate username.
	u3 := newUser("carol", "carol3@example.com")
	if err := s.CreateUser(ctx, u3); err == nil {
		t.Error("expected error for duplicate username, got nil")
	}
}

func TestUser_BanUnban(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("dave", "dave@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := s.BanUser(ctx, u.ID, "spam"); err != nil {
		t.Fatalf("BanUser: %v", err)
	}

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if !got.Banned {
		t.Error("expected user to be banned")
	}
	if got.BanReason != "spam" {
		t.Errorf("BanReason: got %q, want %q", got.BanReason, "spam")
	}

	if err := s.UnbanUser(ctx, u.ID); err != nil {
		t.Fatalf("UnbanUser: %v", err)
	}

	got2, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserByID after unban: %v", err)
	}
	if got2.Banned {
		t.Error("expected user to be unbanned")
	}
	if got2.BanReason != "" {
		t.Errorf("expected empty BanReason after unban, got %q", got2.BanReason)
	}
}

func TestUser_ExternalID(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("eve", "eve@example.com")
	u.ExternalID = "ext-123"
	u.ExternalSource = "kanoogi"
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := s.GetUserByExternalID(ctx, "ext-123", "kanoogi")
	if err != nil {
		t.Fatalf("GetUserByExternalID: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("ID: got %d, want %d", got.ID, u.ID)
	}

	_, err = s.GetUserByExternalID(ctx, "ext-999", "kanoogi")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown external ID, got %v", err)
	}
}

func TestUser_IncrementPostCount(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("frank", "frank@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := s.IncrementPostCount(ctx, u.ID); err != nil {
			t.Fatalf("IncrementPostCount: %v", err)
		}
	}

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.PostCount != 3 {
		t.Errorf("PostCount: got %d, want 3", got.PostCount)
	}
}

func TestUser_UpdateLastSeen(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	u := newUser("grace", "grace@example.com")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	original := got.LastSeenAt

	future := original.Add(60 * 1e9) // 60 seconds later
	if err := s.UpdateLastSeen(ctx, u.ID, future); err != nil {
		t.Fatalf("UpdateLastSeen: %v", err)
	}

	got2, err := s.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserByID after UpdateLastSeen: %v", err)
	}
	if !got2.LastSeenAt.After(original) {
		t.Errorf("LastSeenAt not updated: got %v, original %v", got2.LastSeenAt, original)
	}
}
