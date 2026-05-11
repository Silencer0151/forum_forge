package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nitro/forum_forge/internal/middleware"
)

func TestSecurityDefaultHeaders(t *testing.T) {
	h := middleware.Security(middleware.SecurityConfig{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}

	csp := rec.Header().Get("Content-Security-Policy")
	for _, sub := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'", "form-action 'self'"} {
		if !strings.Contains(csp, sub) {
			t.Errorf("CSP missing %q; got %q", sub, csp)
		}
	}

	if perm := rec.Header().Get("Permissions-Policy"); !strings.Contains(perm, "camera=()") {
		t.Errorf("Permissions-Policy missing camera=(); got %q", perm)
	}

	// Security middleware does NOT set HSTS — that is Proxy's responsibility,
	// gated on the request actually being HTTPS.
	if hsts := rec.Header().Get("Strict-Transport-Security"); hsts != "" {
		t.Errorf("Security should not set HSTS, got %q", hsts)
	}
}

func TestSecurityCustomCSP(t *testing.T) {
	custom := "default-src 'none'"
	h := middleware.Security(middleware.SecurityConfig{ContentSecurityPolicy: custom})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Content-Security-Policy"); got != custom {
		t.Errorf("CSP = %q, want %q", got, custom)
	}
}
