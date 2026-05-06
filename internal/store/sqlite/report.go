package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
)

const reportCols = `id, reporter_id, post_id, reason, status, reviewed_by, created_at, reviewed_at`

func scanReport(row rowScanner) (*model.Report, error) {
	var r model.Report
	var reviewedBy sql.NullInt64
	var createdAt dbTime
	var reviewedAt dbNullTime

	err := row.Scan(
		&r.ID, &r.ReporterID, &r.PostID, &r.Reason, &r.Status,
		&reviewedBy, &createdAt, &reviewedAt,
	)
	if err != nil {
		return nil, err
	}
	if reviewedBy.Valid {
		r.ReviewedBy = &reviewedBy.Int64
	}
	r.CreatedAt = createdAt.T
	r.ReviewedAt = reviewedAt.T
	return &r, nil
}

func (s *Store) CreateReport(ctx context.Context, r *model.Report) error {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO reports (reporter_id, post_id, reason, status, created_at)
		VALUES (?, ?, ?, 'open', CURRENT_TIMESTAMP)`,
		r.ReporterID, r.PostID, r.Reason,
	)
	if err != nil {
		return fmt.Errorf("create report: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("create report last id: %w", err)
	}
	r.ID = id
	r.Status = model.ReportStatusOpen
	return nil
}

func (s *Store) GetReportByID(ctx context.Context, id int64) (*model.Report, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+reportCols+" FROM reports WHERE id = ?", id)
	r, err := scanReport(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return r, err
}

func (s *Store) ListOpenReports(ctx context.Context, page store.PageRequest) (*store.PageResult[*model.Report], error) {
	offset := (page.Page - 1) * page.PerPage

	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM reports WHERE status = 'open'`,
	).Scan(&total); err != nil {
		return nil, fmt.Errorf("list open reports count: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		"SELECT "+reportCols+` FROM reports
		 WHERE status = 'open'
		 ORDER BY created_at ASC LIMIT ? OFFSET ?`,
		page.PerPage, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list open reports: %w", err)
	}
	defer rows.Close()

	var items []*model.Report
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, fmt.Errorf("list open reports scan: %w", err)
		}
		items = append(items, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list open reports rows: %w", err)
	}

	totalPages := 0
	if total > 0 {
		totalPages = (total + page.PerPage - 1) / page.PerPage
	}
	return &store.PageResult[*model.Report]{
		Items:      items,
		Total:      total,
		Page:       page.Page,
		TotalPages: totalPages,
	}, nil
}

func (s *Store) ReviewReport(ctx context.Context, reportID, reviewerID int64, status model.ReportStatus) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE reports SET status = ?, reviewed_by = ?, reviewed_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		string(status), reviewerID, reportID,
	)
	if err != nil {
		return fmt.Errorf("review report: %w", err)
	}
	return nil
}
