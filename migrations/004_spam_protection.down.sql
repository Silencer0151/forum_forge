-- 004_spam_protection.down.sql: drop the held_for_review column and its index.

DROP INDEX IF EXISTS idx_posts_held_for_review;
ALTER TABLE posts DROP COLUMN held_for_review;
