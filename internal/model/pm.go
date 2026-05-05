package model

import "time"

type PrivateMessage struct {
	ID               int64     `json:"id"`
	SenderID         int64     `json:"sender_id"`
	RecipientID      int64     `json:"recipient_id"`
	Subject          string    `json:"subject"`
	Body             string    `json:"body"`
	IsRead           bool      `json:"is_read"`
	SenderDeleted    bool      `json:"sender_deleted"`
	RecipientDeleted bool      `json:"recipient_deleted"`
	CreatedAt        time.Time `json:"created_at"`
}
