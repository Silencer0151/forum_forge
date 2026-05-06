package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nitro/forum_forge/internal/auth"
	"github.com/nitro/forum_forge/internal/middleware"
	"github.com/nitro/forum_forge/internal/model"
)

// stubAuthenticator implements auth.Authenticator for tests. It scripts the
// outcome of AuthenticateRequest based on the inbound cookie value so each
// scenario can be expressed as a plain map lookup.
type stubAuthenticator struct {
	// outcomes is keyed by session cookie value. A nil entry means "guest".
	outcomes map[string]stubOutcome
	// noCookieErr, when set, is returned for requests that don't carry the
	// session cookie at all. Used to verify the middleware does not call
	// AuthenticateRequest in surprising ways.
	noCookieErr error
}

type stubOutcome struct {
	user *model.User
	sess *model.Session
	err  error
}

func (s *stubAuthenticator) AuthenticateRequest(r *http.Request) (*model.User, *model.Session, error) {
	cookie, err := r.Cookie(middleware.SessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, nil, s.noCookieErr
	}
	out, ok := s.outcomes[cookie.Value]
	if !ok {
		return nil, nil, auth.ErrSessionInvalid
	}
	return out.user, out.sess, out.err
}

func (s *stubAuthenticator) Authenticate(*http.Request) (*auth.ExternalUser, error) {
	// Not exercised by middleware tests — the middleware only calls
	// AuthenticateRequest. Returning ErrNotImplemented here would mask
	// accidental call sites; nil-nil keeps the stub permissive.
	return nil, nil
}

func (s *stubAuthenticator) GetUser(string) (*auth.ExternalUser, error) {
	return nil, nil
}

func (s *stubAuthenticator) GetAvatarURL(string) (string, error) {
	return "", nil
}

// peekHandler captures whatever the auth middleware injected into the
// request context so the test can assert on it.
type peekHandler struct {
	gotUser *model.User
	gotSess *model.Session
}

func (p *peekHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.gotUser = middleware.UserFromContext(r.Context())
	p.gotSess = middleware.SessionFromContext(r.Context())
	w.WriteHeader(http.StatusOK)
}

func TestAuth_NoCookie_GuestPassthrough(t *testing.T) {
	t.Parallel()
	a := &stubAuthenticator{}
	peek := &peekHandler{}
	h := middleware.Auth(a)(peek)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if peek.gotUser != nil {
		t.Errorf("guest request should have nil user, got %+v", peek.gotUser)
	}
	if peek.gotSess != nil {
		t.Errorf("guest request should have nil session")
	}
}

func TestAuth_ValidSession_InjectsUser(t *testing.T) {
	t.Parallel()
	user := &model.User{ID: 42, Username: "alice", Role: model.RoleMember}
	sess := &model.Session{ID: "sess-1", UserID: 42, ExpiresAt: time.Now().Add(time.Hour)}
	a := &stubAuthenticator{
		outcomes: map[string]stubOutcome{
			"sess-1": {user: user, sess: sess},
		},
	}
	peek := &peekHandler{}
	h := middleware.Auth(a)(peek)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess-1"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if peek.gotUser == nil || peek.gotUser.ID != 42 {
		t.Fatalf("expected user 42 in context, got %+v", peek.gotUser)
	}
	if peek.gotSess == nil || peek.gotSess.ID != "sess-1" {
		t.Fatalf("expected session in context, got %+v", peek.gotSess)
	}
}

func TestAuth_InvalidSession_ClearsCookie(t *testing.T) {
	t.Parallel()
	a := &stubAuthenticator{
		outcomes: map[string]stubOutcome{
			"ghost": {err: auth.ErrSessionInvalid},
		},
	}
	peek := &peekHandler{}
	h := middleware.Auth(a)(peek)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "ghost"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if peek.gotUser != nil {
		t.Fatalf("invalid session should leave context user-less")
	}
	if !cookieCleared(rr, middleware.SessionCookieName) {
		t.Fatalf("expected session cookie to be cleared on invalid session")
	}
}

func TestAuth_AdapterError_LogsAndPassesThrough(t *testing.T) {
	t.Parallel()
	// A non-ErrSessionInvalid error should be logged but should not block
	// the request — guests degrade gracefully.
	a := &stubAuthenticator{
		outcomes: map[string]stubOutcome{
			"boom": {err: auth.ErrNotImplemented},
		},
	}
	peek := &peekHandler{}
	h := middleware.Auth(a)(peek)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "boom"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if peek.gotUser != nil {
		t.Fatalf("adapter error should leave context user-less")
	}
}

// cookieCleared reports whether the response sets the named cookie with a
// past-dated MaxAge (i.e., asks the client to delete it).
func cookieCleared(rr *httptest.ResponseRecorder, name string) bool {
	for _, c := range rr.Result().Cookies() {
		if c.Name == name && c.MaxAge < 0 {
			return true
		}
	}
	return false
}
