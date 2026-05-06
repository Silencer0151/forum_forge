package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/nitro/forum_forge/internal/auth"
	"github.com/nitro/forum_forge/internal/model"
)

// SessionCookieName is the name of the cookie that carries the standalone
// session ID. Mirrors auth.SessionCookieName so middleware/handler code can
// reference it without importing the auth package directly. Integrated mode
// (Kanoogi) uses its own cookie scheme owned by the adapter.
const SessionCookieName = auth.SessionCookieName

// Auth resolves the request's authentication state through the supplied
// auth.Authenticator and attaches the resulting *model.User and (when
// applicable) *model.Session to the request context.
//
// Adapter selection happens at construction (server wiring): standalone for
// FORUM_AUTH_MODE=standalone, kanoogi for integrated. The middleware itself
// is mode-agnostic — it just calls AuthenticateRequest, which dispatches via
// AuthAdapter.Authenticate underneath. Treating the adapter as a black box
// here is what lets task 3.3's interface design swap implementations
// without touching the middleware chain.
//
// On auth.ErrSessionInvalid the middleware clears the session cookie and
// passes the request through as a guest. Any other error is logged and the
// request also degrades to guest — middleware never blocks a request just
// because the auth lookup hiccupped.
func Auth(a auth.Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, sess, err := a.AuthenticateRequest(r)
			if err != nil {
				if errors.Is(err, auth.ErrSessionInvalid) {
					clearSessionCookie(w)
				} else if !errors.Is(err, auth.ErrNotImplemented) {
					slog.Error("auth: resolve request", "error", err)
				}
				next.ServeHTTP(w, r)
				return
			}
			if user == nil {
				next.ServeHTTP(w, r)
				return
			}

			ctx := r.Context()
			ctx = context.WithValue(ctx, userCtxKey, user)
			if sess != nil {
				ctx = context.WithValue(ctx, sessionCtxKey, sess)
			}
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
// the request is unauthenticated or the adapter does not maintain a local
// session row (integrated mode).
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
