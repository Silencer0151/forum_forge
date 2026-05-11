package sqlite

import (
	"context"
	"fmt"
	"time"
)

// MarkThreadRead upserts the viewer's last_read_at for the given thread.
// Called from the thread view handler so subsequent listings hide the "NEW" badge.
func (s *Store) MarkThreadRead(ctx context.Context, userID, threadID int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO read_status (user_id, thread_id, last_read_at)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id, thread_id) DO UPDATE SET last_read_at = excluded.last_read_at`,
		userID, threadID, at.UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		return fmt.Errorf("mark thread read: %w", err)
	}
	return nil
}
