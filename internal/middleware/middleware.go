package middleware

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

// Middleware is a standard HTTP middleware function.
type Middleware func(http.Handler) http.Handler

// Chain wraps a handler with a list of middlewares from outer to inner.
func Chain(h http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

// PanicRecovery recovers from panics, writes them to stderr and returns 500.
func PanicRecovery() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					fmt.Fprintf(os.Stderr, "[PANIC] %s %s: %v\n", r.Method, r.URL.Path, rec)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte(`{"error":"internal server error"}`))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// CORS sets configurable CORS headers and handles OPTIONS pre-flight.
// If allowedOrigins is empty, no CORS headers are set (production-safe default).
// Access-Control-Allow-Origin holds exactly one origin: browsers reject a
// comma-separated list, so with several origins the request's own Origin is
// returned when it is allowed.
func CORS(allowedOrigins []string, allowedHeaders []string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(allowedOrigins) > 0 {
				originStr := allowedOrigin(allowedOrigins, r.Header.Get("Origin"))
				headerStr := strings.Join(allowedHeaders, ",")
				if len(allowedOrigins) > 1 {
					w.Header().Add("Vary", "Origin")
				}
				if originStr != "" {
					w.Header().Set("Access-Control-Allow-Origin", originStr)
				}
				w.Header().Set("Access-Control-Allow-Headers", headerStr)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			}

			if r.Method == http.MethodOptions {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func allowedOrigin(allowedOrigins []string, origin string) string {
	if len(allowedOrigins) == 1 {
		return allowedOrigins[0]
	}
	for _, allowed := range allowedOrigins {
		if allowed == "*" {
			return "*"
		}
	}
	for _, allowed := range allowedOrigins {
		if origin != "" && strings.EqualFold(allowed, origin) {
			return origin
		}
	}
	return ""
}

// APIKeyAuth is the legacy static-only middleware (kept for backwards compatibility).
func APIKeyAuth(apiKeys []string) Middleware {
	return AuthMiddleware(apiKeys, nil)
}
