# Forum System — v1 Specification

## 1. Project Overview

A classic-leaning, text-first discussion forum with modern conveniences, built as a standalone system designed for easy integration into existing platforms — with **Kanoogi** (kanoogi.com) as the primary integration target.

The forum serves niche gaming/tech communities that value clarity, structure, and usability over flashy UI. It emphasizes simplicity, speed, and reliability.

**Stack:** Go backend, HTMX-enhanced server-rendered frontend, SQLite database.

### 1.1 Motivation & Context

Kanoogi is a cloud gaming platform created by Chris Taylor (of Supreme Commander / Total Annihilation fame). The platform currently has a simple threaded comment system under blog-style update posts. Chris has expressed interest in breaking discussions out into a proper forum for broader community conversation. This forum is designed to fill that need — as a standalone project that can be proposed for integration into Kanoogi, or used independently by any community.

### 1.2 Key Design Goals

- **Standalone-first, integration-ready:** Works out of the box with its own auth, but exposes clean integration points (auth adapter, theme system, embeddable routes) for platforms like Kanoogi.
- **Classic forum feel:** Thread lists, post counts, user profiles with join dates and signatures — the things that made forums feel like communities.
- **Minimal operational burden:** Single binary + SQLite file. No Redis, no message queue, no container orchestration required for v1.
- **Text-first, fast, accessible:** Server-rendered HTML. HTMX for progressive enhancement. Works without JavaScript (degraded but functional).

---

## 2. Data Model

### 2.1 Entities

```
User
├── id              (UUID or int64, primary key)
├── username        (unique, 3-32 chars, alphanumeric + underscore)
├── email           (unique, used for auth in standalone mode)
├── password_hash   (bcrypt, standalone mode only)
├── display_name    (optional, shown instead of username)
├── avatar_url      (URL string — local upload or pulled from external platform)
├── signature       (optional, max 256 chars, plaintext or limited markdown)
├── role            (enum: admin, moderator, member, guest)
├── post_count      (denormalized counter)
├── created_at      (join date)
├── last_seen_at
├── banned          (bool)
├── ban_reason      (text, nullable)
├── external_id     (nullable — maps to Kanoogi user ID for integrated mode)
└── external_source (nullable — e.g. "kanoogi")

Category
├── id
├── name
├── slug            (URL-friendly, unique)
├── description
├── display_order   (int, for manual sorting)
├── is_public       (bool — guests can browse if true)
└── created_at

Subcategory
├── id
├── category_id     (FK → Category)
├── name
├── slug            (unique within parent category)
├── description
├── display_order
└── created_at

Thread
├── id
├── subcategory_id  (FK → Subcategory)
├── author_id       (FK → User)
├── title           (max 200 chars)
├── is_pinned       (bool)
├── is_locked       (bool)
├── view_count      (denormalized counter)
├── reply_count     (denormalized counter)
├── last_post_at    (denormalized timestamp for bump ordering)
├── last_post_by    (FK → User, nullable)
├── created_at
└── updated_at

Post
├── id
├── thread_id       (FK → Thread)
├── author_id       (FK → User)
├── parent_id       (FK → Post, nullable — for nested replies, max 2 levels)
├── body            (text, rendered from markdown/bbcode/plaintext)
├── body_format     (enum: markdown, bbcode, plaintext)
├── edit_count      (int)
├── edited_at       (nullable)
├── edited_by       (FK → User, nullable — tracks mod edits vs self-edits)
├── is_deleted      (soft delete)
├── created_at
└── updated_at

Reaction
├── id
├── post_id         (FK → Post)
├── user_id         (FK → User)
├── type            (enum: like — keep it simple for v1)
└── created_at
-- unique constraint on (post_id, user_id, type)

PrivateMessage
├── id
├── sender_id       (FK → User)
├── recipient_id    (FK → User)
├── subject         (max 200 chars)
├── body
├── is_read         (bool)
├── sender_deleted  (bool — soft delete per-side)
├── recipient_deleted (bool)
└── created_at

Attachment
├── id
├── post_id         (FK → Post)
├── filename        (original filename)
├── storage_path    (path on disk or object storage key)
├── content_type    (MIME type)
├── size_bytes      (int64)
└── created_at

Report
├── id
├── reporter_id     (FK → User)
├── post_id         (FK → Post)
├── reason          (text)
├── status          (enum: open, reviewed, dismissed)
├── reviewed_by     (FK → User, nullable)
├── created_at
└── reviewed_at

Draft
├── id
├── user_id         (FK → User)
├── thread_id       (FK → Thread, nullable — null for new thread drafts)
├── subcategory_id  (FK → Subcategory, nullable — for new thread drafts)
├── title           (nullable — for new thread drafts)
├── body            (text)
├── updated_at
-- unique constraint on (user_id, thread_id) or (user_id, subcategory_id) for new threads
```

### 2.2 Indexing Strategy

These indexes are critical for forum performance at scale:

