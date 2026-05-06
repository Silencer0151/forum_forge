-- 002_auth_tokens.down.sql: drop the auth_tokens table and the email_verified
-- column added in 002_auth_tokens.up.sql.

DROP INDEX IF EXISTS idx_auth_tokens_expires_at;
DROP INDEX IF EXISTS idx_auth_tokens_user_type;
DROP TABLE IF EXISTS auth_tokens;

ALTER TABLE users DROP COLUMN email_verified;
