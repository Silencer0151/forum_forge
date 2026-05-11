package model

import "time"

type BodyFormat string

const (
	BodyFormatMarkdown  BodyFormat = "markdown"
	BodyFormatBBCode    BodyFormat = "bbcode"
	BodyFormatPlaintext BodyFormat = "plaintext"
)

func (f BodyFormat) IsValid() bool {
	switch f {
	case BodyFormatMarkdown, BodyFormatBBCode, BodyFormatPlaintext:
		return true
	}
	return false
}

type Post struct {
	ID         int64      `json:"id"`
	ThreadID   int64      `json:"thread_id"`
	AuthorID   int64      `json:"author_id"`
	ParentID   *int64     `json:"parent_id,omitempty"`
	Body       string     `json:"body"`
	BodyFormat BodyFormat `json:"body_format"`
	EditCount  int        `json:"edit_count"`
	EditedAt   *time.Time `json:"edited_at,omitempty"`
	EditedBy   *int64     `json:"edited_by,omitempty"`
	IsDeleted  bool       `json:"is_deleted"`
	// HeldForReview marks posts that tripped the keyword blocklist; they are
	// hidden from everyone except the author and moderators until reviewed.
	HeldForReview bool      `json:"held_for_review"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
