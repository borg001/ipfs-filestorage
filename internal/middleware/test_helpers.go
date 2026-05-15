package middleware

import (
	"context"
)

// contextWithUserID — хелпер для тестов, устанавливает user_id в контекст.
func contextWithUserID(ctx context.Context, id int) context.Context {
	return context.WithValue(ctx, ContextKeyUserID, id)
}
