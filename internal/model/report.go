package model

import "time"

type ReportStatus string

const (
	ReportStatusOpen      ReportStatus = "open"
	ReportStatusReviewed  ReportStatus = "reviewed"
	ReportStatusDismissed ReportStatus = "dismissed"
)

func (s ReportStatus) IsValid() bool {
	switch s {
	case ReportStatusOpen, ReportStatusReviewed, ReportStatusDismissed:
		return true
	}
	return false
}

type Report struct {
	ID           int64        `json:"id"`
	ReporterID   int64        `json:"reporter_id"`
	PostID       int64        `json:"post_id"`
	Reason       string       `json:"reason"`
	Status       ReportStatus `json:"status"`
	ReviewedBy   *int64       `json:"reviewed_by,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
	ReviewedAt   *time.Time   `json:"reviewed_at,omitempty"`
}
