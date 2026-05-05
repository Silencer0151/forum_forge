package model

import "time"

type Draft struct {
	ID            int64     `json:"id"`
	UserID        int64     `json:"user_id"`
	ThreadID      *int64    `json:"thread_id,omitempty"`
	SubcategoryID *int64    `json:"subcategory_id,omitempty"`
	Title         string    `json:"title,omitempty"`
	Body          string    `json:"body"`
	UpdatedAt     time.Time `json:"updated_at"`
}
