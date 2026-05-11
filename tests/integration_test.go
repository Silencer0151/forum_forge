// Package integration_test contains end-to-end tests that drive the real HTTP
// router via httptest.Server, real SQLite (in-memory), and the full middleware
// chain. Each test function creates an independent environment so tests do not
// share state. Run with:
//
//	go test ./tests/... -v
package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/nitro/forum_forge/internal/middleware"
	"github.com/nitro/forum_forge/internal/store"
)

// ── Full registration / thread / reply / edit flow ────────────────────────────

// TestIntegration_FullFlow covers:
//   - Register → session cookie set
//   - Login / logout round-trip
//   - Create thread → redirects to /t/{id}
//   - Reply to thread → appears on thread page
//   - Edit own post within the edit window (via POST _method=PUT override)
func TestIntegration_FullFlow(t *testing.T) {
	env := newTestEnv(t)
	catSlug, subSlug := seedCategory(t, env.Store)

	// 1. Register alice.
	csrf := createTestUser(t, env, "alice", "alice@example.com", "securepassword1")

	// Session cookie must be present after registration.
	if cookieByName(env.Client, env.Server, middleware.SessionCookieName) == nil {
		t.Fatal("session cookie not set after registration")
	}

	// 2. Forum index is reachable and shows the category.
	resp := mustGet(t, env.Client, env.Server.URL+"/")
	got := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /: status = %d", resp.StatusCode)
	}
	if !strings.Contains(got, "General") {
		t.Errorf("index body missing category name: %s", got[:min(200, len(got))])
	}

	// 3. Category page is reachable.
	resp = mustGet(t, env.Client, env.Server.URL+"/c/"+catSlug)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /c/%s: status = %d", catSlug, resp.StatusCode)
	}
	resp.Body.Close()

	// 4. Subcategory page is reachable.
	resp = mustGet(t, env.Client, env.Server.URL+"/c/"+catSlug+"/"+subSlug)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /c/%s/%s: status = %d", catSlug, subSlug, resp.StatusCode)
	}
	resp.Body.Close()

	// 5. Create a thread.
	threadID := createTestThread(t, env, catSlug, subSlug, "Hello World", "This is the OP body", csrf)
	if threadID == "" {
		t.Fatal("createTestThread returned empty ID")
	}

	// Thread page renders the OP body.
	resp = mustGet(t, env.Client, env.Server.URL+"/t/"+threadID)
	got = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /t/%s: status = %d", threadID, resp.StatusCode)
	}
	if !strings.Contains(got, "Hello World") {
		t.Errorf("thread page missing title: %s", got[:min(400, len(got))])
	}
	if !strings.Contains(got, "This is the OP body") {
		t.Errorf("thread page missing OP body: %s", got[:min(400, len(got))])
	}

	// 6. Post a reply.
	createTestReply(t, env, threadID, "This is a reply from alice", csrf)

	// Reply appears on the thread page.
	resp = mustGet(t, env.Client, env.Server.URL+"/t/"+threadID)
	got = readBody(t, resp)
	if !strings.Contains(got, "This is a reply from alice") {
		t.Errorf("thread page missing reply body: %s", got[:min(600, len(got))])
	}

	// 7. Logout.
	logout(t, env, csrf)
	if cookieByName(env.Client, env.Server, middleware.SessionCookieName) != nil {
		t.Error("session cookie should be cleared after logout")
	}

	// 8. Login again.
	csrf = loginAs(t, env, "alice@example.com", "securepassword1")
	if cookieByName(env.Client, env.Server, middleware.SessionCookieName) == nil {
		t.Fatal("session cookie not set after login")
	}

	// 9. Edit the OP (alice is author, within the default 30-minute window).
	ctx := context.Background()
	result, err := env.Store.ListPostsByThread(ctx, parseID(t, threadID), store.PageRequest{Page: 1, PerPage: 20})
	if err != nil || len(result.Items) == 0 {
		t.Fatalf("list posts: err=%v, items=%d", err, len(result.Items))
	}
	opPost := result.Items[0]

	resp = mustPostForm(t, env.Client,
		fmt.Sprintf("%s/p/%d", env.Server.URL, opPost.Post.ID),
		url.Values{
			"csrf_token":  {csrf},
			"_method":     {"PUT"},
			"body":        {"Edited OP body"},
			"body_format": {"markdown"},
		},
	)
	if resp.StatusCode != http.StatusSeeOther {
		got = readBody(t, resp)
		t.Fatalf("edit post via _method=PUT: status = %d, body = %s", resp.StatusCode, got[:min(300, len(got))])
	}
	resp.Body.Close()

	// Thread page reflects the edit.
	resp = mustGet(t, env.Client, env.Server.URL+"/t/"+threadID)
	got = readBody(t, resp)
	if !strings.Contains(got, "Edited OP body") {
		t.Errorf("thread page should show edited body: %s", got[:min(600, len(got))])
	}
}

