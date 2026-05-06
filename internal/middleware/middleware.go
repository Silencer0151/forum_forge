// Package middleware contains the HTTP middleware that wraps every request
// served by the forum: proxy normalization, structured logging, session/user
// resolution, sliding-window rate limiting, and CSRF enforcement.
//
// All middleware is shaped as func(http.Handler) http.Handler so it can be
// composed with Chain. The package keeps a single unexported context-key type
// so request-scoped values (user, session, csrf token, client IP) can be
// passed across middleware boundaries without collisions.
package middleware

import "net/http"

// ctxKey is the unexported type used for all request-scoped keys in this
// package. Keeping it private prevents downstream packages from injecting
// values under the same keys and silently corrupting middleware state.
type ctxKey int

const (
	userCtxKey ctxKey = iota
	sessionCtxKey
	csrfTokenCtxKey
	clientIPCtxKey
)

// Chain composes middleware so that the leftmost argument is outermost on the
// request path. Chain(a, b, c)(h) is equivalent to a(b(c(h))) — meaning a runs
// first on the way in and last on the way out.
func Chain(mws ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		for i := len(mws) - 1; i >= 0; i-- {
			h = mws[i](h)
		}
		return h
	}
}
