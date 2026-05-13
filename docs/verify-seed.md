# Seed Verification Checklist

Run `go run ./cmd/seed -force` against a fresh DB (or to overwrite an existing one), then start the server with `go run ./cmd/forum`. Walk this list top to bottom and flag anything that doesn't match. Every demo password is `password123`.

## 1. Home page (logged out)

Visit http://localhost:8080.

- [x] Two categories listed: **General**, **Tech**.
- [x] Under General: two subcategories — **Announcements**, **General Discussion**.
- [X] Under Tech: two subcategories — **Software**, **Hardware**.
- [X] Each subcategory shows non-zero thread/post counts and a "last post" line.
- [x] Nav (top right) shows **Login** and **Register**.
- [X] No "+ New Thread" button anywhere — guests can't post.

## 2. Login / role banners

Login as **admin@forum.test**.

- [X] Nav shows username "admin" with a `▾` dropdown indicator.
- [X] Hovering the username opens the dropdown — Profile, Messages, Settings, **Mod Queue**, **Admin Panel**, Log Out.
- [x] The dropdown stays open as you move the cursor down to click an item (no 4px hover-gap bug).
- [X] Visiting `/u/admin` shows role badge **ADMIN**.

Log out, login as **moderator@forum.test**.

- [X] Dropdown includes **Mod Queue** but NOT **Admin Panel**.
- [X] `/u/moderator` shows role badge **MODERATOR**.

Log out, login as **alice@forum.test**.

- [x] Dropdown has only Profile / Messages / Settings / Log Out (no mod or admin links).
- [O] `/u/alice` shows role badge **MEMBER**, "50 posts", a signature line, and a verified email (no banner).

Log out, login as **dave@forum.test**.

- [x] Profile shows 1 post.
- [O] (Expected open question: is the unverified-email banner actually wired into the UI? The model has `EmailVerified=false` for Dave. If you don't see a banner anywhere, that's a missing feature, not a regression.)

Try to login as **eve@forum.test**.

- [X] Login is rejected with an error (eve is banned).

## 3. Breadcrumbs

While logged in, visit `/settings`.

- [x] Breadcrumb reads `Forum Home › admin › Settings` horizontally, separated by `›`.
- [x] No numbered list (1. / 2. / 3.) and no vertical stacking.

Repeat on `/u/alice`, `/c/general`, `/c/general/general-discussion`, and a thread page — all should render the same horizontal style.

## 4. Subcategory listing — pagination

Login as anyone, visit `/c/general/general-discussion`.

- [X] You see **25 threads** on page 1 (the configured `ThreadsPerPage` default).
- [X] Pagination controls appear top AND bottom with a "Next" link.
- [X] Click page 2 — you see the remaining ~6 threads (30 total in this subcategory).
- [X] No thread is pinned in General Discussion.

Visit `/c/general/announcements`.

- [X] One thread, with a pin icon next to the title: **"Welcome to ForumForge — please read"**.

Visit `/c/tech/software`.

- [X] Three threads. One has a lock icon: **"[LOCKED] SQLite vs Postgres for small forums"**.

## 5. Thread view + replies (the recent bug)

Visit the welcome thread (`/c/general/announcements` → click the thread).

- [X] Header shows **"2 replies"** (NOT 3 or 5 — this was the reply-count double-count bug).
- [X] OP body renders Markdown: bulleted list with three points.
- [X] Two replies appear below, by alice and bob, in chronological order.
- [X] Each post shows the author's avatar (or Gravatar identicon), display name, role badge, post count, join date.
- [X] Alice's signature ("— sent from my workstation") appears under HER posts in the thread, with a subtle divider.

Visit "What are you working on this week?".

- [X] Header shows **"4 replies"** (one OP + four replies on this thread).

Open the locked thread.

- [X] As alice/bob (members), no reply form appears at the bottom.
- [X] Logout, login as moderator — the reply form IS available (mods bypass the lock).

