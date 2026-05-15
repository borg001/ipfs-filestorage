package config

// AuthConfig — настройки интеграции с auth-service.
type AuthConfig struct {
	ServiceURL   string
	CacheTTLMin  int
}
