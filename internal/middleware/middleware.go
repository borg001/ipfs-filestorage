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
func CORS(allowedOrigins []string, allowedHeaders []string) Middleware {
	originStr := strings.Join(allowedOrigins, ",")
	headerStr := strings.Join(allowedHeaders, ",")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", originStr)
			w.Header().Set("Access-Control-Allow-Headers", headerStr)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			if r.Method == http.MethodOptions {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
