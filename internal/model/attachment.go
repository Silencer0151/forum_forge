package model

import "time"

type Attachment struct {
	ID          int64     `json:"id"`
	PostID      int64     `json:"post_id"`
	Filename    string    `json:"filename"`
	StoragePath string    `json:"storage_path"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
}
