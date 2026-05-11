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

const postCols = `id, thread_id, author_id, parent_id, body, body_format,
	edit_count, edited_at, edited_by, is_deleted, held_for_review, created_at, updated_at`

func scanPost(row rowScanner) (*model.Post, error) {
	var p model.Post
	var parentID, editedBy sql.NullInt64
	var editedAt dbNullTime
	var createdAt, updatedAt dbTime
	var isDeleted, heldForReview int

	err := row.Scan(
		&p.ID, &p.ThreadID, &p.AuthorID, &parentID,
		&p.Body, &p.BodyFormat,
		&p.EditCount, &editedAt, &editedBy,
		&isDeleted, &heldForReview, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	if parentID.Valid {
		p.ParentID = &parentID.Int64
	}
	if editedBy.Valid {
		p.EditedBy = &editedBy.Int64
	}
	p.EditedAt = editedAt.T
	p.IsDeleted = isDeleted != 0
	p.HeldForReview = heldForReview != 0
	p.CreatedAt = createdAt.T
	p.UpdatedAt = updatedAt.T
	return &p, nil
}

func scanPostWithAuthor(rows *sql.Rows) (*store.PostWithAuthor, error) {
	var p model.Post
	var u model.User
	var parentID, editedBy sql.NullInt64
	var editedAt dbNullTime
	var createdAt, updatedAt, userCreatedAt, userLastSeenAt dbTime
	var isDeleted, banned int
	var banReason, externalID, externalSource sql.NullString

	err := rows.Scan(
		&p.ID, &p.ThreadID, &p.AuthorID, &parentID,
		&p.Body, &p.BodyFormat, &p.EditCount, &editedAt, &editedBy,
		&isDeleted, &createdAt, &updatedAt,
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.AvatarURL, &u.Signature, &u.Role, &u.PostCount,
		&userCreatedAt, &userLastSeenAt, &banned, &banReason,
		&externalID, &externalSource,
	)
	if err != nil {
		return nil, err
	}
	if parentID.Valid {
		p.ParentID = &parentID.Int64
	}
	if editedBy.Valid {
		p.EditedBy = &editedBy.Int64
	}
	p.EditedAt = editedAt.T
	p.IsDeleted = isDeleted != 0
	p.CreatedAt = createdAt.T
	p.UpdatedAt = updatedAt.T
	u.Banned = banned != 0
	u.CreatedAt = userCreatedAt.T
	u.LastSeenAt = userLastSeenAt.T
	u.BanReason = banReason.String
	u.ExternalID = externalID.String
	u.ExternalSource = externalSource.String
	return &store.PostWithAuthor{Post: &p, Author: &u}, nil
}

// nullInt64 converts a *int64 to sql.NullInt64.
func nullInt64(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func (s *Store) CreatePost(ctx context.Context, p *model.Post) error {
	if p.ParentID != nil {
		// Enforce max 2 nesting levels: reject if the parent post is already a
		// nested reply (i.e. parent.parent_id IS NOT NULL AND grandparent.parent_id IS NOT NULL).
		var ggParentID sql.NullInt64
		err := s.db.QueryRowContext(ctx, `
			SELECT pp.parent_id
			FROM posts p
			JOIN posts pp ON pp.id = p.parent_id
			WHERE p.id = ?`, *p.ParentID).Scan(&ggParentID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check nesting depth: %w", err)
		}
		if ggParentID.Valid {
			return fmt.Errorf("create post: maximum reply depth exceeded")
		}
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO posts (thread_id, author_id, parent_id, body, body_format)
		VALUES (?, ?, ?, ?, ?)`,
		p.ThreadID, p.AuthorID, nullInt64(p.ParentID), p.Body, string(p.BodyFormat),
	)
	if err != nil {
		return fmt.Errorf("create post: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("create post last id: %w", err)
	}
	p.ID = id

	if err := s.UpdateLastPost(ctx, p.ThreadID, p.AuthorID, time.Now().UTC()); err != nil {
		return fmt.Errorf("create post update thread: %w", err)
	}
	return nil
}

func (s *Store) GetPostByID(ctx context.Context, id int64) (*model.Post, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+postCols+" FROM posts WHERE id = ?", id)
	p, err := scanPost(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return p, err
}

func (s *Store) ListPostsByThread(ctx context.Context, threadID int64, page store.PageRequest) (*store.PageResult[*store.PostWithAuthor], error) {
	perPage := page.PerPage
	if perPage <= 0 {
		perPage = 25
	}
	pg := page.Page
	if pg <= 0 {
		pg = 1
	}
	offset := (pg - 1) * perPage

	var total int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM posts WHERE thread_id = ?", threadID,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("count posts by thread: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			p.id, p.thread_id, p.author_id, p.parent_id,
			p.body, p.body_format, p.edit_count, p.edited_at, p.edited_by,
			p.is_deleted, p.created_at, p.updated_at,
			u.id, u.username, u.email, u.password_hash, u.display_name,
			u.avatar_url, u.signature, u.role, u.post_count,
			u.created_at, u.last_seen_at, u.banned, u.ban_reason,
			u.external_id, u.external_source
		FROM posts p
		JOIN users u ON u.id = p.author_id
		WHERE p.thread_id = ?
		ORDER BY p.created_at ASC
		LIMIT ? OFFSET ?`,
		threadID, perPage, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list posts by thread: %w", err)
	}
	defer rows.Close()

	return collectPostsWithAuthor(rows, total, pg, perPage)
}

func (s *Store) ListPostsByAuthor(ctx context.Context, userID int64, page store.PageRequest) (*store.PageResult[*store.PostWithAuthor], error) {
	perPage := page.PerPage
	if perPage <= 0 {
		perPage = 25
	}
	pg := page.Page
	if pg <= 0 {
		pg = 1
	}
	offset := (pg - 1) * perPage

	var total int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM posts WHERE author_id = ? AND is_deleted = 0", userID,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("count posts by author: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			p.id, p.thread_id, p.author_id, p.parent_id,
			p.body, p.body_format, p.edit_count, p.edited_at, p.edited_by,
			p.is_deleted, p.created_at, p.updated_at,
			u.id, u.username, u.email, u.password_hash, u.display_name,
			u.avatar_url, u.signature, u.role, u.post_count,
			u.created_at, u.last_seen_at, u.banned, u.ban_reason,
			u.external_id, u.external_source
		FROM posts p
		JOIN users u ON u.id = p.author_id
		WHERE p.author_id = ? AND p.is_deleted = 0
		ORDER BY p.created_at DESC
		LIMIT ? OFFSET ?`,
		userID, perPage, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list posts by author: %w", err)
	}
	defer rows.Close()

	return collectPostsWithAuthor(rows, total, pg, perPage)
}

func collectPostsWithAuthor(rows *sql.Rows, total, pg, perPage int) (*store.PageResult[*store.PostWithAuthor], error) {
	var items []*store.PostWithAuthor
	for rows.Next() {
		pwa, err := scanPostWithAuthor(rows)
		if err != nil {
			return nil, fmt.Errorf("scan post with author: %w", err)
		}
		items = append(items, pwa)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	totalPages := (total + perPage - 1) / perPage
	if totalPages == 0 {
		totalPages = 1
	}
	return &store.PageResult[*store.PostWithAuthor]{
		Items:      items,
		Total:      total,
		Page:       pg,
		TotalPages: totalPages,
	}, nil
}

func (s *Store) UpdatePost(ctx context.Context, p *model.Post) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE posts SET
			body = ?, body_format = ?,
			edit_count = edit_count + 1,
			edited_at = CURRENT_TIMESTAMP,
			edited_by = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		p.Body, string(p.BodyFormat), nullInt64(p.EditedBy), p.ID,
	)
	if err != nil {
		return fmt.Errorf("update post: %w", err)
	}
	return nil
}

// DeletePost soft-deletes a post by setting is_deleted=1 and recording who deleted it.
func (s *Store) DeletePost(ctx context.Context, postID, deletedBy int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE posts SET
			is_deleted = 1,
			edited_by = ?,
			edited_at = CURRENT_TIMESTAMP,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		deletedBy, postID,
	)
	if err != nil {
		return fmt.Errorf("delete post: %w", err)
	}
	return nil
}
