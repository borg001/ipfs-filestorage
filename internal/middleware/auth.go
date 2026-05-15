package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/borg001/ipfs-filestorage/internal/auth"
	"github.com/borg001/ipfs-filestorage/internal/config"
)

type contextKey string

const (
	ContextKeyUserID  contextKey = "user_id"
	ContextKeyRole     contextKey = "role"
	ContextKeyAuthType contextKey = "auth_type"
)

// AuthMiddleware проверяет доступ через auth-service или fallback на статические API-ключи.
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
					ctx = context.WithValue(ctx, ContextKeyRole, user.Role)
					ctx = context.WithValue(ctx, ContextKeyAuthType, "jwt")
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
				// auth-service недоступен — fallback ниже
			}

			// Fallback: статические API-ключи
			if _, ok := validKeys[token]; ok {
				ctx := context.WithValue(r.Context(), ContextKeyRole, "api-key")
				ctx = context.WithValue(ctx, ContextKeyAuthType, "api-key")
				next.ServeHTTP(w, r.WithContext(ctx))
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

// RequireRole проверяет, что роль пользователя входит в разрешённый список.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, _ := r.Context().Value(ContextKeyRole).(string)
			if role == "" {
				writeAuthError(w, http.StatusUnauthorized, "Not authenticated")
				return
			}
			if _, ok := allowed[role]; !ok {
				writeAuthError(w, http.StatusForbidden, "Access denied: insufficient role")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeAuthError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write([]byte(`{"error":"` + msg + `"}`))
}

// RoleFromContext возвращает роль из контекста запроса.
func RoleFromContext(ctx context.Context) string {
	role, _ := ctx.Value(ContextKeyRole).(string)
	return role
}

// UserIDFromContext возвращает user_id из контекста запроса.
func UserIDFromContext(ctx context.Context) int {
	id, _ := ctx.Value(ContextKeyUserID).(int)
	return id
}

// AuthTypeFromContext возвращает тип авторизации из контекста.
func AuthTypeFromContext(ctx context.Context) string {
	at, _ := ctx.Value(ContextKeyAuthType).(string)
	return at
}
