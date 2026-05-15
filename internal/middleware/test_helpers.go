package middleware

import "context"

// contextWithRole — хелпер для тестов, устанавливает роль в контекст.
func contextWithRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, contextKey("role"), role)
}
