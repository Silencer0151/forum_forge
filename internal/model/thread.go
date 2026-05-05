package model

import "time"

type Thread struct {
	ID            int64     `json:"id"`
	SubcategoryID int64     `json:"subcategory_id"`
	AuthorID      int64     `json:"author_id"`
	Title         string    `json:"title"`
	IsPinned      bool      `json:"is_pinned"`
	IsLocked      bool      `json:"is_locked"`
	ViewCount     int       `json:"view_count"`
	ReplyCount    int       `json:"reply_count"`
	LastPostAt    time.Time `json:"last_post_at"`
	LastPostBy    *int64    `json:"last_post_by,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
