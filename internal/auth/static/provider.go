package static

import (
	"context"
	"strings"

	"github.com/borg001/ipfs-filestorage/internal/auth"
)

// Provider проверяет запрос по статическим API-ключам из окружения.
type Provider struct {
	keys map[string]struct{}
}

// New создаёт Provider из списка ключей.
func New(keys []string) *Provider {
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if t := strings.TrimSpace(k); t != "" {
			m[t] = struct{}{}
		}
	}
	return &Provider{keys: m}
}

// Authorize проверяет заголовки на наличие валидного API-ключа.
// Поддерживаются заголовки X-API-Key и Authorization: Bearer.
func (p *Provider) Authorize(_ context.Context, headers map[string]string) (*auth.Result, error) {
	token := extractToken(headers)
	if token == "" {
		return nil, errNoToken
	}
	if _, ok := p.keys[token]; ok {
		return &auth.Result{Role: "api-key"}, nil
	}
	return nil, errInvalidToken
}

func extractToken(headers map[string]string) string {
	if v := headers["X-Api-Key"]; v != "" {
		return v
	}
	if v := headers["Authorization"]; strings.HasPrefix(v, "Bearer ") {
		return strings.TrimPrefix(v, "Bearer ")
	}
	return ""
}