// ── Guest access ──────────────────────────────────────────────────────────────

// TestIntegration_GuestAccess verifies that unauthenticated visitors can browse
// public pages but are blocked from posting.
func TestIntegration_GuestAccess(t *testing.T) {
	env := newTestEnv(t)
	catSlug, subSlug := seedCategory(t, env.Store)

	// Seed a thread to browse.
	csrf := createTestUser(t, env, "poster", "poster@example.com", "strongpassword1")
	threadID := createTestThread(t, env, catSlug, subSlug, "A Public Thread", "Content here", csrf)
	logout(t, env, csrf)

	// Forum index: public.
	resp := mustGet(t, env.Client, env.Server.URL+"/")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("guest GET /: status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Category page: public.
	resp = mustGet(t, env.Client, env.Server.URL+"/c/"+catSlug)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("guest GET /c/%s: status = %d", catSlug, resp.StatusCode)
	}
	resp.Body.Close()

	// Thread page: public (guests can read).
	resp = mustGet(t, env.Client, env.Server.URL+"/t/"+threadID)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("guest GET /t/%s: status = %d", threadID, resp.StatusCode)
	}
	resp.Body.Close()

	// Reply as guest: must be rejected with 403.
	guestCSRF := primeCSRF(t, env)
	resp = mustPostForm(t, env.Client, env.Server.URL+"/t/"+threadID+"/reply", url.Values{
		"csrf_token":  {guestCSRF},
		"body":        {"This should be rejected"},
		"body_format": {"markdown"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("guest POST /t/%s/reply: status = %d, want 403", threadID, resp.StatusCode)
	}
	resp.Body.Close()

	// Create thread as guest: 403.
	resp = mustPostForm(t, env.Client, env.Server.URL+"/t/new", url.Values{
		"csrf_token":  {guestCSRF},
		"cat_slug":    {catSlug},
		"sub_slug":    {subSlug},
		"title":       {"Spam"},
		"body":        {"Spam body"},
		"body_format": {"markdown"},
	})
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusFound {
		t.Errorf("guest POST /t/new: status = %d, want 403 or 302", resp.StatusCode)
	}
	resp.Body.Close()

	// Settings page: requires auth → redirect.
	resp = mustGet(t, env.Client, env.Server.URL+"/settings")
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		t.Errorf("guest GET /settings: status = %d, want redirect", resp.StatusCode)
	}
	resp.Body.Close()
}

// ── Moderation flow ───────────────────────────────────────────────────────────