- `threads(subcategory_id, is_pinned DESC, last_post_at DESC)` — thread listing page
- `posts(thread_id, created_at ASC)` — post listing within a thread
- `posts(parent_id)` — nested reply lookups
- `posts(author_id, created_at DESC)` — user post history
- `users(username)` — login and profile lookups
- `users(email)` — login
- `users(external_id, external_source)` — Kanoogi integration lookups
- `reactions(post_id, user_id, type)` — unique constraint + fast toggle check
- `reports(status, created_at)` — mod queue

---

## 3. Roles & Permissions

### 3.1 Role Definitions

| Action | Guest | Member | Moderator | Admin |
|---|---|---|---|---|
| Browse public categories | ✓ | ✓ | ✓ | ✓ |
| Browse restricted categories | — | ✓ | ✓ | ✓ |
| Create threads | — | ✓ | ✓ | ✓ |
| Reply to threads | — | ✓ | ✓ | ✓ |
| Edit own posts | — | ✓ (within edit window) | ✓ | ✓ |
| Delete own posts | — | — | — | ✓ |
| React to posts | — | ✓ | ✓ | ✓ |
| Send private messages | — | ✓ | ✓ | ✓ |
| Report posts | — | ✓ | ✓ | ✓ |
| Upload attachments | — | ✓ | ✓ | ✓ |
| Edit any post | — | — | ✓ | ✓ |
| Delete any post | — | — | ✓ | ✓ |
| Lock/unlock threads | — | — | ✓ | ✓ |
| Pin/unpin threads | — | — | ✓ | ✓ |
| Ban/unban users | — | — | ✓ | ✓ |
| Review reports | — | — | ✓ | ✓ |
| Manage categories | — | — | — | ✓ |
| Manage roles | — | — | — | ✓ |
| Site settings | — | — | — | ✓ |

### 3.2 Configurable Settings (Admin)

- `edit_window_minutes` — how long members can edit their own posts (default: 30, 0 = unlimited)
- `posts_per_page` — pagination size (default: 20)
- `threads_per_page` — pagination size (default: 25)
- `max_attachment_size_bytes` — per-file limit (default: 5MB)
- `allowed_attachment_types` — MIME type whitelist (default: images + pdf + zip)
- `rate_limit_posts_per_minute` — per-user (default: 3)
- `rate_limit_threads_per_hour` — per-user (default: 5)
- `require_captcha_until_post_count` — new user CAPTCHA threshold (default: 5)

---

## 4. Authentication & Authorization

### 4.1 Standalone Mode (Default)

- **Registration:** Email + password. Email verification via confirmation link.
- **Login:** Email + password → bcrypt compare → session cookie.
- **Sessions:** Secure, HttpOnly cookie containing a session ID. Session data stored server-side (SQLite table or in-memory with persistence).
- **Password Reset:** Email-based reset link with time-limited token (1 hour expiry).
- **CAPTCHA:** On registration and on posts until `require_captcha_until_post_count` threshold is met.

### 4.2 Integrated Mode (Kanoogi)

When deployed as part of an existing platform, the forum supports an **auth adapter interface:**

```go
type AuthAdapter interface {
    // Authenticate validates an incoming request and returns the external user
    // info if authenticated. Returns nil if not authenticated (guest).
    Authenticate(r *http.Request) (*ExternalUser, error)

    // GetUser fetches user info by external platform ID.
    GetUser(externalID string) (*ExternalUser, error)

    // GetAvatarURL returns the avatar URL for a given external user.
    GetAvatarURL(externalID string) (string, error)
}

type ExternalUser struct {
    ExternalID   string
    Username     string
    DisplayName  string
    AvatarURL    string
    Email        string
}
```

In integrated mode:
- The forum skips its own login/registration pages.
- On each request, the auth adapter is called to resolve the current user from Kanoogi's session/cookie/token.
- If the external user doesn't have a local forum `User` row yet, one is created automatically (auto-provisioning) with `role = member`.
- Avatar URLs are pulled from the external platform and cached/synced periodically.
- The forum's own password fields are left null.

A config flag `auth_mode` toggles between `"standalone"` and `"integrated"`.

### 4.3 Future: OAuth

Not in v1 scope, but the auth adapter pattern makes adding Google/GitHub OAuth straightforward later.

---

## 5. Backend Architecture

### 5.1 Stack

| Component | Choice | Rationale |
|---|---|---|
| Language | Go | Performance, single binary, stdlib HTTP |
| Router | `net/http` (Go 1.22+ with method routing) | Zero dependencies for routing; Chi if needed |
| Database | SQLite via `mattn/go-sqlite3` or `modernc.org/sqlite` | Single file, zero ops, good to ~10K concurrent readers |
| Migrations | `golang-migrate` | Well-supported, SQL-file based |
| Templates | `html/template` | Stdlib, secure by default |
| Sessions | Cookie ID + `sessions` table in SQLite | Simple, no Redis needed |
| Password hashing | `golang.org/x/crypto/bcrypt` | Industry standard |
| Markdown | `goldmark` | Fast, extensible, CommonMark compliant |
| BBCode | `frustra/bbcode` or custom parser | Optional, classic forum flavor |
| File uploads | Local disk (configurable path) | S3-compatible adapter can be added later |
| Logging | `log/slog` (structured) | Stdlib, JSON output for production |
| Config | Environment variables + optional TOML/YAML file | 12-factor friendly |

