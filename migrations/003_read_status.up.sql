-- 003_read_status.up.sql: per-user thread read tracking (Task 8.4).
-- One row per (user, thread) recording when the user last viewed the thread.
-- Threads with last_post_at > last_read_at are rendered as "unread" in listings.
-- Guests have no rows here, so for them every thread implicitly appears read.

CREATE TABLE read_status (
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    thread_id    INTEGER NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    last_read_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, thread_id)
);

CREATE INDEX idx_read_status_thread ON read_status (thread_id);
