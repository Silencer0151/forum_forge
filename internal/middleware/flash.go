package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

const flashCookieName = "forum_flash"

// FlashCategory is the visual style applied to a flash message.
type FlashCategory string

const (
	FlashSuccess FlashCategory = "success"
	FlashError   FlashCategory = "error"
	FlashWarning FlashCategory = "warning"
	FlashInfo    FlashCategory = "info"
)

// Flash is a single user-visible notification stored in a short-lived cookie
// and consumed exactly once on the next page load.
type Flash struct {
	Category FlashCategory `json:"category"`
	Message  string        `json:"message"`
}

// SetFlash queues a flash message to be shown on the next full-page render.
// It reads any previously queued messages from the cookie and appends, so
// multiple calls within one request accumulate correctly.
func SetFlash(w http.ResponseWriter, r *http.Request, category FlashCategory, message string) {
	var existing []Flash
	if cookie, err := r.Cookie(flashCookieName); err == nil && cookie.Value != "" {
		_ = json.Unmarshal([]byte(cookie.Value), &existing)
	}
	existing = append(existing, Flash{Category: category, Message: message})
	b, _ := json.Marshal(existing)
	http.SetCookie(w, &http.Cookie{
		Name:     flashCookieName,
		Value:    string(b),
		Path:     "/",
		MaxAge:   120,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// FlashesFromContext returns flash messages injected by the Flash middleware.
// Returns nil if the middleware did not run or found no messages.
func FlashesFromContext(ctx context.Context) []Flash {
	v, _ := ctx.Value(flashCtxKey).([]Flash)
	return v
}

// Flash reads the flash cookie, injects messages into the request context, and
// immediately clears the cookie so each message is shown exactly once.
func FlashMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(flashCookieName)
		if err == nil && cookie.Value != "" {
			var flashes []Flash
			if jsonErr := json.Unmarshal([]byte(cookie.Value), &flashes); jsonErr == nil && len(flashes) > 0 {
				// Clear cookie immediately — show once, discard.
				http.SetCookie(w, &http.Cookie{
					Name:     flashCookieName,
					Value:    "",
					Path:     "/",
					MaxAge:   -1,
					Expires:  time.Unix(0, 0),
					HttpOnly: true,
					SameSite: http.SameSiteLaxMode,
				})
				ctx := context.WithValue(r.Context(), flashCtxKey, flashes)
				r = r.WithContext(ctx)
			}
		}
		next.ServeHTTP(w, r)
	})
}
