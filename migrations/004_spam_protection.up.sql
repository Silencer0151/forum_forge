-- 004_spam_protection.up.sql: hold-for-review flag for posts that trip the
-- admin-defined keyword blocklist (Task 8.2). Held posts are visible only to
-- their author and moderators until reviewed.

ALTER TABLE posts ADD COLUMN held_for_review INTEGER NOT NULL DEFAULT 0;

CREATE INDEX idx_posts_held_for_review ON posts (held_for_review) WHERE held_for_review = 1;