// TestIntegration_ModFlow covers:
//   - Member creates post that another member reports
//   - Moderator views the report queue
//   - Moderator deletes the post via report review
//   - Moderator bans the original poster
func TestIntegration_ModFlow(t *testing.T) {
	env := newTestEnv(t)
	catSlug, subSlug := seedCategory(t, env.Store)

	aliceCSRF := createTestUser(t, env, "alice", "alice@example.com", "alicepassword1")
	_ = createTestUser(t, env, "bob", "bob@example.com", "bobpassword1")
	_ = createTestUser(t, env, "mod", "mod@example.com", "modpassword1")
	promoteMod(t, env.Store, "mod")

	// Alice creates a thread and posts a reply.
	threadID := createTestThread(t, env, catSlug, subSlug, "Alice's Thread", "Content by alice", aliceCSRF)
	createTestReply(t, env, threadID, "Alice's reply to her own thread", aliceCSRF)
	logout(t, env, aliceCSRF)

	// Fetch alice's reply post ID.
	ctx := context.Background()
	result, err := env.Store.ListPostsByThread(ctx, parseID(t, threadID), store.PageRequest{Page: 1, PerPage: 20})
	if err != nil || len(result.Items) < 2 {
		t.Fatalf("expected ≥2 posts; got err=%v, items=%d", err, len(result.Items))
	}
	replyPost := result.Items[1]

	// Bob reports alice's reply.
	bobCSRF := loginAs(t, env, "bob@example.com", "bobpassword1")
	resp := mustPostForm(t, env.Client,
		fmt.Sprintf("%s/p/%d/report", env.Server.URL, replyPost.Post.ID),
		url.Values{
			"csrf_token": {bobCSRF},
			"reason":     {"This is spam"},
		},
	)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusSeeOther {
		got := readBody(t, resp)
		t.Fatalf("report post: status = %d, body = %s", resp.StatusCode, got[:min(300, len(got))])
	}
	resp.Body.Close()
	logout(t, env, bobCSRF)

	// Moderator views the report queue.
	modCSRF := loginAs(t, env, "mod@example.com", "modpassword1")
	resp = mustGet(t, env.Client, env.Server.URL+"/mod/reports")
	got := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /mod/reports: status = %d", resp.StatusCode)
	}
	if !strings.Contains(got, "This is spam") {
		t.Errorf("mod queue missing report reason: %s", got[:min(600, len(got))])
	}

	// Get the open report's ID from the store.
	reports, err := env.Store.ListOpenReports(ctx, store.PageRequest{Page: 1, PerPage: 10})
	if err != nil || len(reports.Items) == 0 {
		t.Fatalf("list open reports: err=%v, items=%d", err, len(reports.Items))
	}
	reportID := reports.Items[0].ID

	// Moderator deletes the post via report review.
	resp = mustPostForm(t, env.Client,
		fmt.Sprintf("%s/mod/reports/%d/review", env.Server.URL, reportID),
		url.Values{
			"csrf_token": {modCSRF},
			"action":     {"delete_and_dismiss"},
		},
	)
	if resp.StatusCode != http.StatusSeeOther {
		got = readBody(t, resp)
		t.Fatalf("review report: status = %d, body = %s", resp.StatusCode, got[:min(300, len(got))])
	}
	resp.Body.Close()

	// Post should now be soft-deleted.
	deletedPost, err := env.Store.GetPostByID(ctx, replyPost.Post.ID)
	if err != nil {
		t.Fatalf("get deleted post: %v", err)
	}
	if !deletedPost.IsDeleted {
		t.Error("post should be soft-deleted after delete_and_dismiss")
	}

	// Moderator bans alice.
	aliceUser, err := env.Store.GetUserByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("get alice: %v", err)
	}
	resp = mustPostForm(t, env.Client,
		fmt.Sprintf("%s/mod/users/%d/ban", env.Server.URL, aliceUser.ID),
		url.Values{
			"csrf_token": {modCSRF},
			"reason":     {"Repeated spam"},
		},
	)
	if resp.StatusCode != http.StatusSeeOther {
		got = readBody(t, resp)
		t.Fatalf("ban user: status = %d, body = %s", resp.StatusCode, got[:min(300, len(got))])
	}
	resp.Body.Close()

	// Alice is banned in the store.
	aliceUser, err = env.Store.GetUserByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("re-fetch alice: %v", err)
	}
	if !aliceUser.Banned {
		t.Error("alice should be banned after mod action")
	}

	// Non-mod accessing mod queue gets 403.
	logout(t, env, modCSRF)
	bobCSRF = loginAs(t, env, "bob@example.com", "bobpassword1")
	resp = mustGet(t, env.Client, env.Server.URL+"/mod/reports")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-mod GET /mod/reports: status = %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()
}

// ── Rate limiting ─────────────────────────────────────────────────────────────

