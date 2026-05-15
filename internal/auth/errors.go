package auth

import "errors"

var (
	ErrInvalidToken = errors.New("invalid or expired token")
	ErrForbidden    = errors.New("access forbidden")
	ErrUnreachable  = errors.New("auth service unreachable")
)

// IsUnreachable проверяет, что ошибка вызвана недоступностью auth-service.
func IsUnreachable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrUnreachable) {
		return true
	}
	msg := err.Error()
	return contains(msg, "connection refused") ||
		contains(msg, "no such host") ||
		contains(msg, "i/o timeout") ||
		contains(msg, "context deadline exceeded") ||
		contains(msg, "auth service unreachable")
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
