-- 001_initial.up.sql: Full initial schema for the forum.
-- SQLite pragmas are set by the migration runner, not here (see internal/database/migrate.go).

CREATE TABLE users (
    id              INTEGER PRIMARY KEY,
    username        TEXT NOT NULL,
    email           TEXT NOT NULL,
    password_hash   TEXT NOT NULL DEFAULT '',
    display_name    TEXT NOT NULL DEFAULT '',
    avatar_url      TEXT NOT NULL DEFAULT '',
    signature       TEXT NOT NULL DEFAULT '',
    role            TEXT NOT NULL DEFAULT 'member',
    post_count      INTEGER NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    banned          INTEGER NOT NULL DEFAULT 0,
    ban_reason      TEXT,
    external_id     TEXT,
    external_source TEXT,
    UNIQUE (username),
    UNIQUE (email)
);

CREATE TABLE categories (
    id            INTEGER PRIMARY KEY,
    name          TEXT NOT NULL,
    slug          TEXT NOT NULL UNIQUE,
    description   TEXT NOT NULL DEFAULT '',
    display_order INTEGER NOT NULL DEFAULT 0,
    is_public     INTEGER NOT NULL DEFAULT 1,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE subcategories (
    id            INTEGER PRIMARY KEY,
    category_id   INTEGER NOT NULL REFERENCES categories(id) ON DELETE RESTRICT,
    name          TEXT NOT NULL,
    slug          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    display_order INTEGER NOT NULL DEFAULT 0,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (category_id, slug)
);

CREATE TABLE threads (
    id             INTEGER PRIMARY KEY,
    subcategory_id INTEGER NOT NULL REFERENCES subcategories(id) ON DELETE RESTRICT,
    author_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    title          TEXT NOT NULL,
    is_pinned      INTEGER NOT NULL DEFAULT 0,
    is_locked      INTEGER NOT NULL DEFAULT 0,
    view_count     INTEGER NOT NULL DEFAULT 0,
    reply_count    INTEGER NOT NULL DEFAULT 0,
    last_post_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_post_by   INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE posts (
    id          INTEGER PRIMARY KEY,
    thread_id   INTEGER NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    author_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    parent_id   INTEGER REFERENCES posts(id) ON DELETE SET NULL,
    body        TEXT NOT NULL DEFAULT '',
    body_format TEXT NOT NULL DEFAULT 'markdown',
    edit_count  INTEGER NOT NULL DEFAULT 0,
    edited_at   DATETIME,
    edited_by   INTEGER REFERENCES users(id) ON DELETE SET NULL,
    is_deleted  INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE reactions (
    id         INTEGER PRIMARY KEY,
    post_id    INTEGER NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type       TEXT NOT NULL DEFAULT 'like',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (post_id, user_id, type)
);

CREATE TABLE private_messages (
    id                INTEGER PRIMARY KEY,
    sender_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject           TEXT NOT NULL DEFAULT '',
    body              TEXT NOT NULL DEFAULT '',
    is_read           INTEGER NOT NULL DEFAULT 0,
    sender_deleted    INTEGER NOT NULL DEFAULT 0,
    recipient_deleted INTEGER NOT NULL DEFAULT 0,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE attachments (
    id           INTEGER PRIMARY KEY,
    post_id      INTEGER NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    filename     TEXT NOT NULL DEFAULT '',
    storage_path TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT '',
    size_bytes   INTEGER NOT NULL DEFAULT 0,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE reports (
    id          INTEGER PRIMARY KEY,
    reporter_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    post_id     INTEGER NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    reason      TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'open',
    reviewed_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    reviewed_at DATETIME
);

CREATE TABLE drafts (
    id             INTEGER PRIMARY KEY,
    user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    thread_id      INTEGER REFERENCES threads(id) ON DELETE CASCADE,
    subcategory_id INTEGER REFERENCES subcategories(id) ON DELETE CASCADE,
    title          TEXT,
    body           TEXT NOT NULL DEFAULT '',
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Sessions: id is a random token, data is serialized session payload.
CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    data       BLOB,
    expires_at DATETIME NOT NULL
);

-- Settings: admin-configurable key-value pairs (see model.Settings).
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);

-- ── Indexes (spec.md §2.2) ─────────────────────────────────────────────────

-- Thread listing: subcategory page, pinned first then by bump order.
CREATE INDEX idx_threads_listing ON threads (subcategory_id, is_pinned DESC, last_post_at DESC);

-- Post listing within a thread (chronological).
CREATE INDEX idx_posts_thread ON posts (thread_id, created_at ASC);

-- Nested reply lookups.
CREATE INDEX idx_posts_parent ON posts (parent_id);

-- User post history page.
CREATE INDEX idx_posts_author ON posts (author_id, created_at DESC);

-- Login and profile lookups by username.
CREATE INDEX idx_users_username ON users (username);

-- Login lookup by email.
CREATE INDEX idx_users_email ON users (email);

-- Kanoogi integration: external user lookups.
CREATE INDEX idx_users_external ON users (external_id, external_source);

-- Mod queue: open reports sorted by date.
CREATE INDEX idx_reports_status ON reports (status, created_at);

-- One reply draft per (user, thread).
CREATE UNIQUE INDEX idx_drafts_user_thread
    ON drafts (user_id, thread_id)
    WHERE thread_id IS NOT NULL;

-- One new-thread draft per (user, subcategory).
CREATE UNIQUE INDEX idx_drafts_user_subcategory
    ON drafts (user_id, subcategory_id)
    WHERE subcategory_id IS NOT NULL;

-- ── Full-Text Search (spec.md §9.2) ───────────────────────────────────────

-- FTS5 external content table: stores index only; content comes from posts.
CREATE VIRTUAL TABLE posts_fts USING fts5 (
    title,
    body,
    content='posts',
    content_rowid='id',
    tokenize='porter unicode61'
);

-- Keep posts_fts in sync with the posts table.

CREATE TRIGGER posts_fts_insert AFTER INSERT ON posts BEGIN
    INSERT INTO posts_fts (rowid, title, body)
    SELECT NEW.id, t.title, NEW.body FROM threads t WHERE t.id = NEW.thread_id;
END;

CREATE TRIGGER posts_fts_delete BEFORE DELETE ON posts BEGIN
    INSERT INTO posts_fts (posts_fts, rowid, title, body)
    VALUES ('delete', OLD.id, (SELECT title FROM threads WHERE id = OLD.thread_id), OLD.body);
END;

CREATE TRIGGER posts_fts_update AFTER UPDATE ON posts BEGIN
    INSERT INTO posts_fts (posts_fts, rowid, title, body)
    VALUES ('delete', OLD.id, (SELECT title FROM threads WHERE id = OLD.thread_id), OLD.body);
    INSERT INTO posts_fts (rowid, title, body)
    SELECT NEW.id, t.title, NEW.body FROM threads t WHERE t.id = NEW.thread_id;
END;
