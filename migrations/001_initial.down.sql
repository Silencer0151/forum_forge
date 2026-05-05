-- 001_initial.down.sql: Drop all objects created by 001_initial.up.sql.
-- Drop triggers and virtual tables before the tables they depend on,
-- then drop tables in child-before-parent order.

DROP TRIGGER IF EXISTS posts_fts_update;
DROP TRIGGER IF EXISTS posts_fts_delete;
DROP TRIGGER IF EXISTS posts_fts_insert;
DROP TABLE IF EXISTS posts_fts;
DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS drafts;
DROP TABLE IF EXISTS reports;
DROP TABLE IF EXISTS attachments;
DROP TABLE IF EXISTS private_messages;
DROP TABLE IF EXISTS reactions;
DROP TABLE IF EXISTS posts;
DROP TABLE IF EXISTS threads;
DROP TABLE IF EXISTS subcategories;
DROP TABLE IF EXISTS categories;
DROP TABLE IF EXISTS users;
