package auth_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nitro/forum_forge/internal/auth"
	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store/sqlite"
)

// newTokenStore stands up an in-memory SQLite-backed store wired with a
// sample user, so token tests can focus on token semantics rather than user
// setup.
func newTokenStore(t *testing.T) (*sqlite.Store, *model.User) {
	t.Helper()
	migrationsFS := os.DirFS("../../migrations")
	st, err := sqlite.New(":memory:", migrationsFS)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	u := &model.User{
		Username:     "alice",
		Email:        "alice@example.com",
		PasswordHash: "$2a$10$existinghash",
		Role:         model.RoleMember,
	}
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return st, u
}

func TestToken_IssueAndConsumeVerification(t *testing.T) {
	ctx := context.Background()
	st, u := newTokenStore(t)
	ts := auth.NewTokenService(st)

	raw, err := ts.IssueVerification(ctx, u.ID)
	if err != nil {
		t.Fatalf("IssueVerification: %v", err)
	}
	if raw == "" {
		t.Fatal("raw token should not be empty")
	}

	// Hash should match what's stored.
	rec, err := st.GetAuthTokenByHash(ctx, auth.HashToken(raw), model.TokenTypeVerifyEmail)
	if err != nil {
		t.Fatalf("GetAuthTokenByHash: %v", err)
	}
	if rec == nil {
		t.Fatal("token row missing or already expired")
	}
	if rec.UserID != u.ID {
		t.Errorf("UserID: got %d, want %d", rec.UserID, u.ID)
	}
	if rec.Type != model.TokenTypeVerifyEmail {
		t.Errorf("Type: got %q, want %q", rec.Type, model.TokenTypeVerifyEmail)
	}

	got, err := ts.ConsumeVerification(ctx, raw)
	if err != nil {
		t.Fatalf("ConsumeVerification: %v", err)
	}
	if !got.EmailVerified {
		t.Error("user should be marked verified")
	}

	// Token must be deleted after consume.
	rec2, err := st.GetAuthTokenByHash(ctx, auth.HashToken(raw), model.TokenTypeVerifyEmail)
	if err == nil && rec2 != nil {
		t.Error("token should be removed after consume")
	}

	// Re-using the same raw token must fail with ErrTokenInvalid.
	if _, err := ts.ConsumeVerification(ctx, raw); !errors.Is(err, auth.ErrTokenInvalid) {
		t.Errorf("re-consume: got %v, want ErrTokenInvalid", err)
	}
}

func TestToken_IssueVerification_ReplacesPrior(t *testing.T) {
	ctx := context.Background()
	st, u := newTokenStore(t)
	ts := auth.NewTokenService(st)

	first, err := ts.IssueVerification(ctx, u.ID)
	if err != nil {
		t.Fatalf("IssueVerification: %v", err)
	}
	second, err := ts.IssueVerification(ctx, u.ID)
	if err != nil {
		t.Fatalf("IssueVerification (second): %v", err)
	}
	if first == second {
		t.Fatal("expected new random token on re-issue")
	}

	// First token should be unusable now.
	if _, err := ts.ConsumeVerification(ctx, first); !errors.Is(err, auth.ErrTokenInvalid) {
		t.Errorf("first token: got %v, want ErrTokenInvalid", err)
	}
	// Second token still works.
	if _, err := ts.ConsumeVerification(ctx, second); err != nil {
		t.Errorf("second token: %v", err)
	}
}

func TestToken_ExpiredTokenInvalid(t *testing.T) {
	ctx := context.Background()
	st, u := newTokenStore(t)

	// Backdate by stuffing the row directly with an already-expired expiry.
	rec := &model.AuthToken{
		UserID:    u.ID,
		Type:      model.TokenTypeResetPassword,
		TokenHash: auth.HashToken("alreadyexpired"),
		ExpiresAt: time.Now().Add(-time.Hour),
	}
	if err := st.CreateAuthToken(ctx, rec); err != nil {
		t.Fatalf("CreateAuthToken: %v", err)
	}

	ts := auth.NewTokenService(st)
	if _, err := ts.ValidateReset(ctx, "alreadyexpired"); !errors.Is(err, auth.ErrTokenInvalid) {
		t.Errorf("ValidateReset on expired token: got %v, want ErrTokenInvalid", err)
	}
}

func TestToken_ConsumeReset_UpdatesPasswordAndKillsSessions(t *testing.T) {
	ctx := context.Background()
	st, u := newTokenStore(t)

	// Open two sessions for the user. Both must be killed by the reset.
	for _, id := range []string{"sess-a", "sess-b"} {
		if err := st.CreateSession(ctx, &model.Session{
			ID:        id,
			UserID:    u.ID,
			ExpiresAt: time.Now().Add(24 * time.Hour),
		}); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
	}

	ts := auth.NewTokenService(st)
	raw, err := ts.IssueReset(ctx, u.ID)
	if err != nil {
		t.Fatalf("IssueReset: %v", err)
	}

	const newHash = "$2a$10$brand-new-bcrypt-hash"
	got, err := ts.ConsumeReset(ctx, raw, newHash)
	if err != nil {
		t.Fatalf("ConsumeReset: %v", err)
	}
	if got.PasswordHash != newHash {
		t.Errorf("PasswordHash on returned user: got %q, want %q", got.PasswordHash, newHash)
	}

	stored, err := st.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if stored.PasswordHash != newHash {
		t.Errorf("stored PasswordHash: got %q, want %q", stored.PasswordHash, newHash)
	}

	// Both sessions should be gone.
	for _, id := range []string{"sess-a", "sess-b"} {
		s, err := st.GetSession(ctx, id)
		if err == nil && s != nil {
			t.Errorf("session %q still present after reset", id)
		}
	}

	// Re-using the reset token must fail.
	if _, err := ts.ConsumeReset(ctx, raw, "another-hash"); !errors.Is(err, auth.ErrTokenInvalid) {
		t.Errorf("re-use: got %v, want ErrTokenInvalid", err)
	}
}

func TestToken_FindUserByEmail_NotFound(t *testing.T) {
	ctx := context.Background()
	st, _ := newTokenStore(t)
	ts := auth.NewTokenService(st)

	_, err := ts.FindUserByEmail(ctx, "ghost@example.com")
	if !errors.Is(err, auth.ErrUserNotFound) {
		t.Errorf("got %v, want ErrUserNotFound", err)
	}
}

func TestToken_HashTokenIsDeterministic(t *testing.T) {
	a := auth.HashToken("hello")
	b := auth.HashToken("hello")
	if a != b {
		t.Errorf("hash mismatch for same input: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "2cf24dba5fb0a30") { // sha256("hello")
		t.Errorf("unexpected hash for %q: %s", "hello", a)
	}
}
