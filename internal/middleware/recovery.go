package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recovery catches panics anywhere in the handler chain, logs the stack trace,
// and invokes onPanic to write a 500 response. onPanic receives a
// ResponseWriter that has NOT yet had WriteHeader called — it should write the
// status itself. If onPanic is nil, a plain-text "Internal Server Error" body
// is written instead.
func Recovery(onPanic func(w http.ResponseWriter, r *http.Request)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					slog.Error("panic recovered",
						"error", v,
						"stack", string(debug.Stack()),
						"method", r.Method,
						"path", r.URL.Path,
					)
					if onPanic != nil {
						onPanic(w, r)
					} else {
						http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
					}
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
