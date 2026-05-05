package model

import "time"

type ReactionType string

const (
	ReactionTypeLike ReactionType = "like"
)

func (t ReactionType) IsValid() bool {
	return t == ReactionTypeLike
}

type Reaction struct {
	ID        int64        `json:"id"`
	PostID    int64        `json:"post_id"`
	UserID    int64        `json:"user_id"`
	Type      ReactionType `json:"type"`
	CreatedAt time.Time    `json:"created_at"`
}