// TestIntegration_RateLimit verifies that the per-user report rate limiter
// (5 reports/hour) returns 429 after the threshold is exceeded.
func TestIntegration_RateLimit(t *testing.T) {
	env := newTestEnv(t)
	catSlug, subSlug := seedCategory(t, env.Store)

	// Register both users with clean session management so they get distinct IDs.
	posterCSRF := createTestUser(t, env, "poster", "poster@example.com", "posterpassword1")
	logout(t, env, posterCSRF)
	_ = createTestUser(t, env, "reporter", "reporter@example.com", "reporterpassword1")
	logout(t, env, primeCSRF(t, env))

	// Log in as poster to create the threads.
	posterCSRF = loginAs(t, env, "poster@example.com", "posterpassword1")

	// Create 6 posts (one per thread) for the reporter to report.
	ctx := context.Background()
	postIDs := make([]int64, 6)
	for i := range 6 {
		tid := createTestThread(t, env, catSlug, subSlug,
			fmt.Sprintf("Thread %d", i),
			fmt.Sprintf("Body of thread %d", i),
			posterCSRF,
		)
		result, err := env.Store.ListPostsByThread(ctx, parseID(t, tid), store.PageRequest{Page: 1, PerPage: 5})
		if err != nil || len(result.Items) == 0 {
			t.Fatalf("list posts for thread %s: %v", tid, err)
		}
		postIDs[i] = result.Items[0].Post.ID
	}
	logout(t, env, posterCSRF)

	// Reporter submits 5 reports — all should succeed.
	reporterCSRF := loginAs(t, env, "reporter@example.com", "reporterpassword1")
	for i := range 5 {
		resp := mustPostForm(t, env.Client,
			fmt.Sprintf("%s/p/%d/report", env.Server.URL, postIDs[i]),
			url.Values{
				"csrf_token": {reporterCSRF},
				"reason":     {fmt.Sprintf("Spam reason %d", i)},
			},
		)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("report %d: unexpected 429 before limit reached", i)
		}
	}

	// 6th report must be rate-limited.
	resp := mustPostForm(t, env.Client,
		fmt.Sprintf("%s/p/%d/report", env.Server.URL, postIDs[5]),
		url.Values{
			"csrf_token": {reporterCSRF},
			"reason":     {"Sixth report — should be blocked"},
		},
	)
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("6th report: status = %d, want 429", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}
}

// ── Search ────────────────────────────────────────────────────────────────────

// TestIntegration_Search verifies that posts become searchable after creation
// and that the search endpoint returns matching results.
func TestIntegration_Search(t *testing.T) {
	env := newTestEnv(t)
	catSlug, subSlug := seedCategory(t, env.Store)

	csrf := createTestUser(t, env, "searcher", "searcher@example.com", "searcherpass1")
	threadID := createTestThread(t, env, catSlug, subSlug,
		"Unique Searchable Title",
		"This body contains the unique keyword xyzzy123",
		csrf,
	)
	createTestReply(t, env, threadID, "Another reply also mentioning xyzzy123", csrf)

	// Search for the unique keyword.
	resp := mustGet(t, env.Client, env.Server.URL+"/search?q=xyzzy123")
	got := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /search?q=xyzzy123: status = %d", resp.StatusCode)
	}
	if !strings.Contains(got, "xyzzy123") {
		t.Errorf("search results page missing keyword: %s", got[:min(600, len(got))])
	}

	// Search for an absent term — must not error.
	resp = mustGet(t, env.Client, env.Server.URL+"/search?q=notpresentanywhere9999")
	got = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /search?q=notpresent: status = %d", resp.StatusCode)
	}
	if strings.Contains(got, "internal server error") {
		t.Errorf("empty search returned server error: %s", got[:min(400, len(got))])
	}
}

// ── HTMX partial responses ────────────────────────────────────────────────────

