package middleware

import (
	"net/http"
	"strings"
)

// APIKeyAuth returns middleware that checks X-API-Key header.
func APIKeyAuth(keys []string) func(http.Handler) http.Handler {
	validKeys := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if trimmed := strings.TrimSpace(k); trimmed != "" {
			validKeys[trimmed] = struct{}{}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("X-API-Key")
			if key == "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"API key is required"}`))
				return
			}

			if _, ok := validKeys[key]; !ok {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"Invalid API key"}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
