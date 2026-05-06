package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
)

// Token lifetimes are spec.md §4.1: 1 hour for password reset, 24 hours for
// email verification. The reset window is intentionally tight to limit the
// damage from a leaked email; verification is more lenient because the user
// might not check mail right away.
const (
	VerifyTokenLifetime = 24 * time.Hour
	ResetTokenLifetime  = 1 * time.Hour

	// tokenRandomBytes is the entropy size of the raw token before encoding.
	// 32 bytes → 256 bits of randomness, well above any practical brute-force
	// attack against a SHA-256 hash kept in the DB.
	tokenRandomBytes = 32
)

// Errors returned by the token API. Handlers translate these to user-facing
// messages; nothing here leaks whether a user account exists.
var (
	ErrTokenInvalid = errors.New("auth: token is invalid or expired")
	ErrUserNotFound = errors.New("auth: user not found")
)

// TokenStore is the slice of *sqlite.Store the token flow needs. Defining a
// narrow interface keeps tests trivial — a fake only has to satisfy the
// methods we actually call.
type TokenStore interface {
	GetUserByID(ctx context.Context, id int64) (*model.User, error)
	GetUserByEmail(ctx context.Context, email string) (*model.User, error)
	UpdateUser(ctx context.Context, u *model.User) error
	DeleteUserSessions(ctx context.Context, userID int64) error

	CreateAuthToken(ctx context.Context, t *model.AuthToken) error
	GetAuthTokenByHash(ctx context.Context, tokenHash string, tokenType model.TokenType) (*model.AuthToken, error)
	DeleteAuthToken(ctx context.Context, id int64) error
	DeleteUserAuthTokensByType(ctx context.Context, userID int64, tokenType model.TokenType) error
}

// TokenService issues and validates email-verification + password-reset tokens.
// It is independent of *Service so the registration/login surface stays narrow,
// but the same store usually backs both.
type TokenService struct {
	store TokenStore
	now   func() time.Time
}

// NewTokenService returns a TokenService backed by store.
func NewTokenService(s TokenStore) *TokenService {
	return &TokenService{store: s, now: time.Now}
}

// IssueVerification generates a one-time email-verification token for userID.
// Any previous verification tokens for the same user are deleted first so the
// user only ever has one active link. The raw token (returned to the caller)
// is what goes in the email; the DB only ever sees its SHA-256 hash.
func (t *TokenService) IssueVerification(ctx context.Context, userID int64) (string, error) {
	return t.issue(ctx, userID, model.TokenTypeVerifyEmail, VerifyTokenLifetime)
}

// IssueReset generates a one-time password-reset token for userID. Like
// IssueVerification, prior reset tokens for the user are deleted first.
func (t *TokenService) IssueReset(ctx context.Context, userID int64) (string, error) {
	return t.issue(ctx, userID, model.TokenTypeResetPassword, ResetTokenLifetime)
}

func (t *TokenService) issue(ctx context.Context, userID int64, tokenType model.TokenType, lifetime time.Duration) (string, error) {
	raw, err := newRawToken()
	if err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}

	if err := t.store.DeleteUserAuthTokensByType(ctx, userID, tokenType); err != nil {
		return "", fmt.Errorf("clear prior tokens: %w", err)
	}

	rec := &model.AuthToken{
		UserID:    userID,
		Type:      tokenType,
		TokenHash: HashToken(raw),
		ExpiresAt: t.now().Add(lifetime),
	}
	if err := t.store.CreateAuthToken(ctx, rec); err != nil {
		return "", fmt.Errorf("persist token: %w", err)
	}
	return raw, nil
}

// ConsumeVerification validates the raw verification token, marks the owning
// user as email-verified, deletes the token, and returns the user. Returns
// ErrTokenInvalid for unknown, expired, or wrong-type tokens.
func (t *TokenService) ConsumeVerification(ctx context.Context, rawToken string) (*model.User, error) {
	tok, user, err := t.lookup(ctx, rawToken, model.TokenTypeVerifyEmail)
	if err != nil {
		return nil, err
	}
	if !user.EmailVerified {
		user.EmailVerified = true
		if err := t.store.UpdateUser(ctx, user); err != nil {
			return nil, fmt.Errorf("mark verified: %w", err)
		}
	}
	if err := t.store.DeleteAuthToken(ctx, tok.ID); err != nil {
		return nil, fmt.Errorf("delete token: %w", err)
	}
	return user, nil
}

// ValidateReset returns the user owning rawToken without consuming it. Used
// to render the password-reset form (so the user sees errors before typing
// a new password). The token is still consumed when ConsumeReset runs.
func (t *TokenService) ValidateReset(ctx context.Context, rawToken string) (*model.User, error) {
	_, user, err := t.lookup(ctx, rawToken, model.TokenTypeResetPassword)
	if err != nil {
		return nil, err
	}
	return user, nil
}

// ConsumeReset validates rawToken, hashes newPassword, and updates the user.
// On success the token row is deleted and every existing session for the user
// is invalidated — a password reset must log out other devices (spec.md §4.1).
func (t *TokenService) ConsumeReset(ctx context.Context, rawToken, newPasswordHash string) (*model.User, error) {
	tok, user, err := t.lookup(ctx, rawToken, model.TokenTypeResetPassword)
	if err != nil {
		return nil, err
	}

	user.PasswordHash = newPasswordHash
	if err := t.store.UpdateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("update password: %w", err)
	}
	if err := t.store.DeleteAuthToken(ctx, tok.ID); err != nil {
		return nil, fmt.Errorf("delete token: %w", err)
	}
	if err := t.store.DeleteUserSessions(ctx, user.ID); err != nil {
		return nil, fmt.Errorf("invalidate sessions: %w", err)
	}
	return user, nil
}

// FindUserByEmail looks up a user by their lowercase email. Returns
// ErrUserNotFound for unknown emails so the caller can render a generic
// "if an account exists, an email was sent" response.
func (t *TokenService) FindUserByEmail(ctx context.Context, email string) (*model.User, error) {
	u, err := t.store.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return u, nil
}

// lookup hashes rawToken, fetches the matching DB row, and resolves the user.
// All "no match" / "expired" / "wrong type" cases collapse to ErrTokenInvalid
// so the caller can render a single generic error.
func (t *TokenService) lookup(ctx context.Context, rawToken string, tokenType model.TokenType) (*model.AuthToken, *model.User, error) {
	if rawToken == "" {
		return nil, nil, ErrTokenInvalid
	}
	tok, err := t.store.GetAuthTokenByHash(ctx, HashToken(rawToken), tokenType)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, ErrTokenInvalid
		}
		return nil, nil, fmt.Errorf("lookup token: %w", err)
	}
	if tok == nil { // expired
		return nil, nil, ErrTokenInvalid
	}

	user, err := t.store.GetUserByID(ctx, tok.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Token row outlived the user; treat as invalid.
			return nil, nil, ErrTokenInvalid
		}
		return nil, nil, fmt.Errorf("lookup user: %w", err)
	}
	return tok, user, nil
}

// HashToken returns the canonical SHA-256 hex digest used to look tokens up
// in the DB. Exported so tests (and any future migration tooling) can verify
// hashes without re-implementing the algorithm.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func newRawToken() (string, error) {
	b := make([]byte, tokenRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
