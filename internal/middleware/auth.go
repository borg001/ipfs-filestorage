package middleware

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/borg001/ipfs-filestorage/internal/auth/lua"
)

type contextKey string

const contextKeyUserID contextKey = "user_id"

// AuthMiddleware checks request authentication:
// 1. Static API keys from API_KEYS env (always, default)
// 2. Lua authorize() script if AUTH_LUA_SCRIPT is set (fallback)
func AuthMiddleware(apiKeys []string, luaProvider *lua.Provider) func(http.Handler) http.Handler {
	validKeys := make(map[string]struct{}, len(apiKeys))
	for _, k := range apiKeys {
		if trimmed := strings.TrimSpace(k); trimmed != "" {
			validKeys[trimmed] = struct{}{}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && r.URL.Path == "/config" {
				next.ServeHTTP(w, r)
				return
			}

			// Extract token from Authorization, X-API-Key, or query string.
			// Query tokens are needed for browser-managed media requests like <img src>.
			token := extractToken(r)

			// 1. Check static API keys (if token present)
			if token != "" {
				if _, ok := validKeys[token]; ok {
					ctx := context.WithValue(r.Context(), contextKeyUserID, "api-key")
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			// 2. Check lua provider (if configured)
			if luaProvider != nil && luaProvider.Enabled() {
				ok, err := luaProvider.Authorize(r.Context(), r)
				if err != nil {
					// Log details server-side only, return generic message to client
					log.Printf("[AUTH] Lua error: %v", err)
					writeAuthError(w, http.StatusUnauthorized, "Authentication failed")
					return
				}
				if ok {
					ctx := context.WithValue(r.Context(), contextKeyUserID, "lua-auth")
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			if token != "" {
				writeAuthError(w, http.StatusForbidden, "Authentication failed")
				return
			}
			writeAuthError(w, http.StatusUnauthorized, "Authentication required")
		})
	}
}

func extractToken(r *http.Request) string {
	// Check Authorization: Bearer <token>
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	// Check X-API-Key
	if key := r.Header.Get("X-API-Key"); key != "" {
		return key
	}
	// Browser image/video tags cannot send Authorization headers.
	if token := r.URL.Query().Get("token"); token != "" {
		return token
	}
	if token := r.URL.Query().Get("access_token"); token != "" {
		return token
	}
	return ""
}

func writeAuthError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}

// UserIDFromContext returns the user_id value from context (for logging/audit).
func UserIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(contextKeyUserID).(string); ok {
		return v
	}
	return ""
}
