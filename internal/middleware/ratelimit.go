package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Limiter is an in-memory sliding-window rate limiter keyed by an arbitrary
// string. It is safe for concurrent use.
//
// The window is enforced as: at most `limit` events may occur in any rolling
// `window`-length interval. Older events are pruned lazily on each Allow.
//
// Memory: the events map grows with the number of distinct keys. For a forum
// at the scale this codebase targets that's bounded by active users + IPs —
// fine without explicit cleanup. If usage scales up, add a janitor goroutine.
type Limiter struct {
	mu     sync.Mutex
	events map[string][]time.Time
	limit  int
	window time.Duration
}

// NewLimiter constructs a Limiter that allows at most limit events per window.
func NewLimiter(limit int, window time.Duration) *Limiter {
	return &Limiter{
		events: make(map[string][]time.Time),
		limit:  limit,
		window: window,
	}
}

// Allow records an event for key at time now. It returns whether the event is
// allowed and, when denied, how long until the next event would be permitted
// (suitable for a Retry-After header).
func (l *Limiter) Allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-l.window)
	events := l.events[key]
	pruned := events[:0]
	for _, t := range events {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}

	if len(pruned) >= l.limit {
		retry := pruned[0].Add(l.window).Sub(now)
		if retry < time.Second {
			retry = time.Second
		}
		l.events[key] = pruned
		return false, retry
	}

	l.events[key] = append(pruned, now)
	return true, 0
}

// RateLimit applies the given Limiter to every request flowing through it.
//
// Keying: authenticated requests are bucketed per user (so a user gets one
// quota across devices), anonymous requests per client IP. Place this
// middleware AFTER Auth so the user lookup has already happened.
func RateLimit(l *Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ok, retry := l.Allow(rateKey(r), time.Now())
			if !ok {
				secs := int(retry.Round(time.Second).Seconds())
				if secs < 1 {
					secs = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func rateKey(r *http.Request) string {
	if u := UserFromContext(r.Context()); u != nil {
		return fmt.Sprintf("u:%d", u.ID)
	}
	return "ip:" + clientIP(r)
}
