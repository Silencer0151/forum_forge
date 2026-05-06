package handler_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/nitro/forum_forge/internal/auth"
	"github.com/nitro/forum_forge/internal/handler"
	"github.com/nitro/forum_forge/internal/middleware"
	"github.com/nitro/forum_forge/internal/render"
	"github.com/nitro/forum_forge/internal/store/sqlite"
)

// newTestEnv stands up a self-contained server with the full auth middleware
// chain (Auth + CSRF) plus the auth handlers, backed by an in-memory SQLite
// store and the on-disk templates. bcrypt cost is forced to MinCost so the
// suite runs in under a second.
func newTestEnv(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()

	migrationsFS := os.DirFS("../../migrations")
	st, err := sqlite.New(":memory:", migrationsFS)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	templatesFS := os.DirFS("../../templates")
	r, err := render.New(templatesFS)
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}

	svc := auth.NewWithCost(st, bcrypt.MinCost)
	h := handler.NewAuth(svc, r, handler.AuthConfig{Secure: false})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/login", h.LoginPage)
	mux.HandleFunc("POST /auth/login", h.Login)
	mux.HandleFunc("GET /auth/register", h.RegisterPage)
	mux.HandleFunc("POST /auth/register", h.Register)
	mux.HandleFunc("POST /auth/logout", h.Logout)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		data := handler.BaseData(r)
		data["Title"] = "Home"
		if err := r.ParseForm(); err != nil {
			t.Logf("parse form: %v", err)
		}
		u := middleware.UserFromContext(r.Context())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if u == nil {
			_, _ = w.Write([]byte("guest"))
			return
		}
		_, _ = w.Write([]byte("user:" + u.Username))
	})

	chain := middleware.Chain(
		middleware.Auth(st),
		middleware.CSRF(middleware.CSRFConfig{Secure: false}),
	)
	srv := httptest.NewServer(chain(mux))
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Don't follow redirects — tests assert on the redirect itself.
			return http.ErrUseLastResponse
		},
	}
	return srv, client
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

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// primeCSRF makes one GET request to seed the CSRF cookie in the jar and
// returns the token value. Subsequent POSTs include it as a form field.
func primeCSRF(t *testing.T, client *http.Client, srv *httptest.Server) string {
	t.Helper()
	resp := mustGet(t, client, srv.URL+"/auth/login")
	resp.Body.Close()
	c := cookieByName(client, srv, middleware.CSRFCookieName)
	if c == nil {
		t.Fatalf("csrf cookie not set")
	}
	return c.Value
}

// ── tests ────────────────────────────────────────────────────────────────────

func TestAuth_RegisterLoginLogoutFlow(t *testing.T) {
	srv, client := newTestEnv(t)
	csrf := primeCSRF(t, client, srv)

	// 1. Register a fresh user.
	registerForm := url.Values{
		"csrf_token": {csrf},
		"username":   {"alice"},
		"email":      {"alice@example.com"},
		"password":   {"hunter2hunter2"},
	}
	resp := mustPostForm(t, client, srv.URL+"/auth/register", registerForm)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("register: status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("register redirect: got %q, want /", loc)
	}
	if cookieByName(client, srv, middleware.SessionCookieName) == nil {
		t.Fatal("register: expected session cookie to be set")
	}

	// 2. Authenticated GET / sees the user.
	got := body(t, mustGet(t, client, srv.URL+"/"))
	if !strings.Contains(got, "user:alice") {
		t.Errorf("home body: got %q, want to contain user:alice", got)
	}

	// 3. Logout clears the session cookie.
	logoutForm := url.Values{"csrf_token": {csrf}}
	resp = mustPostForm(t, client, srv.URL+"/auth/logout", logoutForm)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout: status = %d, want 303", resp.StatusCode)
	}
	if c := cookieByName(client, srv, middleware.SessionCookieName); c != nil {
		t.Errorf("logout: session cookie still present: %+v", c)
	}

	// 4. After logout the home page renders the guest body.
	got = body(t, mustGet(t, client, srv.URL+"/"))
	if !strings.Contains(got, "guest") {
		t.Errorf("post-logout home body: got %q, want guest", got)
	}

	// 5. Login with the same credentials succeeds.
	csrf = primeCSRF(t, client, srv)
	loginForm := url.Values{
		"csrf_token": {csrf},
		"email":      {"alice@example.com"},
		"password":   {"hunter2hunter2"},
	}
	resp = mustPostForm(t, client, srv.URL+"/auth/login", loginForm)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: status = %d, want 303", resp.StatusCode)
	}
	if cookieByName(client, srv, middleware.SessionCookieName) == nil {
		t.Fatal("login: expected session cookie to be set")
	}
}

