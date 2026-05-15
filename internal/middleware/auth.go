package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/borg001/ipfs-filestorage/internal/auth"
)

type contextKey string

const ContextKeyUserID contextKey = "user_id"

// AuthMiddleware проверяет доступ через auth-service или fallback на статические API-ключи.
// Если сессия активна — пропускает запрос дальше.
func AuthMiddleware(authClient *auth.Client, apiKeys []string) func(http.Handler) http.Handler {
	validKeys := make(map[string]struct{}, len(apiKeys))
	for _, k := range apiKeys {
		if trimmed := strings.TrimSpace(k); trimmed != "" {
			validKeys[trimmed] = struct{}{}
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractToken(r)
			if token == "" {
				writeAuthError(w, http.StatusUnauthorized, "Authorization token required")
				return
			}

			// Попытка через auth-service
			if authClient != nil {
				user, err := authClient.Validate(r.Context(), token)
				if err == nil {
					ctx := context.WithValue(r.Context(), ContextKeyUserID, user.UserID)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				if !auth.IsUnreachable(err) {
					if err == auth.ErrForbidden {
						writeAuthError(w, http.StatusForbidden, "Access forbidden")
					} else {
						writeAuthError(w, http.StatusUnauthorized, "Invalid or expired token")
					}
					return
				}
			}

			// Fallback: статические API-ключи
			if _, ok := validKeys[token]; ok {
				next.ServeHTTP(w, r)
				return
			}

			writeAuthError(w, http.StatusUnauthorized, "Invalid API key")
		})
	}
}

// extractToken извлекает токен из заголовков.
func extractToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		if t := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer ")); t != "" {
			return t
		}
	}
	if key := strings.TrimSpace(r.Header.Get("X-API-Key")); key != "" {
		return key
	}
	return ""
}

func writeAuthError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write([]byte(`{"error":"` + msg + `"}`))
}

// UserIDFromContext возвращает user_id из контекста запроса (0 если нет).
func UserIDFromContext(ctx context.Context) int {
	id, _ := ctx.Value(ContextKeyUserID).(int)
	return id
}
