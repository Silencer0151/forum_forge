package sqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
)

// ToggleReaction inserts a reaction if it does not exist, or deletes it if it does.
// Returns added=true when the reaction was inserted, false when it was removed.
func (s *Store) ToggleReaction(ctx context.Context, postID, userID int64, reactionType model.ReactionType) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		"DELETE FROM reactions WHERE post_id = ? AND user_id = ? AND type = ?",
		postID, userID, string(reactionType),
	)
	if err != nil {
		return false, fmt.Errorf("toggle reaction delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("toggle reaction rows affected: %w", err)
	}
	if n > 0 {
		// Row existed and was removed.
		return false, nil
	}
	// Row did not exist; insert it.
	_, err = s.db.ExecContext(ctx,
		"INSERT INTO reactions (post_id, user_id, type) VALUES (?, ?, ?)",
		postID, userID, string(reactionType),
	)
	if err != nil {
		return false, fmt.Errorf("toggle reaction insert: %w", err)
	}
	return true, nil
}

// GetReactionCounts returns the count of each reaction type on the given post.
func (s *Store) GetReactionCounts(ctx context.Context, postID int64) ([]store.ReactionCount, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT type, COUNT(*) FROM reactions WHERE post_id = ? GROUP BY type",
		postID,
	)
	if err != nil {
		return nil, fmt.Errorf("get reaction counts: %w", err)
	}
	defer rows.Close()

	var counts []store.ReactionCount
	for rows.Next() {
		var rc store.ReactionCount
		var t string
		if err := rows.Scan(&t, &rc.Count); err != nil {
			return nil, fmt.Errorf("scan reaction count: %w", err)
		}
		rc.Type = model.ReactionType(t)
		counts = append(counts, rc)
	}
	return counts, rows.Err()
}

// GetUserReactionsForPosts returns a map of postID → reaction types for the given user across the given post IDs.
func (s *Store) GetUserReactionsForPosts(ctx context.Context, userID int64, postIDs []int64) (map[int64][]model.ReactionType, error) {
	if len(postIDs) == 0 {
		return map[int64][]model.ReactionType{}, nil
	}

	placeholders := strings.Repeat("?,", len(postIDs))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, 0, len(postIDs)+1)
	args = append(args, userID)
	for _, id := range postIDs {
		args = append(args, id)
	}

	rows, err := s.db.QueryContext(ctx,
		"SELECT post_id, type FROM reactions WHERE user_id = ? AND post_id IN ("+placeholders+")",
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("get user reactions for posts: %w", err)
	}
	defer rows.Close()

	result := make(map[int64][]model.ReactionType)
	for rows.Next() {
		var postID int64
		var t string
		if err := rows.Scan(&postID, &t); err != nil {
			return nil, fmt.Errorf("scan user reaction: %w", err)
		}
		result[postID] = append(result[postID], model.ReactionType(t))
	}
	return result, rows.Err()
}
