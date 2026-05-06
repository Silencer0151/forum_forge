package handler_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/smtp"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/nitro/forum_forge/internal/auth"
	"github.com/nitro/forum_forge/internal/handler"
	"github.com/nitro/forum_forge/internal/middleware"
	"github.com/nitro/forum_forge/internal/render"
	"github.com/nitro/forum_forge/internal/store/sqlite"
)

// capturedEmail is a single message intercepted by the test mailer.
type capturedEmail struct {
	To      []string
	From    string
	Message []byte
}

// emailCapture is a thread-safe slice of captured emails. Tests use it to
// assert what got sent (and to extract verification/reset URLs).
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

// linkRe extracts the first http(s) URL inside an email body. Sufficient for
// our two templates which contain a single anchor tag with the action link.
var linkRe = regexp.MustCompile(`https?://[^\s"'<>]+`)

// extractLink returns the first URL found in the email body, panicking if
// none is found — tests prefer a clear failure to silent t.Skip.
func extractLink(t *testing.T, e capturedEmail) string {
	t.Helper()
	m := linkRe.FindString(string(e.Message))
	if m == "" {
		t.Fatalf("no link in email body: %s", e.Message)
	}
	return m
}

// newTestEnv stands up a self-contained server with the full auth middleware
// chain (Auth + CSRF) plus the auth handlers, backed by an in-memory SQLite
// store and the on-disk templates. bcrypt cost is forced to MinCost so the
// suite runs in under a second.
//
// The returned emailCapture intercepts every outbound email — tests that
// don't exercise verification/reset can ignore it.
func newTestEnv(t *testing.T) (*httptest.Server, *http.Client, *emailCapture) {
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
	tokens := auth.NewTokenService(st)
	capture := &emailCapture{}
	// Host is non-empty so Enabled() returns true and our capture sendFunc
	// runs in place of net/smtp.
	mailer, err := auth.NewMailer(
		auth.SMTPConfig{Host: "test", Port: 25, From: "noreply@example.com"},
		map[string]string{
			"verify": "<p>Hi {{.Username}}</p><a href=\"{{.VerifyURL}}\">verify</a>",
			"reset":  "<p>Hi {{.Username}}</p><a href=\"{{.ResetURL}}\">reset</a>",
		},
		auth.WithSendFunc(capture.record),
	)
	if err != nil {
		t.Fatalf("build mailer: %v", err)
	}
	h := handler.NewAuth(svc, tokens, mailer, r, handler.AuthConfig{
		Secure:  false,
		BaseURL: "http://example.com",
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/login", h.LoginPage)
	mux.HandleFunc("POST /auth/login", h.Login)
	mux.HandleFunc("GET /auth/register", h.RegisterPage)
	mux.HandleFunc("POST /auth/register", h.Register)
	mux.HandleFunc("POST /auth/logout", h.Logout)
	mux.HandleFunc("GET /auth/verify", h.VerifyEmail)
	mux.HandleFunc("GET /auth/forgot-password", h.ForgotPasswordPage)
	mux.HandleFunc("POST /auth/forgot-password", h.ForgotPassword)
	mux.HandleFunc("GET /auth/reset-password", h.ResetPasswordPage)
	mux.HandleFunc("POST /auth/reset-password", h.ResetPassword)
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
	return srv, client, capture
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
	srv, client, _ := newTestEnv(t)
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
	srv, client, _ := newTestEnv(t)
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
	srv, client, _ := newTestEnv(t)
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
	srv, client, _ := newTestEnv(t)
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
	srv, client, _ := newTestEnv(t)
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
	srv, client, _ := newTestEnv(t)
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
	srv, client, _ := newTestEnv(t)
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

// ── verification + reset flow tests ──────────────────────────────────────────

// registerUser is a tiny helper that creates the named account and returns
// the verification email captured during the request. Subsequent test logic
// pulls the URL out of that email.
func registerUser(t *testing.T, srv *httptest.Server, client *http.Client, capture *emailCapture, username, email, password string) capturedEmail {
	t.Helper()
	csrf := primeCSRF(t, client, srv)
	resp := mustPostForm(t, client, srv.URL+"/auth/register", url.Values{
		"csrf_token": {csrf},
		"username":   {username},
		"email":      {email},
		"password":   {password},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("register: status = %d", resp.StatusCode)
	}

	emails := capture.all()
	if len(emails) == 0 {
		t.Fatal("expected verification email to be sent on register")
	}
	return emails[len(emails)-1]
}

func TestAuth_RegisterSendsVerificationEmail(t *testing.T) {
	srv, client, capture := newTestEnv(t)
	email := registerUser(t, srv, client, capture, "alice", "alice@example.com", "longpassword1")

	if got, want := email.To, []string{"alice@example.com"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("To: got %v, want %v", got, want)
	}
	if !strings.Contains(string(email.Message), "verify") {
		t.Errorf("body missing verify link: %s", email.Message)
	}
	link := extractLink(t, email)
	if !strings.HasPrefix(link, "http://example.com/auth/verify?token=") {
		t.Errorf("link wrong host/path: %s", link)
	}
}

func TestAuth_VerifyEmail_HappyPath(t *testing.T) {
	srv, client, capture := newTestEnv(t)
	email := registerUser(t, srv, client, capture, "bob", "bob@example.com", "longpassword1")
	link := extractLink(t, email)

	// Strip the scheme/host so we hit the test server, keeping the same path+query.
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link: %v", err)
	}
	resp := mustGet(t, client, srv.URL+u.RequestURI())
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify: status = %d, body = %s", resp.StatusCode, got)
	}
	if !strings.Contains(got, "now verified") {
		t.Errorf("verify body missing success: %s", got)
	}

	// Reusing the same link should now show an error.
	resp = mustGet(t, client, srv.URL+u.RequestURI())
	got = body(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("re-verify: status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(got, "invalid or has expired") {
		t.Errorf("re-verify body: %s", got)
	}
}

func TestAuth_VerifyEmail_BadToken(t *testing.T) {
	srv, client, _ := newTestEnv(t)
	resp := mustGet(t, client, srv.URL+"/auth/verify?token=not-a-real-token")
	got := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(got, "invalid or has expired") {
		t.Errorf("body: %s", got)
	}
}

func TestAuth_ForgotPassword_SendsResetEmail(t *testing.T) {
	srv, client, capture := newTestEnv(t)
	_ = registerUser(t, srv, client, capture, "carol", "carol@example.com", "longpassword1")

	// Logout so the forgot-password flow runs as a guest.
	csrf := primeCSRF(t, client, srv)
	resp := mustPostForm(t, client, srv.URL+"/auth/logout", url.Values{"csrf_token": {csrf}})
	resp.Body.Close()

	csrf = primeCSRF(t, client, srv)
	before := len(capture.all())
	resp = mustPostForm(t, client, srv.URL+"/auth/forgot-password", url.Values{
		"csrf_token": {csrf},
		"email":      {"carol@example.com"},
	})
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("forgot: status = %d, body = %s", resp.StatusCode, got)
	}
	if !strings.Contains(got, "reset link has been sent") {
		t.Errorf("body: %s", got)
	}

	emails := capture.all()
	if len(emails) <= before {
		t.Fatal("expected reset email to be queued")
	}
	last := emails[len(emails)-1]
	if !strings.Contains(string(last.Message), "Reset") && !strings.Contains(string(last.Message), "reset") {
		t.Errorf("last email not a reset email: %s", last.Message)
	}
}

func TestAuth_ForgotPassword_UnknownEmailGivesGenericResponse(t *testing.T) {
	srv, client, capture := newTestEnv(t)
	csrf := primeCSRF(t, client, srv)

	resp := mustPostForm(t, client, srv.URL+"/auth/forgot-password", url.Values{
		"csrf_token": {csrf},
		"email":      {"ghost@example.com"},
	})
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, got)
	}
	if !strings.Contains(got, "reset link has been sent") {
		t.Errorf("body should match the happy-path message to prevent enumeration: %s", got)
	}
	if len(capture.all()) != 0 {
		t.Errorf("no email should be sent for unknown address; got %d", len(capture.all()))
	}
}