## 6. Reactions

On the welcome thread OP, look at the reaction button.

- [X] Count shows **4** likes (Alice, Bob, Carol, Dave reacted).
- [X] Click it as the current user — count flips +1 or -1 without a page reload.
- [X] Click again — toggles back.

## 7. Reply flow (sanity check on counters)

Login as **bob@forum.test**, open "Beginner question: when to use a goroutine?" and post a reply.

- [X] Reply appears immediately at the bottom (HTMX append).
- [X] After reload, the subcategory listing for that thread shows **"2 replies"** (was 1, now 2). NOT "3".
- [X] Last-post username on the subcategory row is now "bob".

## 8. New thread flow

Login as alice (`/c/general/general-discussion`).

- [X] "+ New Thread" button visible in top right of the header.
- [x] Click → form loads with title input, format selector (markdown default), body textarea.
- [x] Submit with a title and body — redirects to `/t/{id}` showing your new thread.
- [X] The new thread now appears at the TOP of the subcategory listing (last_post_at = now).
- [X] The new thread shows **"0 replies"** (NOT "1 reply").

## 9. Avatar upload (multipart CSRF fix)

Login as alice, go to `/settings`.

- [X] Choose a JPEG/PNG/GIF under 2MB. Click **Save Profile**.
- [X] You are redirected to `/settings?saved=1` with a "saved" notice — NOT 403, NOT an empty page.
- [X] Avatar preview updates.
- [X] Visit `/u/alice` — the new avatar replaces the Gravatar identicon.
- [X] Confirm `uploads/avatars/{userID}.{ext}` exists on disk.

Try uploading a WebP, BMP, or large (>2MB) image.

- [X] You see the settings page re-rendered with a clear red error message (NOT an empty page).

## 10. Messages

Login as alice. Open nav dropdown → **Messages**.

- [X] Lands on `/pm` (NOT 404).
- [X] Empty inbox state with "Send a message" link.

Click `/pm/new`, recipient = `bob`, send a message.

- [X] Redirects to `/pm` inbox.

Logout, login as bob, open Messages.

- [X] Message from alice appears (unread highlight).
- [X] Click it → full thread, with **Reply** button pre-filling recipient + "Re: ..." subject.

## 11. Mod queue

Login as moderator. Nav → **Mod Queue**.

- [X] One open report visible: "Looks like affiliate spam." (filed by bob against mallory's spam OP).
- [X] You can dismiss OR delete-and-dismiss.

After delete-and-dismiss:

- [X] The spam thread's OP renders as "[removed by moderator]" placeholder.
- [X] The report disappears from the open queue.

Try banning mallory from her profile.

- [X] Profile shows "Banned" badge afterwards.
- [X] Logout, attempt mallory login → rejected.

## 12. Admin panel

Login as admin. Nav → **Admin Panel**.

- [X] **Settings** page shows all configurable fields (edit window, page sizes, rate limits, link limits, keyword blocklist).
- [X] **Categories** page lets you create, rename, reorder, delete categories and subcategories.

Try deleting "Hardware". Add a keyword to the blocklist (e.g., "spamme"). Save.

- [O] Settings reload reflects the change.

## 13. Search

Top nav → Search. Type `goroutine`.

- [X] Live results appear after a short delay (HTMX `keyup` trigger).
- [O] Carol's beginner thread shows up with a highlighted snippet.
- [X] Typing nonsense (`xyzzy999`) → "no results" state, no server error.

## 14. Rate limit (optional, slower test)

Login as bob. Try reporting 6 different posts in rapid succession.

- [X] First 5 succeed (the configured `reportRateLimit = 5/hour`).
- [X] 6th returns 429 Too Many Requests with a `Retry-After` header.

---

## How to file a "doesn't match" finding

Note the checklist item number, what you expected (per this doc), what you saw, and any console/network output. That gives enough context to bisect quickly.