func TestAuth_Register_DuplicateEmail(t *testing.T) {
	srv, client := newTestEnv(t)
	csrf := primeCSRF(t, client, srv)

	form := func(username, email string) url.Values {
		return url.Values{
			"csrf_token": {csrf},
			"username":   {username},
			"email":      {email},
			"password":   {"longerpw1"},
		}
	}

	resp := mustPostForm(t, client, srv.URL+"/auth/register", form("alice", "alice@example.com"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("first register: status = %d", resp.StatusCode)
	}

	// Same email, different username → 409 Conflict and form re-renders.
	resp = mustPostForm(t, client, srv.URL+"/auth/register", form("alice2", "alice@example.com"))
	got := body(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("dup email: status = %d, want 409", resp.StatusCode)
	}
	if !strings.Contains(got, "already exists") {
		t.Errorf("dup email body missing message: %s", got)
	}
}

func TestAuth_Register_ValidationErrors(t *testing.T) {
	srv, client := newTestEnv(t)
	csrf := primeCSRF(t, client, srv)

	// Short password and bad username → 400 with field errors rendered.
	form := url.Values{
		"csrf_token": {csrf},
		"username":   {"a"},
		"email":      {"not-an-email"},
		"password":   {"short"},
	}
	resp := mustPostForm(t, client, srv.URL+"/auth/register", form)
	got := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	for _, want := range []string{"3–32 characters", "Email address is not valid", "at least 8 characters"} {
		if !strings.Contains(got, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, got)
		}
	}
}

func TestAuth_Login_BadPassword(t *testing.T) {
	srv, client := newTestEnv(t)
	csrf := primeCSRF(t, client, srv)

	// Register first.
	resp := mustPostForm(t, client, srv.URL+"/auth/register", url.Values{
		"csrf_token": {csrf},
		"username":   {"bob"},
		"email":      {"bob@example.com"},
		"password":   {"correctpassword"},
	})
	resp.Body.Close()

	// Logout so we're a guest again.
	resp = mustPostForm(t, client, srv.URL+"/auth/logout", url.Values{"csrf_token": {csrf}})
	resp.Body.Close()

	// Wrong password → 401 with "Invalid email or password".
	csrf = primeCSRF(t, client, srv)
	resp = mustPostForm(t, client, srv.URL+"/auth/login", url.Values{
		"csrf_token": {csrf},
		"email":      {"bob@example.com"},
		"password":   {"wrongpassword"},
	})
	got := body(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if !strings.Contains(got, "Invalid email or password") {
		t.Errorf("body missing error: %s", got)
	}
	if cookieByName(client, srv, middleware.SessionCookieName) != nil {
		t.Error("failed login should not set session cookie")
	}
}

func TestAuth_Login_UnknownEmail(t *testing.T) {
	srv, client := newTestEnv(t)
	csrf := primeCSRF(t, client, srv)

	// Same generic error message for unknown email so attackers can't
	// enumerate accounts.
	resp := mustPostForm(t, client, srv.URL+"/auth/login", url.Values{
		"csrf_token": {csrf},
		"email":      {"ghost@example.com"},
		"password":   {"whatever"},
	})
	got := body(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if !strings.Contains(got, "Invalid email or password") {
		t.Errorf("body missing error: %s", got)
	}
}

func TestAuth_PostMissingCSRFRejected(t *testing.T) {
	srv, client := newTestEnv(t)
	// Skip primeCSRF — submit a POST with no csrf_token form value AND
	// no csrf cookie. The CSRF middleware should reject it with 403.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/auth/register",
		strings.NewReader("username=x&email=x@x.test&password=longpassword"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestAuth_LoginPage_RedirectsAuthenticatedUser(t *testing.T) {
	srv, client := newTestEnv(t)
	csrf := primeCSRF(t, client, srv)

	// Register so we have a session.
	resp := mustPostForm(t, client, srv.URL+"/auth/register", url.Values{
		"csrf_token": {csrf},
		"username":   {"carol"},
		"email":      {"carol@example.com"},
		"password":   {"strongerpw"},
	})
	resp.Body.Close()

	// GET /auth/login should redirect authenticated users home.
	resp = mustGet(t, client, srv.URL+"/auth/login")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("location = %q, want /", loc)
	}
}
