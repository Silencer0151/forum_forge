package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
)

// CreateAuthToken inserts a new email-verification or password-reset token.
// Callers (the auth package) generate the raw token, hash it with SHA-256,
// and pass the hex-encoded hash here so the raw value is never persisted.
func (s *Store) CreateAuthToken(ctx context.Context, t *model.AuthToken) error {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO auth_tokens (user_id, type, token_hash, expires_at)
		VALUES (?, ?, ?, ?)`,
		t.UserID, string(t.Type), t.TokenHash,
		t.ExpiresAt.UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		return fmt.Errorf("create auth token: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("create auth token last id: %w", err)
	}
	t.ID = id
	return nil
}

// GetAuthTokenByHash returns the token row matching tokenHash + type. It
// returns store.ErrNotFound when no row matches and (nil, nil) when a row
// exists but has expired — the caller should treat both as "invalid token".
func (s *Store) GetAuthTokenByHash(ctx context.Context, tokenHash string, tokenType model.TokenType) (*model.AuthToken, error) {
	var t model.AuthToken
	var expiresAt, createdAt dbTime
	var rawType string

	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, type, token_hash, expires_at, created_at
		FROM auth_tokens WHERE token_hash = ? AND type = ?`,
		tokenHash, string(tokenType),
	).Scan(&t.ID, &t.UserID, &rawType, &t.TokenHash, &expiresAt, &createdAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get auth token: %w", err)
	}
	t.Type = model.TokenType(rawType)
	t.ExpiresAt = expiresAt.T
	t.CreatedAt = createdAt.T

	if time.Now().UTC().After(t.ExpiresAt) {
		return nil, nil
	}
	return &t, nil
}

// DeleteAuthToken removes a single token row, typically after a successful
// verification or password reset.
func (s *Store) DeleteAuthToken(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM auth_tokens WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete auth token: %w", err)
	}
	return nil
}

// DeleteUserAuthTokensByType removes every outstanding token of one type for
// a user. Called before issuing a fresh token so a user only ever has one
// active verification or reset link in their inbox.
func (s *Store) DeleteUserAuthTokensByType(ctx context.Context, userID int64, tokenType model.TokenType) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM auth_tokens WHERE user_id = ? AND type = ?",
		userID, string(tokenType),
	)
	if err != nil {
		return fmt.Errorf("delete user auth tokens: %w", err)
	}
	return nil
}

// DeleteExpiredAuthTokens is a maintenance hook: prune rows whose expiry has
// already passed. Safe to call from a periodic background task.
func (s *Store) DeleteExpiredAuthTokens(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM auth_tokens WHERE expires_at < ?",
		now.UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		return fmt.Errorf("delete expired auth tokens: %w", err)
	}
	return nil
}
