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

const draftCols = `id, user_id, thread_id, subcategory_id, title, body, updated_at`

func scanDraft(row rowScanner) (*model.Draft, error) {
	var d model.Draft
	var threadID, subcategoryID sql.NullInt64
	var title sql.NullString
	var updatedAt dbTime

	err := row.Scan(
		&d.ID, &d.UserID, &threadID, &subcategoryID, &title, &d.Body, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	if threadID.Valid {
		d.ThreadID = &threadID.Int64
	}
	if subcategoryID.Valid {
		d.SubcategoryID = &subcategoryID.Int64
	}
	d.Title = title.String
	d.UpdatedAt = updatedAt.T
	return &d, nil
}

func (s *Store) UpsertDraft(ctx context.Context, d *model.Draft) error {
	threadID := nullInt64(d.ThreadID)
	subcategoryID := nullInt64(d.SubcategoryID)
	title := nullStr(d.Title)

	var id int64
	var err error

	switch {
	case d.ThreadID != nil:
		err = s.db.QueryRowContext(ctx, `
			INSERT INTO drafts (user_id, thread_id, subcategory_id, title, body, updated_at)
			VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(user_id, thread_id) WHERE thread_id IS NOT NULL
			DO UPDATE SET title = excluded.title, body = excluded.body, updated_at = CURRENT_TIMESTAMP
			RETURNING id`,
			d.UserID, threadID, subcategoryID, title, d.Body,
		).Scan(&id)
	case d.SubcategoryID != nil:
		err = s.db.QueryRowContext(ctx, `
			INSERT INTO drafts (user_id, thread_id, subcategory_id, title, body, updated_at)
			VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(user_id, subcategory_id) WHERE subcategory_id IS NOT NULL
			DO UPDATE SET title = excluded.title, body = excluded.body, updated_at = CURRENT_TIMESTAMP
			RETURNING id`,
			d.UserID, threadID, subcategoryID, title, d.Body,
		).Scan(&id)
	default:
		var res sql.Result
		res, err = s.db.ExecContext(ctx,
			`INSERT INTO drafts (user_id, title, body, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`,
			d.UserID, title, d.Body,
		)
		if err == nil {
			id, err = res.LastInsertId()
		}
	}

	if err != nil {
		return fmt.Errorf("upsert draft: %w", err)
	}
	d.ID = id
	return nil
}

func (s *Store) GetDraftByThread(ctx context.Context, userID, threadID int64) (*model.Draft, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+draftCols+" FROM drafts WHERE user_id = ? AND thread_id = ?",
		userID, threadID,
	)
	d, err := scanDraft(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return d, err
}

func (s *Store) GetDraftBySubcategory(ctx context.Context, userID, subcategoryID int64) (*model.Draft, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+draftCols+" FROM drafts WHERE user_id = ? AND subcategory_id = ?",
		userID, subcategoryID,
	)
	d, err := scanDraft(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return d, err
}

func (s *Store) DeleteDraft(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM drafts WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete draft: %w", err)
	}
	return nil
}

func (s *Store) CleanupOldDrafts(ctx context.Context, olderThan time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM drafts WHERE updated_at < ?",
		olderThan.UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		return fmt.Errorf("cleanup old drafts: %w", err)
	}
	return nil
}
