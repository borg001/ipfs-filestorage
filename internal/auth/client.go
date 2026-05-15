package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

// UserInfo содержит данные пользователя, полученные от auth-service.
type UserInfo struct {
	UserID       int    `json:"id"`
	Email        string `json:"email"`
	Phone        string `json:"phone"`
	Role         string `json:"role"`
	VerifyStatus string `json:"verify_status"`
}

// cachedEntry — запись в локальном кеше с TTL.
type cachedEntry struct {
	user   *UserInfo
	expiry time.Time
}

// Client обращается к auth-service для проверки токенов.
type Client struct {
	baseURL    string
	httpClient *http.Client
	cache      sync.Map // token string → cachedEntry
	cacheTTL   time.Duration
}

// NewClient создаёт HTTP-клиент к auth-service.
func NewClient(cfg *config.AuthConfig) *Client {
	if cfg == nil || cfg.ServiceURL == "" {
		return nil
	}
	return &Client{
		baseURL: cfg.ServiceURL,
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
		cacheTTL: time.Duration(cfg.CacheTTLMin) * time.Minute,
	}
}

// Validate проверяет токен через auth-service. Возвращает UserInfo или ошибку.
func (c *Client) Validate(ctx context.Context, token string) (*UserInfo, error) {
	if c == nil {
		return nil, ErrUnreachable
	}

	// 1. Проверяем локальный кеш
	if val, ok := c.cache.Load(token); ok {
		entry := val.(cachedEntry)
		if time.Now().Before(entry.expiry) {
			return entry.user, nil
		}
		c.cache.Delete(token)
	}

	// 2. Вызываем auth-service
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/auth/me", nil)
	if err != nil {
		return nil, fmt.Errorf("auth request build: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth service unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrInvalidToken
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, ErrForbidden
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("auth service returned %d: %s", resp.StatusCode, string(body))
	}

	var user UserInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&user); err != nil {
		return nil, fmt.Errorf("decode auth response: %w", err)
	}

	// 3. Сохраняем в кеш
	c.cache.Store(token, cachedEntry{
		user:   &user,
		expiry: time.Now().Add(c.cacheTTL),
	})

	return &user, nil
}

// Invalidate удаляет токен из локального кеша.
func (c *Client) Invalidate(token string) {
	if c != nil {
		c.cache.Delete(token)
	}
}