### 5.2 Project Layout

```
forum/
├── cmd/
│   └── forum/
│       └── main.go              # Entry point, config loading, server startup
├── internal/
│   ├── config/                  # Config struct, env/file loading
│   ├── auth/
│   │   ├── adapter.go           # AuthAdapter interface
│   │   ├── standalone.go        # Email+password implementation
│   │   └── kanoogi.go           # Kanoogi integration adapter (stub/example)
│   ├── handler/
│   │   ├── thread.go            # Thread CRUD + listing
│   │   ├── post.go              # Post CRUD, reactions, quoting
│   │   ├── user.go              # Profile, settings, avatar
│   │   ├── auth.go              # Login, register, logout, password reset
│   │   ├── moderation.go        # Mod actions, report queue
│   │   ├── category.go          # Category/subcategory management
│   │   ├── pm.go                # Private messages
│   │   ├── search.go            # Search
│   │   └── admin.go             # Admin settings panel
│   ├── middleware/
│   │   ├── auth.go              # Session resolution, user injection into context
│   │   ├── ratelimit.go         # Per-user and per-IP rate limiting
│   │   ├── csrf.go              # CSRF token middleware
│   │   └── proxy.go             # X-Forwarded-* trust, secure headers (HSTS)
│   ├── model/                   # Data structs (User, Thread, Post, etc.)
│   ├── store/
│   │   ├── store.go             # Store interface
│   │   ├── sqlite.go            # SQLite implementation
│   │   └── queries/             # Raw SQL files or embedded SQL
│   ├── render/
│   │   ├── render.go            # Template rendering helpers
│   │   └── markdown.go          # Markdown/BBCode → HTML pipeline
│   └── upload/
│       └── upload.go            # File upload handling, validation, storage
├── migrations/
│   ├── 001_initial.up.sql
│   ├── 001_initial.down.sql
│   └── ...
├── templates/
│   ├── layout/
│   │   ├── base.html            # Outer shell (head, nav, footer)
│   │   ├── nav.html             # Navigation partial
│   │   └── footer.html
│   ├── pages/
│   │   ├── home.html            # Category listing (forum index)
│   │   ├── category.html        # Subcategory listing
│   │   ├── subcategory.html     # Thread listing
│   │   ├── thread.html          # Post listing within thread
│   │   ├── new_thread.html      # New thread form
│   │   ├── profile.html         # User profile page
│   │   ├── login.html
│   │   ├── register.html
│   │   ├── search.html
│   │   ├── pm_inbox.html
│   │   ├── pm_thread.html
│   │   └── mod_queue.html       # Report queue for mods
│   └── partials/
│       ├── post.html            # Single post (reused in thread + HTMX swap)
│       ├── post_form.html       # Reply/edit form partial
│       ├── thread_row.html      # Single thread row in listing
│       ├── pagination.html
│       ├── reaction_button.html
│       └── flash.html           # Flash message partial
├── static/
│   ├── css/
│   │   └── style.css            # Single CSS file, custom properties for theming
│   ├── js/
│   │   ├── htmx.min.js
│   │   └── alpine.min.js        # Optional, for small UI behaviors
│   └── img/
│       └── default_avatar.png
├── uploads/                     # User-uploaded files (gitignored)
├── certs/                       # autocert cache directory (gitignored)
├── data/
│   └── forum.db                 # SQLite database (gitignored)
├── go.mod
├── go.sum
├── Makefile
├── Dockerfile                   # Optional, for Postgres deployment path
├── docker-compose.yml           # Optional, Postgres + app
└── README.md
```

### 5.3 API Routes

All routes return server-rendered HTML. HTMX-targeted routes return HTML partials (detected via `HX-Request` header).

