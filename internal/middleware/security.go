package middleware

import (
	"net/http"
	"strings"
)

// SecurityConfig controls the static security response headers applied to
// every response. Zero values are safe defaults — the only knob that varies
// per deployment is whether the ContentSecurityPolicy needs extra hosts (for
// future avatar CDN, embedded media, etc.).
type SecurityConfig struct {
	// ContentSecurityPolicy is the value sent as Content-Security-Policy.
	// If empty, DefaultCSP is used. The default permits same-origin scripts,
	// same-origin + inline styles (HTMX swaps need 'unsafe-inline' on style),
	// images from self/data:/https:, and forbids framing entirely.
	ContentSecurityPolicy string

	// FrameOptions is the legacy clickjacking header. Modern browsers respect
	// CSP frame-ancestors instead, but we send both to cover older agents and
	// to make scanners happy. Default: "DENY".
	FrameOptions string

	// ReferrerPolicy controls how much referrer is leaked on outbound
	// navigations. Default: "strict-origin-when-cross-origin", which sends
	// the origin (no path) on cross-origin requests over HTTPS, and nothing
	// on HTTPS→HTTP downgrades.
	ReferrerPolicy string

	// PermissionsPolicy disables browser features the forum does not use,
	// so a future XSS cannot reach for camera/microphone/geolocation. Default
	// disables a broad set of sensors and ambient APIs.
	PermissionsPolicy string
}

// DefaultCSP is the spec.md §8.3 baseline. It uses 'unsafe-inline' for
// script-src because the templates currently include a handful of inline
// onclick handlers (nav toggle, report form Cancel buttons) and one inline
// <script> in base.html that wires CSRF into HTMX. Tightening this further
// requires migrating those handlers to addEventListener in static JS files;
// CSP nonces alone are not enough because inline event-handler attributes
// cannot carry a nonce. Track that follow-up before removing 'unsafe-inline'.
//
// 'unsafe-inline' for style-src is permanent: HTMX swap animations and the
// many style="..." attributes in templates rely on it.
const DefaultCSP = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: https:; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"frame-ancestors 'none'; " +
	"form-action 'self'; " +
	"base-uri 'self'; " +
	"object-src 'none'"

const (
	defaultFrameOptions      = "DENY"
	defaultReferrerPolicy    = "strict-origin-when-cross-origin"
	defaultPermissionsPolicy = "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=(), interest-cohort=()"
)

// Security writes the standard security headers on every response. It does
// not handle HSTS — that lives in the Proxy middleware so it can be gated on
// the request being actually HTTPS. Place Security outside Proxy in the chain
// so the headers ride along on every response, including 4xx/5xx error pages
// rendered before Proxy resolves the scheme.
//
// Headers set:
//   - Content-Security-Policy
//   - X-Content-Type-Options: nosniff
//   - X-Frame-Options
//   - Referrer-Policy
//   - Permissions-Policy
//
// Headers intentionally NOT set:
//   - Strict-Transport-Security (set by Proxy when scheme is https)
//   - X-XSS-Protection (deprecated, can introduce vulnerabilities on old IE)
func Security(cfg SecurityConfig) func(http.Handler) http.Handler {
	csp := strings.TrimSpace(cfg.ContentSecurityPolicy)
	if csp == "" {
		csp = DefaultCSP
	}
	frame := strings.TrimSpace(cfg.FrameOptions)
	if frame == "" {
		frame = defaultFrameOptions
	}
	ref := strings.TrimSpace(cfg.ReferrerPolicy)
	if ref == "" {
		ref = defaultReferrerPolicy
	}
	perm := strings.TrimSpace(cfg.PermissionsPolicy)
	if perm == "" {
		perm = defaultPermissionsPolicy
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", frame)
			h.Set("Referrer-Policy", ref)
			h.Set("Permissions-Policy", perm)
			next.ServeHTTP(w, r)
		})
	}
}