// TestIntegration_HTMX verifies that routes with HTMX support return content
// partials (no <html> wrapper) when HX-Request: true is set.
func TestIntegration_HTMX(t *testing.T) {
	env := newTestEnv(t)
	catSlug, subSlug := seedCategory(t, env.Store)

	csrf := createTestUser(t, env, "htmxuser", "htmx@example.com", "htmxpassword1")
	threadID := createTestThread(t, env, catSlug, subSlug, "HTMX Test Thread", "Thread content", csrf)

	// Non-HTMX request → full HTML document.
	resp := mustGet(t, env.Client, env.Server.URL+"/t/"+threadID)
	fullPage := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("full-page GET /t/%s: status = %d", threadID, resp.StatusCode)
	}
	if !strings.Contains(fullPage, "<html") {
		t.Error("full-page response should contain <html>")
	}

	// HTMX request → content partial, no <html> wrapper.
	resp = mustHTMXGet(t, env.Client, env.Server.URL+"/t/"+threadID)
	partial := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTMX GET /t/%s: status = %d", threadID, resp.StatusCode)
	}
	if strings.Contains(partial, "<html") {
		t.Error("HTMX partial response should not contain <html>")
	}
	if !strings.Contains(partial, "HTMX Test Thread") {
		t.Errorf("HTMX partial missing thread title: %s", partial[:min(600, len(partial))])
	}

	// HTMX reply POST → post partial returned inline.
	resp = mustHTMXPost(t, env.Client,
		env.Server.URL+"/t/"+threadID+"/reply",
		url.Values{
			"csrf_token":  {csrf},
			"body":        {"HTMX reply content"},
			"body_format": {"markdown"},
		},
		csrf,
	)
	htmxReply := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTMX POST /t/%s/reply: status = %d, body = %s",
			threadID, resp.StatusCode, htmxReply[:min(300, len(htmxReply))])
	}
	if strings.Contains(htmxReply, "<html") {
		t.Error("HTMX reply response should not contain <html>")
	}
	if !strings.Contains(htmxReply, "HTMX reply content") {
		t.Errorf("HTMX reply partial missing post body: %s", htmxReply[:min(400, len(htmxReply))])
	}

	// HTMX subcategory listing → content without full layout.
	resp = mustHTMXGet(t, env.Client, env.Server.URL+"/c/"+catSlug+"/"+subSlug)
	subPartial := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTMX GET /c/%s/%s: status = %d", catSlug, subSlug, resp.StatusCode)
	}
	if strings.Contains(subPartial, "<html") {
		t.Error("HTMX subcategory response should not contain <html>")
	}
}

// ── Health check ─────────────────────────────────────────────────────────────

func TestIntegration_HealthCheck(t *testing.T) {
	env := newTestEnv(t)

	resp := mustGet(t, env.Client, env.Server.URL+"/health")
	got := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health: status = %d", resp.StatusCode)
	}
	if !strings.Contains(got, "ok") {
		t.Errorf("health check body = %q, want \"ok\"", got)
	}
}

// ── Security headers ─────────────────────────────────────────────────────────

// TestIntegration_SecurityHeaders verifies that the security middleware injects
// hardening headers on every response.
func TestIntegration_SecurityHeaders(t *testing.T) {
	env := newTestEnv(t)

	resp := mustGet(t, env.Client, env.Server.URL+"/")
	resp.Body.Close()

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
	}
	for header, wantVal := range want {
		if got := resp.Header.Get(header); !strings.Contains(got, wantVal) {
			t.Errorf("header %s = %q, want to contain %q", header, got, wantVal)
		}
	}
	if csp := resp.Header.Get("Content-Security-Policy"); csp == "" {
		t.Error("Content-Security-Policy header not set")
	}
}

// ── CSRF protection ───────────────────────────────────────────────────────────

// TestIntegration_CSRFProtection ensures POST requests without a valid CSRF
// token are rejected with 403.
func TestIntegration_CSRFProtection(t *testing.T) {
	env := newTestEnv(t)

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		env.Server.URL+"/auth/register",
		strings.NewReader("username=x&email=x@x.test&password=longpassword"),
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := env.Client.Do(req)
	if err != nil {
		t.Fatalf("POST without CSRF: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("no-CSRF POST: status = %d, want 403", resp.StatusCode)
	}
}

// ── User profile ──────────────────────────────────────────────────────────────

func TestIntegration_UserProfile(t *testing.T) {
	env := newTestEnv(t)

	createTestUser(t, env, "profileuser", "profile@example.com", "profilepass1")

	resp := mustGet(t, env.Client, env.Server.URL+"/u/profileuser")
	got := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /u/profileuser: status = %d", resp.StatusCode)
	}
	if !strings.Contains(got, "profileuser") {
		t.Errorf("profile page missing username: %s", got[:min(400, len(got))])
	}

	resp = mustGet(t, env.Client, env.Server.URL+"/u/nosuchuser")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /u/nosuchuser: status = %d, want 404", resp.StatusCode)
	}
}

// ── Private messages ──────────────────────────────────────────────────────────

