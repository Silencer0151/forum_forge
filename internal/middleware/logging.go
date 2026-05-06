package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// statusRecorder captures the response status (and bytes written) so the
// request logger can include them after the handler returns.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Logging emits a structured log line per request. Place it AFTER Auth in the
// chain so the user_id field is populated for authenticated requests; the
// trade-off is that auth-lookup latency is not included in duration_ms (an
// acceptable cost — handler+template work dominates).
//
// Status family drives the log level: 5xx → error, 4xx → warn, otherwise info.
func Logging(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sr := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(sr, r)

			status := sr.status
			if status == 0 {
				status = http.StatusOK
			}

			attrs := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"status", status,
				"bytes", sr.bytes,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote", clientIP(r),
			}
			if u := UserFromContext(r.Context()); u != nil {
				attrs = append(attrs, "user_id", u.ID)
			}

			switch {
			case status >= 500:
				logger.Error("http", attrs...)
			case status >= 400:
				logger.Warn("http", attrs...)
			default:
				logger.Info("http", attrs...)
			}
		})
	}
}
