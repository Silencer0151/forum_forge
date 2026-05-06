package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/nitro/forum_forge/internal/model"
	"github.com/nitro/forum_forge/internal/store"
)

// SessionCookieName is the name of the cookie that carries the session ID.
// It must match what auth handlers set on login and clear on logout
// (see spec.md §4.1).
const SessionCookieName = "forum_session"

// AuthStore is the slice of store.Store the auth middleware needs.
// Defining a smaller interface keeps tests trivial — the real *sqlite.Store
// satisfies it implicitly.
type AuthStore interface {
	GetSession(ctx context.Context, id string) (*model.Session, error)
	GetUserByID(ctx context.Context, id int64) (*model.User, error)
	DeleteSession(ctx context.Context, id string) error
}

// Auth resolves the session cookie into a *model.User and attaches it (along
// with the session) to the request context. Missing, expired, or invalid
// cookies leave the context user-less so the request continues as a guest.
//
// A banned user is treated as a guest and their session is destroyed: this is
// what makes a moderator-issued ban take effect on the user's next request
// without waiting for the cookie to expire.
func Auth(st AuthStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName)
			if err != nil || cookie.Value == "" {
				next.ServeHTTP(w, r)
				return
			}

			ctx := r.Context()
			sess, err := st.GetSession(ctx, cookie.Value)
			if err != nil {
				if !errors.Is(err, store.ErrNotFound) {
					slog.Error("auth: get session", "error", err)
				}
				clearSessionCookie(w)
				next.ServeHTTP(w, r)
				return
			}
			if sess == nil { // expired
				clearSessionCookie(w)
				next.ServeHTTP(w, r)
				return
			}

			user, err := st.GetUserByID(ctx, sess.UserID)
			if err != nil {
				if !errors.Is(err, store.ErrNotFound) {
					slog.Error("auth: get user", "error", err)
				}
				clearSessionCookie(w)
				next.ServeHTTP(w, r)
				return
			}
			if user.Banned {
				_ = st.DeleteSession(ctx, sess.ID)
				clearSessionCookie(w)
				next.ServeHTTP(w, r)
				return
			}

			ctx = context.WithValue(ctx, userCtxKey, user)
			ctx = context.WithValue(ctx, sessionCtxKey, sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserFromContext returns the authenticated user, or nil for guests.
func UserFromContext(ctx context.Context) *model.User {
	u, _ := ctx.Value(userCtxKey).(*model.User)
	return u
}

// SessionFromContext returns the session attached to the request, or nil if
// the request is unauthenticated.
func SessionFromContext(ctx context.Context) *model.Session {
	s, _ := ctx.Value(sessionCtxKey).(*model.Session)
	return s
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