```
GET  /                                  → Forum index (category listing)
GET  /c/{category_slug}                 → Subcategory listing
GET  /c/{category_slug}/{sub_slug}      → Thread listing (paginated)
GET  /t/{thread_id}                     → Thread view (paginated posts)
POST /t/{thread_id}/reply               → Create reply (HTMX: append post partial)
GET  /t/new?sub={sub_slug}              → New thread form
POST /t/new                             → Create thread → redirect to thread

GET  /p/{post_id}/edit                  → Edit form partial (HTMX)
PUT  /p/{post_id}                       → Update post (HTMX: swap post partial)
POST /p/{post_id}/react                 → Toggle reaction (HTMX: swap button)
POST /p/{post_id}/quote                 → Returns quoted text for reply box (HTMX)
POST /p/{post_id}/report                → Submit report
DELETE /p/{post_id}                     → Soft delete (mod/admin only)

GET  /u/{username}                      → User profile
GET  /u/{username}/posts                → User's post history
GET  /settings                          → User settings (avatar, signature, password)
POST /settings                          → Update settings

GET  /pm                                → PM inbox
GET  /pm/{pm_id}                        → Read message
POST /pm/new                            → Send message

GET  /search?q={query}                  → Search results (HTMX partial for live results)

POST /auth/login                        → Login (standalone mode)
POST /auth/register                     → Register (standalone mode)
POST /auth/logout                       → Logout
GET  /auth/verify?token={token}         → Email verification
POST /auth/forgot-password              → Request reset
POST /auth/reset-password               → Reset with token

GET  /mod/reports                       → Report queue
POST /mod/reports/{id}/review           → Mark report reviewed/dismissed
POST /mod/threads/{id}/lock             → Lock thread
POST /mod/threads/{id}/pin              → Pin thread
POST /mod/users/{id}/ban                → Ban user

GET  /admin/settings                    → Admin settings panel
POST /admin/settings                    → Update settings
GET  /admin/categories                  → Category management
POST /admin/categories                  → CRUD categories/subcategories
```

### 5.4 Database: SQLite Configuration

SQLite needs specific pragmas for web server use:

```sql
PRAGMA journal_mode = WAL;          -- concurrent readers + one writer
PRAGMA busy_timeout = 5000;         -- wait up to 5s on lock contention
PRAGMA synchronous = NORMAL;        -- safe with WAL, better performance
PRAGMA foreign_keys = ON;
PRAGMA cache_size = -64000;         -- 64MB cache
```

### 5.5 Postgres Migration Path

The store interface (`store.Store`) abstracts all data access. A `postgres.go` implementation can be added alongside `sqlite.go` without changing handlers. Migration files should use SQL compatible with both SQLite and Postgres where possible, with dialect-specific files where needed (e.g., `001_initial.up.sqlite.sql` / `001_initial.up.postgres.sql`).

When Postgres is desired:
- Switch DSN in config
- Use `docker-compose.yml` for Postgres + app
- No application code changes needed

---

## 6. Frontend Architecture

### 6.1 Rendering Model

Server-rendered HTML templates with HTMX for progressive enhancement. The forum must be **fully functional without JavaScript** — HTMX enhances the experience but all actions have `<form>` fallbacks with full page reloads.

### 6.2 HTMX Interactions

| Feature | HTMX Behavior | Fallback |
|---|---|---|
| Reply to thread | `hx-post` appends post partial below thread | Form POST → redirect to thread |
| Edit post | `hx-get` swaps post with edit form, `hx-put` swaps back | Link to edit page → redirect |
| Reactions | `hx-post` swaps reaction button/count | Form POST → redirect |
| Pagination | `hx-get` swaps thread/post list | Standard `?page=N` links |
| Quote | `hx-post` injects quoted text into reply textarea | Pre-filled form field |
| Search | `hx-get` with `hx-trigger="keyup changed delay:300ms"` | Standard search form |
| Delete post | `hx-delete` swaps post with "[deleted]" placeholder | Form POST → redirect |
| Report | `hx-post` with modal/inline form → success message | Form POST → redirect |
| Draft save | `hx-post` with `hx-trigger="keyup changed delay:2000ms"` to `/drafts` | localStorage fallback via small Alpine.js snippet |

### 6.3 JavaScript Policy

- **Required:** `htmx.min.js` (~14KB gzipped)
- **Optional:** `alpine.min.js` (~15KB gzipped) for: draft auto-save localStorage fallback, character counters on signature/title fields, dropdown menus, confirm dialogs on destructive actions
- **No other JS frameworks.** No build step. No bundler. No npm.

### 6.4 CSS Architecture

Single `style.css` file using CSS custom properties for theming. No preprocessor, no build step.

```css
:root {
    /* Override these for platform integration */
    --ff-bg-primary:      #1a1125;    /* Deep purple-black (from Kanoogi) */
    --ff-bg-secondary:    #241a30;    /* Slightly lighter card background */
    --ff-bg-tertiary:     #2d2139;    /* Hover states, input backgrounds */
    --ff-border:          #3d2f4a;    /* Subtle borders */
    --ff-text-primary:    #e8e0f0;   /* Light text */
    --ff-text-secondary:  #a89bb8;   /* Muted text (timestamps, counts) */
    --ff-accent:          #d4940a;    /* Orange/gold (from Kanoogi headings) */
    --ff-accent-hover:    #e8a820;    /* Lighter gold on hover */
    --ff-accent-subtle:   #4a3a20;   /* Subtle gold tint for backgrounds */
    --ff-link:            #d4940a;    /* Links match accent */
    --ff-danger:          #c0392b;    /* Delete, ban actions */
    --ff-success:         #27ae60;    /* Success messages */
    --ff-code-bg:         #0d0a12;    /* Code block background */
    --ff-avatar-size:     48px;
    --ff-avatar-radius:   6px;       /* Slightly rounded, not circular (matching Kanoogi) */
    --ff-font-body:       system-ui, -apple-system, sans-serif;
    --ff-font-mono:       'Fira Code', 'Cascadia Code', monospace;
    --ff-max-width:       1200px;
    --ff-spacing-unit:    8px;
}
```

