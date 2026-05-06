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

const threadCols = `id, subcategory_id, author_id, title, is_pinned, is_locked,
	view_count, reply_count, last_post_at, last_post_by, created_at, updated_at`

func scanThread(row rowScanner) (*model.Thread, error) {
	var t model.Thread
	var isPinned, isLocked int
	var lastPostAt, createdAt, updatedAt dbTime
	var lastPostBy sql.NullInt64

	err := row.Scan(
		&t.ID, &t.SubcategoryID, &t.AuthorID, &t.Title,
		&isPinned, &isLocked,
		&t.ViewCount, &t.ReplyCount,
		&lastPostAt, &lastPostBy,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	t.IsPinned = isPinned != 0
	t.IsLocked = isLocked != 0
	t.LastPostAt = lastPostAt.T
	t.CreatedAt = createdAt.T
	t.UpdatedAt = updatedAt.T
	if lastPostBy.Valid {
		t.LastPostBy = &lastPostBy.Int64
	}
	return &t, nil
}

func scanThreadWithMeta(rows *sql.Rows) (*store.ThreadWithMeta, error) {
	var t model.Thread
	var isPinned, isLocked int
	var lastPostAt, createdAt, updatedAt dbTime
	var lastPostBy sql.NullInt64
	var authorUsername, authorAvatarURL string
	var lastPostUsername sql.NullString

	err := rows.Scan(
		&t.ID, &t.SubcategoryID, &t.AuthorID, &t.Title,
		&isPinned, &isLocked,
		&t.ViewCount, &t.ReplyCount,
		&lastPostAt, &lastPostBy,
		&createdAt, &updatedAt,
		&authorUsername, &authorAvatarURL,
		&lastPostUsername,
	)
	if err != nil {
		return nil, err
	}
	t.IsPinned = isPinned != 0
	t.IsLocked = isLocked != 0
	t.LastPostAt = lastPostAt.T
	t.CreatedAt = createdAt.T
	t.UpdatedAt = updatedAt.T
	if lastPostBy.Valid {
		t.LastPostBy = &lastPostBy.Int64
	}

	meta := &store.ThreadWithMeta{
		Thread:          &t,
		AuthorUsername:  authorUsername,
		AuthorAvatarURL: authorAvatarURL,
	}
	if lastPostUsername.Valid {
		meta.LastPostUsername = &lastPostUsername.String
	}
	return meta, nil
}

func (s *Store) CreateThread(ctx context.Context, t *model.Thread) error {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO threads
			(subcategory_id, author_id, title, is_pinned, is_locked,
			 view_count, reply_count, last_post_at, last_post_by)
		VALUES (?, ?, ?, ?, ?, 0, 0, CURRENT_TIMESTAMP, NULL)`,
		t.SubcategoryID, t.AuthorID, t.Title,
		boolInt(t.IsPinned), boolInt(t.IsLocked),
	)
	if err != nil {
		return fmt.Errorf("create thread: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("create thread last id: %w", err)
	}
	t.ID = id
	return nil
}

func (s *Store) GetThreadByID(ctx context.Context, id int64) (*model.Thread, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+threadCols+" FROM threads WHERE id = ?", id)
	t, err := scanThread(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return t, err
}

func (s *Store) ListThreads(ctx context.Context, opts store.ThreadListOptions) (*store.PageResult[*store.ThreadWithMeta], error) {
	perPage := opts.Page.PerPage
	if perPage <= 0 {
		perPage = 25
	}
	page := opts.Page.Page
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * perPage

	var total int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM threads WHERE subcategory_id = ?",
		opts.SubcategoryID,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("count threads: %w", err)
	}

	orderClause := "t.last_post_at DESC"
	if opts.PinnedFirst {
		orderClause = "t.is_pinned DESC, t.last_post_at DESC"
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			t.id, t.subcategory_id, t.author_id, t.title,
			t.is_pinned, t.is_locked, t.view_count, t.reply_count,
			t.last_post_at, t.last_post_by, t.created_at, t.updated_at,
			u.username, u.avatar_url,
			lpu.username
		FROM threads t
		JOIN users u ON u.id = t.author_id
		LEFT JOIN users lpu ON lpu.id = t.last_post_by
		WHERE t.subcategory_id = ?
		ORDER BY `+orderClause+`
		LIMIT ? OFFSET ?`,
		opts.SubcategoryID, perPage, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list threads: %w", err)
	}
	defer rows.Close()

	var items []*store.ThreadWithMeta
	for rows.Next() {
		meta, err := scanThreadWithMeta(rows)
		if err != nil {
			return nil, fmt.Errorf("scan thread: %w", err)
		}
		items = append(items, meta)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	totalPages := (total + perPage - 1) / perPage
	if totalPages == 0 {
		totalPages = 1
	}
	return &store.PageResult[*store.ThreadWithMeta]{
		Items:      items,
		Total:      total,
		Page:       page,
		TotalPages: totalPages,
	}, nil
}

func (s *Store) UpdateThread(ctx context.Context, t *model.Thread) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE threads SET
			subcategory_id = ?, title = ?, is_pinned = ?, is_locked = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		t.SubcategoryID, t.Title, boolInt(t.IsPinned), boolInt(t.IsLocked), t.ID,
	)
	if err != nil {
		return fmt.Errorf("update thread: %w", err)
	}
	return nil
}

func (s *Store) IncrementViewCount(ctx context.Context, threadID int64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE threads SET view_count = view_count + 1 WHERE id = ?", threadID)
	if err != nil {
		return fmt.Errorf("increment view count: %w", err)
	}
	return nil
}

func (s *Store) UpdateLastPost(ctx context.Context, threadID, userID int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE threads SET
			last_post_at = ?,
			last_post_by = ?,
			reply_count  = reply_count + 1,
			updated_at   = CURRENT_TIMESTAMP
		WHERE id = ?`,
		at.UTC().Format("2006-01-02 15:04:05"), userID, threadID,
	)
	if err != nil {
		return fmt.Errorf("update last post: %w", err)
	}
	return nil
}

func (s *Store) PinThread(ctx context.Context, threadID int64, pinned bool) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE threads SET is_pinned = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		boolInt(pinned), threadID,
	)
	if err != nil {
		return fmt.Errorf("pin thread: %w", err)
	}
	return nil
}

func (s *Store) LockThread(ctx context.Context, threadID int64, locked bool) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE threads SET is_locked = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		boolInt(locked), threadID,
	)
	if err != nil {
		return fmt.Errorf("lock thread: %w", err)
	}
	return nil
}

// DeleteThread hard-deletes a thread and its posts (via CASCADE).
// The schema has no is_deleted column for threads, so this is a permanent removal.
func (s *Store) DeleteThread(ctx context.Context, threadID int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM threads WHERE id = ?", threadID)
	if err != nil {
		return fmt.Errorf("delete thread: %w", err)
	}
	return nil
}
