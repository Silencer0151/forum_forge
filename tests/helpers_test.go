package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/nitro/forum_forge/internal/auth"
	"github.com/nitro/forum_forge/internal/config"
	"github.com/nitro/forum_forge/internal/middleware"
	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/render"
	"github.com/nitro/forum_forge/internal/server"
	sqlitestore "github.com/nitro/forum_forge/internal/store/sqlite"
)

// capturedEmail holds a single intercepted outbound email.
type capturedEmail struct {
	To      []string
	From    string
	Message []byte
}

// emailCapture is a thread-safe collector used to intercept emails sent during tests.
type emailCapture struct {
	mu   sync.Mutex
	sent []capturedEmail
}

func (c *emailCapture) record(addr string, _ smtp.Auth, from string, to []string, msg []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, capturedEmail{To: to, From: from, Message: msg})
	return nil
}

func (c *emailCapture) all() []capturedEmail {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedEmail, len(c.sent))
	copy(out, c.sent)
	return out
}

// testEnv is a fully wired test environment: real server, real SQLite, captured email.
type testEnv struct {
	Server  *httptest.Server
	Client  *http.Client
	Store   *sqlitestore.Store
	Capture *emailCapture
}

// newTestEnv starts an httptest.Server with the full middleware stack and all
// routes, backed by an in-memory SQLite database. bcrypt cost is lowered to
// MinCost so the suite stays fast. Each call returns an independent environment;
// call t.Cleanup is handled internally.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	migrationsFS := os.DirFS("../migrations")
	st, err := sqlitestore.New(":memory:", migrationsFS)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	templatesFS := os.DirFS("../templates")
	r, err := render.New(templatesFS)
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}

	staticFS := os.DirFS("../static")

	svc := auth.NewWithCost(st, bcrypt.MinCost)
	tokens := auth.NewTokenService(st)

	capture := &emailCapture{}
	mailer, err := auth.NewMailer(
		auth.SMTPConfig{Host: "test", Port: 25, From: "noreply@test.com"},
		map[string]string{
			"verify": `<p>Hi {{.Username}}</p><a href="{{.VerifyURL}}">verify</a>`,
			"reset":  `<p>Hi {{.Username}}</p><a href="{{.ResetURL}}">reset</a>`,
		},
		auth.WithSendFunc(capture.record),
	)
	if err != nil {
		t.Fatalf("build mailer: %v", err)
	}

	cfg := &config.Config{
		DBDriver:        "sqlite",
		DBPath:          ":memory:",
		ListenAddr:      ":0",
		BaseURL:         "http://localhost",
		UploadPath:      t.TempDir(),
		AuthMode:        "standalone",
		TLSMode:         "none",
		CaptchaProvider: "none",
		LogLevel:        "error",
		LogFormat:       "text",
	}

	authDeps := server.AuthDeps{
		Service:       svc,
		Tokens:        tokens,
		Mailer:        mailer,
		Authenticator: svc,
	}

	srv := server.New(cfg, st, r, staticFS, authDeps)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	env := &testEnv{
		Server:  ts,
		Client:  client,
		Store:   st,
		Capture: capture,
	}

	// Pre-create a throwaway user to absorb the first-user → admin promotion
	// from internal/auth/standalone.go. Tests that register their own users
	// via createTestUser then receive regular member roles, matching the
	// pre-promotion semantics the test suite was written against.
	seedFirstAdmin(t, env)

	return env
}

// seedFirstAdmin registers and logs out a sentinel user (user_id=1) whose only
// purpose is to receive the first-user → admin promotion. After this runs,
// `createTestUser` produces RoleMember users, which is what the rest of the
// suite expects.
func seedFirstAdmin(t *testing.T, env *testEnv) {
	t.Helper()
	csrf := primeCSRF(t, env)
	resp := mustPostForm(t, env.Client, env.Server.URL+"/auth/register", url.Values{
		"csrf_token": {csrf},
		"username":   {"_seed_admin"},
		"email":      {"_seed_admin@example.test"},
		"password":   {"seedpassword1"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("seed admin register: status = %d", resp.StatusCode)
	}
	resp = mustPostForm(t, env.Client, env.Server.URL+"/auth/logout", url.Values{
		"csrf_token": {csrf},
	})
	resp.Body.Close()
}

// primeCSRF seeds the CSRF cookie by making a GET to /auth/login and returns
// the token value for inclusion in POST forms and HTMX headers.
func primeCSRF(t *testing.T, env *testEnv) string {
	t.Helper()
	resp := mustGet(t, env.Client, env.Server.URL+"/auth/login")
	resp.Body.Close()
	c := cookieByName(env.Client, env.Server, middleware.CSRFCookieName)
	if c == nil {
		t.Fatalf("csrf cookie not set after GET /auth/login")
	}
	return c.Value
}

func cookieByName(client *http.Client, srv *httptest.Server, name string) *http.Cookie {
	u, _ := url.Parse(srv.URL)
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func mustGet(t *testing.T, client *http.Client, urlStr string) *http.Response {
	t.Helper()
	resp, err := client.Get(urlStr)
	if err != nil {
		t.Fatalf("GET %s: %v", urlStr, err)
	}
	return resp
}

func mustPostForm(t *testing.T, client *http.Client, urlStr string, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, urlStr, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", urlStr, err)
	}
	return resp
}

func mustHTMXGet(t *testing.T, client *http.Client, urlStr string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, urlStr, nil)
	if err != nil {
		t.Fatalf("build HTMX GET request: %v", err)
	}
	req.Header.Set("HX-Request", "true")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("HTMX GET %s: %v", urlStr, err)
	}
	return resp
}