All `--ff-*` variables can be overridden by a host platform's CSS to re-skin the forum without modifying source. Kanoogi integration would override these to match its exact palette.

---

## 7. Visual Design & UX

### 7.1 Design Language (Kanoogi-Aligned)

The forum's default theme is designed to harmonize with Kanoogi's existing aesthetic:

- **Background:** Deep purple-black, not pure black. Warm undertone.
- **Cards/Containers:** Slightly lighter background with subtle borders. No heavy drop shadows — rely on background contrast.
- **Typography:** Clean sans-serif body text in light cream/lavender. Orange-gold for headings, links, and interactive elements. Monospace for code blocks.
- **Accent color:** Orange/gold (`#d4940a` range) used sparingly for headings, links, active states, and interactive affordances. Not used for large fills.
- **Navigation:** Simple horizontal text links, highlighted current section. Consistent with Kanoogi's `Home | About | Join Beta | Updates | Library` bar — the forum adds a "Forum" entry.
- **Avatars:** Small (48px), slightly rounded rectangles (not circles), matching Kanoogi's comment section style.
- **Spacing:** Generous but not wasteful. Text-first layout with clear hierarchy via size and color, not heavy dividers.

### 7.2 Key Pages

**Forum Index (`/`):**
- List of categories, each showing its subcategories beneath.
- Each subcategory shows: name, description, thread count, post count, last post info (title snippet, author, timestamp).
- Pinned announcements section at top if any exist.

**Thread Listing (`/c/{cat}/{sub}`):**
- Table-like rows: thread title, author, reply count, view count, last reply info.
- Pinned threads at top with visual indicator.
- Locked threads show lock icon.
- Pagination at top and bottom.
- "New Thread" button (members+).

**Thread View (`/t/{id}`):**
- Posts displayed chronologically.
- Each post shows: author card (avatar, username, role badge, post count, join date, signature) on the left/top, post body on the right/below, action bar (quote, react, edit, report, delete) at the bottom.
- Nested replies (max 2 levels) indented beneath parent.
- Reply form at the bottom (or inline via HTMX).
- Breadcrumb navigation: Forum → Category → Subcategory → Thread Title.

**User Profile (`/u/{username}`):**
- Avatar (large), display name, username, role badge, join date, last seen, post count.
- Signature preview.
- Recent posts list.
- PM button (if logged in and not self).
- Mod actions (ban button) if viewer is mod/admin.

### 7.3 Responsive Design

- Desktop: two-column post layout (author card left, post body right).
- Tablet/mobile: single-column, author info condensed to inline header above post body.
- Navigation collapses to hamburger menu on mobile.
- No separate mobile app — responsive web only.

---

## 8. Thread & Post Features

### 8.1 Post Content

- **Supported formats:** Markdown (default), BBCode, plaintext. Users select format per-post; admin can restrict available formats.
- **Markdown rendering:** CommonMark via `goldmark`. Extensions: tables, strikethrough, autolinks, task lists.
- **Code blocks:** Fenced code blocks with syntax highlighting via a lightweight client-side highlighter (e.g., Prism.js, ~2KB per language) or server-side via `chroma`.
- **Link auto-embedding:** URLs automatically become clickable links. Image URLs optionally render inline (configurable). YouTube/video embedding is post-v1.
- **Quoting:** Click "Quote" on a post → the quoted text (with attribution) is inserted into the reply form as a blockquote. Format: `> **Username wrote:**\n> quoted text\n\n`
- **XSS prevention:** All user content is sanitized server-side after Markdown/BBCode rendering. Use `bluemonday` for HTML sanitization with a strict policy (no raw HTML, no script tags, no event handlers).

### 8.2 Thread Behavior

- **Bumping:** Threads sort by `last_post_at` (descending) within their subcategory. New reply updates this timestamp.
- **Pinning:** Pinned threads always appear at the top of the listing, sorted by pin order.
- **Locking:** Locked threads can be viewed but not replied to. Mods/admins can still post in locked threads.
- **Soft delete:** Deleted posts show "[This post has been removed]" with mod attribution. Thread is soft-deleted if all posts are deleted.

### 8.3 Pagination

- Posts within a thread: chronological, configurable page size (default 20).
- Threads within a subcategory: by `last_post_at` descending (pinned first), configurable page size (default 25).
- Cursor-based pagination internally, rendered as page numbers in the UI.
- Direct link to specific post: `/t/{thread_id}?page=3#post-{post_id}`

### 8.4 Attachments

- Uploaded via multipart form (with HTMX, inline preview before submit).
- Stored on local disk under `uploads/{year}/{month}/{uuid}.{ext}`.
- Served via a dedicated `/attachments/{id}/{filename}` route (not directly from disk path).
- Images render inline in the post. Other files show as download links with filename and size.
- Limits: max file size (default 5MB), allowed MIME types (configurable), max attachments per post (default 5).

