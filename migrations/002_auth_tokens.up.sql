-- 002_auth_tokens.up.sql: email-verification + password-reset tokens (Task 3.2).
-- Tokens are stored as SHA-256 hashes so the raw value never lives in the DB.
-- A separate column tracks whether the user has verified their email; until
-- they do, the UI shows a banner but login still works (spec.md §4.1).

ALTER TABLE users ADD COLUMN email_verified INTEGER NOT NULL DEFAULT 0;

CREATE TABLE auth_tokens (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type        TEXT    NOT NULL,
    token_hash  TEXT    NOT NULL,
    expires_at  DATETIME NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (token_hash)
);

CREATE INDEX idx_auth_tokens_user_type ON auth_tokens (user_id, type);
CREATE INDEX idx_auth_tokens_expires_at ON auth_tokens (expires_at);
