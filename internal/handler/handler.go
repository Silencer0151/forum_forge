// Package handler hosts the HTTP handlers that turn parsed requests into
// rendered responses. Handlers depend on the store (for data), the renderer
// (for HTML), and a small auth service (for sign-in flows). Cross-cutting
// concerns like CSRF and rate limiting live in internal/middleware and are
// applied to the router by the server package.
package handler

import (
	"net/http"
	"time"

	"github.com/nitro/forum_forge/internal/middleware"
)

// BaseData returns the layout-level template variables every page needs:
// the authenticated user (or nil for guests), the per-request CSRF token,
// flash messages consumed from the flash cookie, and the current year for the
// footer copyright. Page handlers compose this with page-specific keys before
// calling Render.
func BaseData(r *http.Request) map[string]any {
	return map[string]any{
		"User":      middleware.UserFromContext(r.Context()),
		"CSRFToken": middleware.CSRFTokenFromContext(r.Context()),
		"Flashes":   middleware.FlashesFromContext(r.Context()),
		"Year":      time.Now().Year(),
	}
}