### 8.5 Drafts

- Auto-saved server-side via HTMX (`hx-post="/drafts"` on keyup with 2-second debounce).
- Fallback: localStorage auto-save via Alpine.js if JS is available but server save fails.
- One draft per (user, thread) for replies, one per (user, subcategory) for new threads.
- Drafts are deleted when the post is submitted.
- Drafts older than 30 days are automatically cleaned up.

---

## 9. Search

### 9.1 v1: Simple Search

- Search across thread titles and post bodies.
- SQLite FTS5 virtual table for performant full-text search.
- Endpoint: `GET /search?q={query}&page={page}`
- Results show: thread title, matching post snippet with highlighted terms, author, timestamp.
- HTMX live search with debounced keyup (300ms delay).

### 9.2 FTS5 Setup

```sql
CREATE VIRTUAL TABLE posts_fts USING fts5(
    title,       -- thread title (denormalized for search)
    body,        -- post body (plaintext, stripped of markup)
    content='posts',
    content_rowid='id',
    tokenize='porter unicode61'
);
```

Triggers to keep FTS5 in sync with the posts table on INSERT/UPDATE/DELETE.

---

## 10. Moderation & Safety

### 10.1 Moderation Tools

- **Delete post:** Soft delete, post replaced with "[removed by {mod_name}]"
- **Lock thread:** Prevents new replies (except by mods/admins)
- **Pin/unpin thread:** Moves thread to top of listing
- **Ban user:** Sets `banned = true`, records reason. Banned users see a "you are banned" message on login. All active sessions are invalidated.
- **Edit post:** Mods can edit any post. Edit history shows "edited by {mod_name}" distinct from author edits.
- **Report queue:** `/mod/reports` shows open reports, sortable by date. Mods can review → mark resolved or dismissed.

### 10.2 Spam Protection

- **Rate limiting:** Per-user and per-IP, configurable. Implemented as middleware using a simple in-memory sliding window (no Redis needed for v1 scale). Defaults: 3 posts/min, 5 threads/hour, 10 PMs/hour.
- **New user restrictions:** CAPTCHA required on registration and on all posts until `post_count >= require_captcha_until_post_count` (default 5). Use a simple self-hosted CAPTCHA or hCaptcha.
- **Link limits:** Posts by users with `post_count < 10` are limited to 2 URLs per post.
- **Keyword filter:** Admin-configurable blocklist. Posts containing blocked keywords are held for mod review (not auto-deleted).

---

## 11. Performance & Caching

### 11.1 Target Scale

- ~1000 registered users
- ~100 concurrent readers
- ~10 concurrent writers
- SQLite in WAL mode handles this comfortably

### 11.2 Caching Strategy

- **In-process cache:** `sync.Map` or a simple LRU for: rendered post HTML (keyed by post ID + edit timestamp), user profile data, category/subcategory listings.
- **Cache invalidation:** On write, invalidate relevant cache entries. Keep it simple — no distributed cache coordination.
- **Template caching:** Parse templates once at startup, not per-request.
- **Static assets:** Serve with far-future `Cache-Control` headers, use content-hash in filenames for cache busting.

### 11.3 Denormalization

Several counters are denormalized for performance (avoiding `COUNT(*)` on every page load):
- `user.post_count` — incremented on post creation
- `thread.reply_count` — incremented on reply
- `thread.view_count` — incremented on thread view (debounced, not on every request)
- `thread.last_post_at` / `thread.last_post_by` — updated on new reply

---

## 12. Deployment

### 12.1 TLS Strategy

The forum supports three TLS modes, selected via `FORUM_TLS_MODE`:

**Mode 1: `autocert` (default for standalone)**

Go-native automatic TLS using `golang.org/x/crypto/acme/autocert`. The binary handles certificate provisioning, renewal, and HTTPS termination itself — no nginx, no Caddy, no certbot, no cron jobs.

```go
// Simplified implementation in cmd/forum/main.go
m := &autocert.Manager{
    Cache:      autocert.DirCache("./certs"),      // persists certs to disk
    Prompt:     autocert.AcceptTOS,
    HostPolicy: autocert.HostWhitelist("forum.example.com"),
}

srv := &http.Server{
    Addr:      ":443",
    Handler:   forumRouter,
    TLSConfig: m.TLSConfig(),
}

// HTTP :80 listener for ACME challenges + redirect to HTTPS
go http.ListenAndServe(":80", m.HTTPHandler(
    http.RedirectHandler("https://forum.example.com", http.StatusMovedPermanently),
))

srv.ListenAndServeTLS("", "") // certs managed by autocert
```

Requirements: port 80 and 443 open, a valid public domain pointing at the server. Certs are cached in `FORUM_TLS_CERT_DIR` (default `./certs/`) and auto-renewed before expiry.

```bash
# Run with autocert — this is the entire deployment
FORUM_TLS_MODE=autocert \
FORUM_TLS_DOMAINS=forum.example.com \
FORUM_TLS_CERT_DIR=./certs \
FORUM_BASE_URL=https://forum.example.com \
./forum
```