// TestIntegration_PrivateMessages verifies the PM send and inbox flow.
func TestIntegration_PrivateMessages(t *testing.T) {
	env := newTestEnv(t)

	// Register alice, then logout so bob can register cleanly on a fresh session.
	aliceCSRF := createTestUser(t, env, "alice", "alice@example.com", "alicepassword1")
	logout(t, env, aliceCSRF)
	_ = createTestUser(t, env, "bob", "bob@example.com", "bobpassword1")
	logout(t, env, primeCSRF(t, env))

	// Alice logs in and sends bob a PM.
	aliceCSRF = loginAs(t, env, "alice@example.com", "alicepassword1")
	resp := mustPostForm(t, env.Client, env.Server.URL+"/pm/new", url.Values{
		"csrf_token": {aliceCSRF},
		"to":         {"bob"},
		"subject":    {"Hello Bob"},
		"body":       {"This is a private message."},
	})
	if resp.StatusCode != http.StatusSeeOther {
		got := readBody(t, resp)
		t.Fatalf("send PM: status = %d, body = %s", resp.StatusCode, got[:min(300, len(got))])
	}
	resp.Body.Close()
	logout(t, env, aliceCSRF)

	// Bob logs in and views his inbox — the message should appear.
	_ = loginAs(t, env, "bob@example.com", "bobpassword1")
	resp = mustGet(t, env.Client, env.Server.URL+"/pm")
	got := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /pm: status = %d", resp.StatusCode)
	}
	if !strings.Contains(got, "Hello Bob") {
		t.Errorf("inbox missing PM subject: %s", got[:min(600, len(got))])
	}
}

// ── Thread moderation (lock / pin) ───────────────────────────────────────────

// TestIntegration_ThreadModeration checks that a moderator can lock and pin
// threads and that locked threads refuse replies from regular members.
func TestIntegration_ThreadModeration(t *testing.T) {
	env := newTestEnv(t)
	catSlug, subSlug := seedCategory(t, env.Store)

	memberCSRF := createTestUser(t, env, "member", "member@example.com", "memberpassword1")
	_ = createTestUser(t, env, "mod", "mod@example.com", "modpassword1")
	promoteMod(t, env.Store, "mod")

	threadID := createTestThread(t, env, catSlug, subSlug, "Lockable Thread", "Content", memberCSRF)
	logout(t, env, memberCSRF)

	modCSRF := loginAs(t, env, "mod@example.com", "modpassword1")

	// Lock the thread.
	resp := mustPostForm(t, env.Client,
		fmt.Sprintf("%s/mod/threads/%s/lock", env.Server.URL, threadID),
		url.Values{"csrf_token": {modCSRF}},
	)
	if resp.StatusCode != http.StatusSeeOther {
		got := readBody(t, resp)
		t.Fatalf("lock thread: status = %d, body = %s", resp.StatusCode, got[:min(300, len(got))])
	}
	resp.Body.Close()

	ctx := context.Background()
	thread, err := env.Store.GetThreadByID(ctx, parseID(t, threadID))
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	if !thread.IsLocked {
		t.Error("thread should be locked after mod action")
	}

	// Pin the thread.
	resp = mustPostForm(t, env.Client,
		fmt.Sprintf("%s/mod/threads/%s/pin", env.Server.URL, threadID),
		url.Values{"csrf_token": {modCSRF}},
	)
	if resp.StatusCode != http.StatusSeeOther {
		got := readBody(t, resp)
		t.Fatalf("pin thread: status = %d, body = %s", resp.StatusCode, got[:min(300, len(got))])
	}
	resp.Body.Close()

	thread, err = env.Store.GetThreadByID(ctx, parseID(t, threadID))
	if err != nil {
		t.Fatalf("get thread after pin: %v", err)
	}
	if !thread.IsPinned {
		t.Error("thread should be pinned after mod action")
	}

	// Member cannot reply to a locked thread.
	logout(t, env, modCSRF)
	memberCSRF = loginAs(t, env, "member@example.com", "memberpassword1")
	resp = mustPostForm(t, env.Client, env.Server.URL+"/t/"+threadID+"/reply", url.Values{
		"csrf_token":  {memberCSRF},
		"body":        {"Trying to reply to locked thread"},
		"body_format": {"markdown"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("reply to locked thread: status = %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()
}

// ── helpers ──────────────────────────────────────────────────────────────────

// parseID converts the decimal string returned in redirect locations (/t/{id})
// to int64. Fails the test if the string is not a valid integer.
func parseID(t *testing.T, s string) int64 {
	t.Helper()
	var n int64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		t.Fatalf("parseID(%q): %v", s, err)
	}
	return n
}
