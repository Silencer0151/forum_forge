package middleware

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

// Cookie/header/form names exposed so handlers and templates can reuse them.
const (
	CSRFCookieName = "forum_csrf"
	CSRFHeaderName = "X-CSRF-Token"
	CSRFFormField  = "csrf_token"

	csrfTokenBytes = 32
	csrfMaxAge     = 60 * 60 * 24 * 30 // 30 days
)

// CSRFConfig configures the CSRF middleware.
type CSRFConfig struct {
	// Secure controls whether the CSRF cookie is sent only over HTTPS.
	// Set true when BaseURL is https or when running behind a TLS-terminating
	// proxy. In dev (plain HTTP) leave false or the cookie won't stick.
	Secure bool
}

// CSRF enforces double-submit-cookie CSRF protection.
//
// On every request it ensures a forum_csrf cookie is present, generating one
// if needed and stashing the token in the request context (so templates can
// render it into hidden form fields and the <meta name="csrf-token"> used by
// HTMX).
//
// On unsafe methods (POST/PUT/PATCH/DELETE) it requires the submitted value
// — read from the X-CSRF-Token header (HTMX) or the csrf_token form field —
// to match the cookie via constant-time comparison. A mismatch returns 403.
//
// HTMX requests are NOT exempt: the spec calls for HTMX to send X-CSRF-Token,
// which this middleware validates the same way as a form submission.
func CSRF(cfg CSRFConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, err := readOrIssueCSRFToken(w, r, cfg.Secure)
			if err != nil {
				http.Error(w, "csrf: token error", http.StatusInternalServerError)
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), csrfTokenCtxKey, token))

			if isSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			submitted := r.Header.Get(CSRFHeaderName)
			if submitted == "" {
				// ParseForm reads body for POST x-www-form-urlencoded. For
				// multipart, callers typically use r.FormValue inside their
				// handler, which is fine — the token can still arrive via
				// header. Failing ParseForm is non-fatal for the validation
				// step: we just won't find a form-field token.
				_ = r.ParseForm()
				submitted = r.PostFormValue(CSRFFormField)
			}
			if submitted == "" || subtle.ConstantTimeCompare([]byte(submitted), []byte(token)) != 1 {
				http.Error(w, "csrf: invalid or missing token", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// CSRFTokenFromContext returns the per-request CSRF token. Handlers should
// pass this into template data so forms can render `<input type="hidden"
// name="csrf_token" value="...">` and the layout can publish a meta tag for
// HTMX to pick up.
func CSRFTokenFromContext(ctx context.Context) string {
	s, _ := ctx.Value(csrfTokenCtxKey).(string)
	return s
}

func readOrIssueCSRFToken(w http.ResponseWriter, r *http.Request, secure bool) (string, error) {
	if c, err := r.Cookie(CSRFCookieName); err == nil && c.Value != "" {
		return c.Value, nil
	}
	token, err := newCSRFToken()
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   csrfMaxAge,
	})
	return token, nil
}

func newCSRFToken() (string, error) {
	b := make([]byte, csrfTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func isSafeMethod(m string) bool {
	switch strings.ToUpper(m) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}
