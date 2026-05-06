package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
)

const pmCols = `id, sender_id, recipient_id, subject, body, is_read, sender_deleted, recipient_deleted, created_at`

func scanPM(row rowScanner) (*model.PrivateMessage, error) {
	var pm model.PrivateMessage
	var isRead, senderDeleted, recipientDeleted int
	var createdAt dbTime

	err := row.Scan(
		&pm.ID, &pm.SenderID, &pm.RecipientID,
		&pm.Subject, &pm.Body,
		&isRead, &senderDeleted, &recipientDeleted,
		&createdAt,
	)
	if err != nil {
		return nil, err
	}
	pm.IsRead = isRead != 0
	pm.SenderDeleted = senderDeleted != 0
	pm.RecipientDeleted = recipientDeleted != 0
	pm.CreatedAt = createdAt.T
	return &pm, nil
}

func (s *Store) CreatePM(ctx context.Context, pm *model.PrivateMessage) error {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO private_messages
			(sender_id, recipient_id, subject, body, is_read, sender_deleted, recipient_deleted, created_at)
		VALUES (?, ?, ?, ?, 0, 0, 0, CURRENT_TIMESTAMP)`,
		pm.SenderID, pm.RecipientID, pm.Subject, pm.Body,
	)
	if err != nil {
		return fmt.Errorf("create pm: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("create pm last id: %w", err)
	}
	pm.ID = id
	return nil
}

func (s *Store) GetPMByID(ctx context.Context, id int64) (*model.PrivateMessage, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+pmCols+" FROM private_messages WHERE id = ?", id)
	pm, err := scanPM(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return pm, err
}

func (s *Store) ListInbox(ctx context.Context, recipientID int64, page store.PageRequest) (*store.PageResult[*model.PrivateMessage], error) {
	offset := (page.Page - 1) * page.PerPage

	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM private_messages WHERE recipient_id = ? AND recipient_deleted = 0`,
		recipientID,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("list inbox count: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		"SELECT "+pmCols+` FROM private_messages
		 WHERE recipient_id = ? AND recipient_deleted = 0
		 ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		recipientID, page.PerPage, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list inbox: %w", err)
	}
	defer rows.Close()

	var items []*model.PrivateMessage
	for rows.Next() {
		pm, err := scanPM(rows)
		if err != nil {
			return nil, fmt.Errorf("list inbox scan: %w", err)
		}
		items = append(items, pm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list inbox rows: %w", err)
	}

	totalPages := 0
	if total > 0 {
		totalPages = (total + page.PerPage - 1) / page.PerPage
	}
	return &store.PageResult[*model.PrivateMessage]{
		Items:      items,
		Total:      total,
		Page:       page.Page,
		TotalPages: totalPages,
	}, nil
}

func (s *Store) MarkPMRead(ctx context.Context, pmID int64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE private_messages SET is_read = 1 WHERE id = ?", pmID)
	if err != nil {
		return fmt.Errorf("mark pm read: %w", err)
	}
	return nil
}

func (s *Store) DeletePMForSender(ctx context.Context, pmID int64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE private_messages SET sender_deleted = 1 WHERE id = ?", pmID)
	if err != nil {
		return fmt.Errorf("delete pm for sender: %w", err)
	}
	return nil
}

func (s *Store) DeletePMForRecipient(ctx context.Context, pmID int64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE private_messages SET recipient_deleted = 1 WHERE id = ?", pmID)
	if err != nil {
		return fmt.Errorf("delete pm for recipient: %w", err)
	}
	return nil
}