**Mode 2: `manual` (bring your own certs)**

For environments where you already have certs (corporate CA, wildcard cert, etc.):

```bash
FORUM_TLS_MODE=manual \
FORUM_TLS_CERT_FILE=/etc/ssl/forum/cert.pem \
FORUM_TLS_KEY_FILE=/etc/ssl/forum/key.pem \
FORUM_LISTEN_ADDR=:443 \
FORUM_BASE_URL=https://forum.example.com \
./forum
```

Go's `http.Server.ListenAndServeTLS(certFile, keyFile)` handles this directly. Cert rotation on renewal can be handled by watching the file for changes or by restarting the process (systemd `ExecReload` or similar).

**Mode 3: `none` (plaintext HTTP, proxy-terminated TLS)**

For deployments behind a reverse proxy (nginx, Caddy, cloud load balancer, or Kanoogi's own proxy) that terminates TLS upstream:

```bash
FORUM_TLS_MODE=none \
FORUM_LISTEN_ADDR=:8080 \
FORUM_BASE_URL=https://forum.example.com \
FORUM_TRUST_PROXY=true \
./forum
```

When `FORUM_TRUST_PROXY=true`, the forum:
- Trusts `X-Forwarded-For` for client IP (rate limiting)
- Trusts `X-Forwarded-Proto` for scheme detection (secure cookie decisions)
- Sets `Secure` flag on session cookies based on `FORUM_BASE_URL` scheme, not the listener scheme

### 12.2 TLS Hardening

Regardless of mode, when the forum terminates TLS itself (autocert or manual), the TLS config enforces:

```go
tlsConfig := &tls.Config{
    MinVersion:               tls.VersionTLS12,
    PreferServerCipherSuites: true,
    CurvePreferences:         []tls.CurveID{tls.X25519, tls.CurveP256},
}
```

Additionally:
- All HTTP responses include `Strict-Transport-Security: max-age=63072000; includeSubDomains` when served over HTTPS
- Session cookies are always set with `Secure; HttpOnly; SameSite=Lax` when `FORUM_BASE_URL` uses `https://`
- HTTP → HTTPS redirect is automatic in `autocert` mode (via the :80 listener)
- In `manual` mode, an optional `FORUM_REDIRECT_HTTP=true` spins up a :80 → :443 redirect listener

### 12.3 Primary Deployment: Single Binary + SQLite

```bash
# Build
go build -o forum ./cmd/forum

# Run with automatic HTTPS (zero external dependencies)
FORUM_TLS_MODE=autocert \
FORUM_TLS_DOMAINS=forum.example.com \
FORUM_DB_PATH=./data/forum.db \
FORUM_UPLOAD_PATH=./uploads \
FORUM_BASE_URL=https://forum.example.com \
./forum
```

That's the entire deployment. Single binary, automatic certs, SQLite file. Throw it on any VPS with ports 80+443 open.

### 12.4 Optional: Docker Compose (Postgres)

```yaml
version: "3.8"
services:
  forum:
    build: .
    environment:
      FORUM_DB_DRIVER: postgres
      FORUM_DB_DSN: postgres://forum:secret@db:5432/forum?sslmode=disable
      FORUM_TLS_MODE: autocert
      FORUM_TLS_DOMAINS: forum.example.com
      FORUM_BASE_URL: https://forum.example.com
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - certs:/app/certs
    depends_on:
      - db
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: forum
      POSTGRES_PASSWORD: secret
      POSTGRES_DB: forum
    volumes:
      - pgdata:/var/lib/postgresql/data
volumes:
  pgdata:
  certs:
```

### 12.5 Kanoogi Integration Deployment

When embedded in Kanoogi, the forum runs as either:

1. **Separate service behind Kanoogi's proxy** — Forum runs with `FORUM_TLS_MODE=none` on an internal port. Kanoogi's existing TLS-terminating proxy (whatever it uses) routes `/forum/*` to the forum service. The forum trusts `X-Forwarded-*` headers from the proxy. Session cookies are scoped to Kanoogi's domain so the auth adapter can read Kanoogi's session. This works regardless of Kanoogi's tech stack.

2. **Library import** — Kanoogi's Go backend imports the forum as a Go module and mounts its routes under `/forum/`. TLS is entirely Kanoogi's concern — the forum is just a `http.Handler`. The auth adapter reads Kanoogi's session directly from the request context. Cleanest option but requires Kanoogi's backend to be Go (or CGo-compatible).

3. **Separate domain** — Forum runs on its own domain (e.g., `forum.kanoogi.com`) with `FORUM_TLS_MODE=autocert`. Auth integration happens via the auth adapter calling Kanoogi's API to validate session tokens. Requires CORS configuration and cross-domain cookie handling (or token-based auth). More operational overhead but fully decoupled.

---

## 13. Testing

### 13.1 Unit Tests

- Store layer: test all CRUD operations against an in-memory SQLite database.
- Rendering: test Markdown/BBCode → HTML pipeline, sanitization.
- Auth: test password hashing, session management, token generation.
- Rate limiter: test window behavior, per-user vs per-IP.

### 13.2 Integration Tests

- Full HTTP tests using `httptest.Server`:
  - Registration → email verification → login → create thread → reply → edit → delete
  - Guest access: can browse public, denied on restricted
  - Mod flow: report → review → ban
  - Rate limiting: verify 429 after threshold
  - Search: index → query → results
  - HTMX partial responses: verify correct partial HTML returned when `HX-Request` header is present

### 13.3 CI/CD

- `make test` — runs all tests
- `make lint` — `golangci-lint`
- `make build` — produces binary
- GitHub Actions or similar: lint → test → build on every push

---

## 14. Configuration Reference

All configuration via environment variables, with optional TOML file override:

```
FORUM_DB_DRIVER          sqlite | postgres                 (default: sqlite)
FORUM_DB_PATH            path to SQLite file               (default: ./data/forum.db)
FORUM_DB_DSN             Postgres connection string        (only if driver=postgres)
FORUM_LISTEN_ADDR        listen address                    (default: :443 if TLS, :8080 if none)
FORUM_BASE_URL           public URL                        (default: http://localhost:8080)
FORUM_UPLOAD_PATH        file upload directory             (default: ./uploads)
FORUM_AUTH_MODE          standalone | integrated           (default: standalone)
FORUM_SESSION_SECRET     HMAC key for session cookies      (required, generated if absent)
FORUM_TLS_MODE           autocert | manual | none          (default: none)
FORUM_TLS_DOMAINS        comma-separated domain whitelist  (autocert mode, required)
FORUM_TLS_CERT_DIR       autocert cache directory          (default: ./certs)
FORUM_TLS_CERT_FILE      path to TLS certificate PEM      (manual mode)
FORUM_TLS_KEY_FILE       path to TLS private key PEM      (manual mode)
FORUM_REDIRECT_HTTP      spin up :80 → :443 redirect      (default: true if TLS, ignored if none)
FORUM_TRUST_PROXY        trust X-Forwarded-* headers       (default: false, set true behind proxy)
FORUM_SMTP_HOST          SMTP server for emails            (standalone mode)
FORUM_SMTP_PORT          SMTP port                         (default: 587)
FORUM_SMTP_USER          SMTP username
FORUM_SMTP_PASS          SMTP password
FORUM_SMTP_FROM          sender email address
FORUM_CAPTCHA_PROVIDER   none | hcaptcha                   (default: none)
FORUM_CAPTCHA_SITEKEY    hCaptcha site key
FORUM_CAPTCHA_SECRET     hCaptcha secret key
FORUM_LOG_LEVEL          debug | info | warn | error       (default: info)
FORUM_LOG_FORMAT         text | json                       (default: text)
```

---

## 15. Roadmap (Post-v1)

- Full-text search improvements (faceted, filters by author/date/category)
- Rich link previews (Open Graph scraping)
- WebSocket live updates (new posts appear in real-time)
- User mentions (`@username`) with notifications
- Notification system (in-app + optional email digest)
- Shadowbanning (posts visible only to author)
- OAuth providers (Google, GitHub, Discord)
- Theme customization (admin panel for CSS variables)
- Mobile-optimized progressive web app
- Emoji picker for reactions (expand beyond like/upvote)
- Thread tags/labels
- Poll support
- User reputation/trust levels
- RSS feeds per category/subcategory
- API endpoint layer (JSON) for third-party clients

---

## Appendix A: Kanoogi Design Reference

Visual properties observed from Kanoogi (kanoogi.com) for theme alignment:

| Element | Observation |
|---|---|
| Background | Deep purple-black, warm undertone, not pure `#000` |
| Card backgrounds | Slightly lighter than page bg, subtle 1px borders |
| Heading text | Orange/gold, italic serif-like font for taglines |
| Body text | Light cream/white |
| Muted text | Lavender-gray for timestamps, metadata |
| Navigation | Horizontal text links, orange highlight on active |
| Avatars | Small squares with slight rounding (~6px radius) |
| Comment layout | Threaded with indentation, avatar left, text right |
| Overall feel | Dark, warm, gaming-oriented, clean but not sterile |

## Appendix B: Integration Checklist for Kanoogi

If Chris Taylor wants to integrate this into Kanoogi, these are the concrete steps:

1. **Implement the `AuthAdapter` interface** for Kanoogi's session system
2. **Override CSS custom properties** (`--ff-*`) to match Kanoogi's exact palette
3. **Mount the forum routes** under `/forum/` in Kanoogi's routing
4. **Add "Forum" to the nav bar** alongside Home, About, Join Beta, Updates, Library
5. **Map existing Kanoogi user profiles** to forum users via `external_id`
6. **Configure avatar sync** — point `GetAvatarURL` at Kanoogi's existing avatar storage
7. **Optional:** Migrate existing update comments into forum threads if desired
