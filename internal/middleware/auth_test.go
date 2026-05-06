package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nitro/forum_forge/internal/middleware"
	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
)

// stubAuthStore implements middleware.AuthStore for tests.
type stubAuthStore struct {
	sessions       map[string]*model.Session
	sessionErr     error
	users          map[int64]*model.User
	userErr        error
	deletedSession string
}

func (s *stubAuthStore) GetSession(_ context.Context, id string) (*model.Session, error) {
	if s.sessionErr != nil {
		return nil, s.sessionErr
	}
	sess, ok := s.sessions[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	if !sess.ExpiresAt.IsZero() && time.Now().After(sess.ExpiresAt) {
		return nil, nil
	}
	return sess, nil
}

func (s *stubAuthStore) GetUserByID(_ context.Context, id int64) (*model.User, error) {
	if s.userErr != nil {
		return nil, s.userErr
	}
	u, ok := s.users[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return u, nil
}

func (s *stubAuthStore) DeleteSession(_ context.Context, id string) error {
	s.deletedSession = id
	delete(s.sessions, id)
	return nil
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
	st := &stubAuthStore{}
	peek := &peekHandler{}
	h := middleware.Auth(st)(peek)

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
	st := &stubAuthStore{
		sessions: map[string]*model.Session{"sess-1": sess},
		users:    map[int64]*model.User{42: user},
	}
	peek := &peekHandler{}
	h := middleware.Auth(st)(peek)

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

func TestAuth_UnknownSession_ClearsCookie(t *testing.T) {
	t.Parallel()
	st := &stubAuthStore{}
	peek := &peekHandler{}
	h := middleware.Auth(st)(peek)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "ghost"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if peek.gotUser != nil {
		t.Fatalf("unknown session should leave context user-less")
	}
	if !cookieCleared(rr, middleware.SessionCookieName) {
		t.Fatalf("expected session cookie to be cleared on unknown session")
	}
}

func TestAuth_ExpiredSession_ClearsCookie(t *testing.T) {
	t.Parallel()
	expired := &model.Session{ID: "old", UserID: 1, ExpiresAt: time.Now().Add(-time.Minute)}
	st := &stubAuthStore{
		sessions: map[string]*model.Session{"old": expired},
		users:    map[int64]*model.User{1: {ID: 1, Username: "x"}},
	}
	peek := &peekHandler{}
	h := middleware.Auth(st)(peek)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "old"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if peek.gotUser != nil {
		t.Fatalf("expired session should leave context user-less")
	}
	if !cookieCleared(rr, middleware.SessionCookieName) {
		t.Fatalf("expected session cookie to be cleared on expired session")
	}
}

func TestAuth_BannedUser_DeletesSessionAndClearsCookie(t *testing.T) {
	t.Parallel()
	user := &model.User{ID: 7, Username: "bad", Banned: true}
	sess := &model.Session{ID: "sess-7", UserID: 7, ExpiresAt: time.Now().Add(time.Hour)}
	st := &stubAuthStore{
		sessions: map[string]*model.Session{"sess-7": sess},
		users:    map[int64]*model.User{7: user},
	}
	peek := &peekHandler{}
	h := middleware.Auth(st)(peek)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "sess-7"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if peek.gotUser != nil {
		t.Fatalf("banned user should not be present in context")
	}
	if st.deletedSession != "sess-7" {
		t.Fatalf("expected session sess-7 to be deleted, got %q", st.deletedSession)
	}
	if !cookieCleared(rr, middleware.SessionCookieName) {
		t.Fatalf("expected session cookie to be cleared on ban")
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
