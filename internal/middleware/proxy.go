package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// DefaultHSTS is the recommended Strict-Transport-Security value for the
// forum: two years, includeSubDomains. Set when serving over HTTPS.
const DefaultHSTS = "max-age=63072000; includeSubDomains"

// ProxyConfig controls how the middleware extracts client info from forwarded
// headers and whether it sets HSTS.
type ProxyConfig struct {
	// TrustProxy enables interpreting X-Forwarded-For / X-Forwarded-Proto.
	// Only enable behind a proxy you control — clients can otherwise spoof
	// their IP via X-Forwarded-For and bypass per-IP rate limits.
	TrustProxy bool

	// HSTSValue is the Strict-Transport-Security header value to send when
	// the request is HTTPS. Empty disables HSTS (appropriate for plain-HTTP
	// dev). Use DefaultHSTS in production TLS modes.
	HSTSValue string
}

// Proxy normalizes per-request fields based on X-Forwarded-* headers (when
// TrustProxy is enabled) and adds HSTS to HTTPS responses.
//
// It stashes the resolved client IP in the request context so downstream
// middleware (rate limit, logging) sees the real client address rather than
// the proxy's. r.URL.Scheme is filled in from X-Forwarded-Proto so handlers
// can detect HTTPS uniformly across deployment modes.
func Proxy(cfg ProxyConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := remoteIP(r)
			if cfg.TrustProxy {
				if forwarded := firstForwardedFor(r.Header.Get("X-Forwarded-For")); forwarded != "" {
					ip = forwarded
				}
				if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" && r.URL.Scheme == "" {
					r.URL.Scheme = strings.ToLower(proto)
				}
			}
			r = r.WithContext(context.WithValue(r.Context(), clientIPCtxKey, ip))

			if cfg.HSTSValue != "" && requestIsHTTPS(r, cfg.TrustProxy) {
				w.Header().Set("Strict-Transport-Security", cfg.HSTSValue)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP exposes the resolved client IP to callers outside this package
// (e.g. handlers building rate-limit keys or CAPTCHA verification calls).
func ClientIP(r *http.Request) string { return clientIP(r) }

// clientIP returns the request's client IP, preferring the value set by the
// Proxy middleware (when TRUST_PROXY is on) and falling back to RemoteAddr.
// Used by the rate limit middleware and the request logger.
func clientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPCtxKey).(string); ok && ip != "" {
		return ip
	}
	return remoteIP(r)
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// firstForwardedFor returns the leftmost (originating client) entry from a
// comma-separated X-Forwarded-For value. If TrustProxy is on we accept that
// the immediate proxy filled the header honestly.
func firstForwardedFor(h string) string {
	if h == "" {
		return ""
	}
	if idx := strings.IndexByte(h, ','); idx >= 0 {
		return strings.TrimSpace(h[:idx])
	}
	return strings.TrimSpace(h)
}

func requestIsHTTPS(r *http.Request, trustProxy bool) bool {
	if r.TLS != nil {
		return true
	}
	if trustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return false
}
