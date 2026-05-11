-- 003_read_status.down.sql: drop per-user thread read tracking.

DROP INDEX IF EXISTS idx_read_status_thread;
DROP TABLE IF EXISTS read_status;
