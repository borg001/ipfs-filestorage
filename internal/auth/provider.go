package auth

import "context"

// Result содержит результат проверки авторизации.
type Result struct {
	UserID int
	Role   string
}

// Provider проверяет авторизацию запроса.
type Provider interface {
	Authorize(ctx context.Context, headers map[string]string) (*Result, error)
}
