package middleware

import "context"

func contextWithRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, contextKey("role"), role)
}