func mustHTMXPost(t *testing.T, client *http.Client, urlStr string, form url.Values, csrfToken string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, urlStr, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build HTMX POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set(middleware.CSRFHeaderName, csrfToken)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("HTMX POST %s: %v", urlStr, err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// createTestUser registers a new account via the HTTP API. Returns the CSRF
// token still valid for the resulting session.
func createTestUser(t *testing.T, env *testEnv, username, email, password string) string {
	t.Helper()
	csrf := primeCSRF(t, env)
	resp := mustPostForm(t, env.Client, env.Server.URL+"/auth/register", url.Values{
		"csrf_token": {csrf},
		"username":   {username},
		"email":      {email},
		"password":   {password},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("register %s: status = %d, want 303", username, resp.StatusCode)
	}
	return csrf
}

// loginAs logs in as the named user and returns the refreshed CSRF token.
func loginAs(t *testing.T, env *testEnv, email, password string) string {
	t.Helper()
	csrf := primeCSRF(t, env)
	resp := mustPostForm(t, env.Client, env.Server.URL+"/auth/login", url.Values{
		"csrf_token": {csrf},
		"email":      {email},
		"password":   {password},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login %s: status = %d, want 303", email, resp.StatusCode)
	}
	return csrf
}

// logout clears the current session.
func logout(t *testing.T, env *testEnv, csrf string) {
	t.Helper()
	resp := mustPostForm(t, env.Client, env.Server.URL+"/auth/logout", url.Values{
		"csrf_token": {csrf},
	})
	resp.Body.Close()
}

// promoteMod upgrades a user's role to moderator directly in the store. Use
// after HTTP registration so that subsequent requests carry mod privileges.
func promoteMod(t *testing.T, st *sqlitestore.Store, username string) {
	t.Helper()
	ctx := context.Background()
	u, err := st.GetUserByUsername(ctx, username)
	if err != nil {
		t.Fatalf("get user %s for promotion: %v", username, err)
	}
	u.Role = model.RoleModerator
	if err := st.UpdateUser(ctx, u); err != nil {
		t.Fatalf("promote %s to mod: %v", username, err)
	}
}

// seedCategory creates a public category and subcategory directly in the store.
// Returns (categorySlug, subcategorySlug).
func seedCategory(t *testing.T, st *sqlitestore.Store) (string, string) {
	t.Helper()
	ctx := context.Background()

	cat := &model.Category{
		Name:     "General",
		Slug:     "general",
		IsPublic: true,
	}
	if err := st.CreateCategory(ctx, cat); err != nil {
		t.Fatalf("create category: %v", err)
	}

	sub := &model.Subcategory{
		CategoryID: cat.ID,
		Name:       "Discussion",
		Slug:       "discussion",
	}
	if err := st.CreateSubcategory(ctx, sub); err != nil {
		t.Fatalf("create subcategory: %v", err)
	}

	return cat.Slug, sub.Slug
}

// createTestThread POSTs to /t/new and returns the new thread ID string as it
// appears in the redirect Location (/t/{id}).
func createTestThread(t *testing.T, env *testEnv, catSlug, subSlug, title, body, csrf string) string {
	t.Helper()
	resp := mustPostForm(t, env.Client, env.Server.URL+"/t/new", url.Values{
		"csrf_token":  {csrf},
		"cat_slug":    {catSlug},
		"sub_slug":    {subSlug},
		"title":       {title},
		"body":        {body},
		"body_format": {"markdown"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create thread: status = %d, body = %s", resp.StatusCode, b)
	}
	// Location: /t/{id}
	loc := resp.Header.Get("Location")
	return strings.TrimPrefix(loc, "/t/")
}

// createTestReply POSTs a reply to threadID. Returns the post's Location fragment.
func createTestReply(t *testing.T, env *testEnv, threadID, body, csrf string) {
	t.Helper()
	resp := mustPostForm(t, env.Client, env.Server.URL+"/t/"+threadID+"/reply", url.Values{
		"csrf_token":  {csrf},
		"body":        {body},
		"body_format": {"markdown"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create reply to thread %s: status = %d, body = %s", threadID, resp.StatusCode, b)
	}
}