func TestAuth_ResetPassword_HappyPath(t *testing.T) {
	srv, client, capture := newTestEnv(t)
	_ = registerUser(t, srv, client, capture, "dave", "dave@example.com", "originalpassword")

	// Logout so we hit the reset flow as a guest.
	csrf := primeCSRF(t, client, srv)
	resp := mustPostForm(t, client, srv.URL+"/auth/logout", url.Values{"csrf_token": {csrf}})
	resp.Body.Close()

	// Request the reset link.
	csrf = primeCSRF(t, client, srv)
	resp = mustPostForm(t, client, srv.URL+"/auth/forgot-password", url.Values{
		"csrf_token": {csrf},
		"email":      {"dave@example.com"},
	})
	resp.Body.Close()

	emails := capture.all()
	if len(emails) < 2 {
		t.Fatalf("expected verify + reset emails; got %d", len(emails))
	}
	resetLink := extractLink(t, emails[len(emails)-1])

	u, err := url.Parse(resetLink)
	if err != nil {
		t.Fatalf("parse link: %v", err)
	}

	// GET the reset page to seed the CSRF cookie + render the form.
	resp = mustGet(t, client, srv.URL+u.RequestURI())
	pageBody := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset page: status = %d", resp.StatusCode)
	}
	if !strings.Contains(pageBody, `name="token"`) {
		t.Errorf("reset page missing hidden token field: %s", pageBody)
	}
	csrf = primeCSRF(t, client, srv)

	resp = mustPostForm(t, client, srv.URL+"/auth/reset-password", url.Values{
		"csrf_token":       {csrf},
		"token":            {u.Query().Get("token")},
		"password":         {"brandnewpw1"},
		"password_confirm": {"brandnewpw1"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("reset submit: status = %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/auth/login") {
		t.Errorf("reset redirect: got %q, want prefix /auth/login", loc)
	}

	// Old password no longer works.
	csrf = primeCSRF(t, client, srv)
	resp = mustPostForm(t, client, srv.URL+"/auth/login", url.Values{
		"csrf_token": {csrf},
		"email":      {"dave@example.com"},
		"password":   {"originalpassword"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("login with old password: status = %d, want 401", resp.StatusCode)
	}

	// New password works.
	csrf = primeCSRF(t, client, srv)
	resp = mustPostForm(t, client, srv.URL+"/auth/login", url.Values{
		"csrf_token": {csrf},
		"email":      {"dave@example.com"},
		"password":   {"brandnewpw1"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("login with new password: status = %d, want 303", resp.StatusCode)
	}
}

func TestAuth_ResetPassword_MismatchedConfirm(t *testing.T) {
	srv, client, capture := newTestEnv(t)
	_ = registerUser(t, srv, client, capture, "erin", "erin@example.com", "originalpassword")

	csrf := primeCSRF(t, client, srv)
	resp := mustPostForm(t, client, srv.URL+"/auth/forgot-password", url.Values{
		"csrf_token": {csrf},
		"email":      {"erin@example.com"},
	})
	resp.Body.Close()

	emails := capture.all()
	resetLink := extractLink(t, emails[len(emails)-1])
	u, _ := url.Parse(resetLink)

	csrf = primeCSRF(t, client, srv)
	resp = mustPostForm(t, client, srv.URL+"/auth/reset-password", url.Values{
		"csrf_token":       {csrf},
		"token":            {u.Query().Get("token")},
		"password":         {"newpassword1"},
		"password_confirm": {"different1"},
	})
	got := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(got, "do not match") {
		t.Errorf("body: %s", got)
	}
}

func TestAuth_ResetPassword_BadToken(t *testing.T) {
	srv, client, _ := newTestEnv(t)
	resp := mustGet(t, client, srv.URL+"/auth/reset-password?token=garbage")
	got := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(got, "invalid or has expired") {
		t.Errorf("body: %s", got)
	}
}
