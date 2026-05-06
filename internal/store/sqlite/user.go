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

const userCols = `id, username, email, password_hash, display_name, avatar_url,
	signature, role, post_count, created_at, last_seen_at,
	banned, ban_reason, email_verified, external_id, external_source`

func scanUser(row rowScanner) (*model.User, error) {
	var u model.User
	var banReason, externalID, externalSource sql.NullString
	var banned, emailVerified int
	var createdAt, lastSeenAt dbTime

	err := row.Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash,
		&u.DisplayName, &u.AvatarURL, &u.Signature,
		&u.Role, &u.PostCount,
		&createdAt, &lastSeenAt,
		&banned, &banReason, &emailVerified, &externalID, &externalSource,
	)
	if err != nil {
		return nil, err
	}
	u.CreatedAt = createdAt.T
	u.LastSeenAt = lastSeenAt.T
	u.Banned = banned != 0
	u.BanReason = banReason.String
	u.EmailVerified = emailVerified != 0
	u.ExternalID = externalID.String
	u.ExternalSource = externalSource.String
	return &u, nil
}

func (s *Store) CreateUser(ctx context.Context, u *model.User) error {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO users
			(username, email, password_hash, display_name, avatar_url, signature,
			 role, post_count, created_at, last_seen_at, banned, ban_reason,
			 external_id, external_source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 0, NULL, ?, ?)`,
		u.Username, u.Email, u.PasswordHash, u.DisplayName, u.AvatarURL,
		u.Signature, string(u.Role), u.PostCount,
		nullStr(u.ExternalID), nullStr(u.ExternalSource),
	)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("create user last id: %w", err)
	}
	u.ID = id
	return nil
}

func (s *Store) GetUserByID(ctx context.Context, id int64) (*model.User, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+userCols+" FROM users WHERE id = ?", id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return u, err
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+userCols+" FROM users WHERE email = ?", email)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return u, err
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+userCols+" FROM users WHERE username = ?", username)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return u, err
}

func (s *Store) GetUserByExternalID(ctx context.Context, externalID, source string) (*model.User, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+userCols+" FROM users WHERE external_id = ? AND external_source = ?",
		externalID, source)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return u, err
}

func (s *Store) UpdateUser(ctx context.Context, u *model.User) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users SET
			username = ?, email = ?, password_hash = ?, display_name = ?,
			avatar_url = ?, signature = ?, role = ?,
			ban_reason = ?, banned = ?, email_verified = ?,
			external_id = ?, external_source = ?
		WHERE id = ?`,
		u.Username, u.Email, u.PasswordHash, u.DisplayName,
		u.AvatarURL, u.Signature, string(u.Role),
		nullStr(u.BanReason), boolInt(u.Banned), boolInt(u.EmailVerified),
		nullStr(u.ExternalID), nullStr(u.ExternalSource),
		u.ID,
	)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	return nil
}

func (s *Store) IncrementPostCount(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE users SET post_count = post_count + 1 WHERE id = ?", userID)
	if err != nil {
		return fmt.Errorf("increment post count: %w", err)
	}
	return nil
}

func (s *Store) UpdateLastSeen(ctx context.Context, userID int64, t time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE users SET last_seen_at = ? WHERE id = ?",
		t.UTC().Format("2006-01-02 15:04:05"), userID)
	if err != nil {
		return fmt.Errorf("update last seen: %w", err)
	}
	return nil
}

func (s *Store) BanUser(ctx context.Context, userID int64, reason string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE users SET banned = 1, ban_reason = ? WHERE id = ?", reason, userID)
	if err != nil {
		return fmt.Errorf("ban user: %w", err)
	}
	return nil
}

func (s *Store) UnbanUser(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE users SET banned = 0, ban_reason = NULL WHERE id = ?", userID)
	if err != nil {
		return fmt.Errorf("unban user: %w", err)
	}
	return nil
}

// nullStr converts an empty string to sql.NullString{Valid:false}.
func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// boolInt converts a bool to 0/1 for SQLite INTEGER columns.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
