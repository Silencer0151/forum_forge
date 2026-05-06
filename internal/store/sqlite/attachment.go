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

const attachmentCols = `id, post_id, filename, storage_path, content_type, size_bytes, created_at`

func scanAttachment(row rowScanner) (*model.Attachment, error) {
	var a model.Attachment
	var createdAt dbTime

	err := row.Scan(
		&a.ID, &a.PostID, &a.Filename, &a.StoragePath,
		&a.ContentType, &a.SizeBytes, &createdAt,
	)
	if err != nil {
		return nil, err
	}
	a.CreatedAt = createdAt.T
	return &a, nil
}

func (s *Store) CreateAttachment(ctx context.Context, a *model.Attachment) error {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO attachments (post_id, filename, storage_path, content_type, size_bytes, created_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		a.PostID, a.Filename, a.StoragePath, a.ContentType, a.SizeBytes,
	)
	if err != nil {
		return fmt.Errorf("create attachment: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("create attachment last id: %w", err)
	}
	a.ID = id
	return nil
}

func (s *Store) GetAttachmentByID(ctx context.Context, id int64) (*model.Attachment, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+attachmentCols+" FROM attachments WHERE id = ?", id)
	a, err := scanAttachment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return a, err
}

func (s *Store) ListAttachmentsByPostID(ctx context.Context, postID int64) ([]*model.Attachment, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+attachmentCols+" FROM attachments WHERE post_id = ? ORDER BY created_at ASC",
		postID,
	)
	if err != nil {
		return nil, fmt.Errorf("list attachments: %w", err)
	}
	defer rows.Close()

	var items []*model.Attachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, fmt.Errorf("list attachments scan: %w", err)
		}
		items = append(items, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list attachments rows: %w", err)
	}
	return items, nil
}

func (s *Store) DeleteAttachment(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM attachments WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete attachment: %w", err)
	}
	return nil
}

// DeleteOrphanedAttachments removes attachments older than olderThan whose post no longer exists.
// With ON DELETE CASCADE on post_id, true orphans cannot occur under normal operation.
func (s *Store) DeleteOrphanedAttachments(ctx context.Context, olderThan time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM attachments
		 WHERE created_at < ? AND post_id NOT IN (SELECT id FROM posts)`,
		olderThan.UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		return fmt.Errorf("delete orphaned attachments: %w", err)
	}
	return nil
}
