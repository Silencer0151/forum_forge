package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/nitro/forum_forge/internal/middleware"
)

func TestLimiter_AllowsUpToLimit(t *testing.T) {
	t.Parallel()
	l := middleware.NewLimiter(3, time.Second)
	now := time.Now()

	for i := 0; i < 3; i++ {
		ok, _ := l.Allow("k", now)
		if !ok {
			t.Fatalf("event %d should be allowed", i+1)
		}
	}

	ok, retry := l.Allow("k", now)
	if ok {
		t.Fatalf("event 4 should be denied (limit=3)")
	}
	if retry <= 0 {
		t.Fatalf("denied event should report a positive Retry-After, got %v", retry)
	}
}

func TestLimiter_WindowSlidesEventsOut(t *testing.T) {
	t.Parallel()
	l := middleware.NewLimiter(2, time.Second)
	t0 := time.Now()

	if ok, _ := l.Allow("k", t0); !ok {
		t.Fatal("first event must be allowed")
	}
	if ok, _ := l.Allow("k", t0.Add(100*time.Millisecond)); !ok {
		t.Fatal("second event must be allowed")
	}
	if ok, _ := l.Allow("k", t0.Add(200*time.Millisecond)); ok {
		t.Fatal("third event should be denied while in window")
	}

	// Slide past the window — the first two events fall out, so the next event
	// should be allowed again.
	if ok, _ := l.Allow("k", t0.Add(2*time.Second)); !ok {
		t.Fatal("event after window should be allowed again")
	}
}

func TestLimiter_KeysAreIndependent(t *testing.T) {
	t.Parallel()
	l := middleware.NewLimiter(1, time.Second)
	now := time.Now()

	if ok, _ := l.Allow("a", now); !ok {
		t.Fatal("first event for a must be allowed")
	}
	if ok, _ := l.Allow("b", now); !ok {
		t.Fatal("first event for b must be allowed (different key)")
	}
	if ok, _ := l.Allow("a", now); ok {
		t.Fatal("second event for a should be denied")
	}
}

func TestRateLimit_NPlusOne_LastReturns429(t *testing.T) {
	t.Parallel()
	const limit = 3
	l := middleware.NewLimiter(limit, time.Minute)
	mw := middleware.RateLimit(l)

	hit := 0
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit++
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < limit; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d/%d: status = %d, want 200", i+1, limit, rr.Code)
		}
	}
	if hit != limit {
		t.Fatalf("handler hit count = %d, want %d", hit, limit)
	}

	// The N+1 request from the same IP must be rejected with 429 + Retry-After.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rr.Code)
	}
	retry := rr.Header().Get("Retry-After")
	if retry == "" {
		t.Fatalf("expected Retry-After header on 429")
	}
	if n, err := strconv.Atoi(retry); err != nil || n < 1 {
		t.Fatalf("Retry-After should be a positive integer, got %q", retry)
	}
	if hit != limit {
		t.Fatalf("handler must not run on rate-limited request; hit = %d", hit)
	}
}
