package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration for the service.
type Config struct {
	Server  ServerConfig
	IPFS    IPFSConfig
	API     APIConfig
	Upload  UploadConfig
	Pinning PinningConfig
	Unpin   UnpinConfig
	CORS    CORSConfig
}

type ServerConfig struct {
	Port string
}

type IPFSConfig struct {
	// URL локальной IPFS-ноды (с которой работает этот инстанс)
	LocalURL string
	// URLs всех нод кластера (включая локальную) для репликации
	ClusterNodes []string
}

type APIConfig struct {
	Keys []string
}

type UploadConfig struct {
	MaxFileSize       int64
	AllowedExtensions []string
	AllowedMimeTypes  map[string]bool
}

type PinningConfig struct {
	Retries        int
	RetryDelayMs   int
}

type UnpinConfig struct {
	// TTL после которого GC физически анпиннит файл из unpin-списка
	TTL        time.Duration
	// Интервал запуска GC-воркера
	GCInterval time.Duration
	// Путь к JSON-файлу для персистентности unpin-списка
	StorePath  string
}

type CORSConfig struct {
	AllowedOrigins []string
	AllowedHeaders []string
}

// Load читает конфигурацию из переменных окружения.
func Load() *Config {
	return &Config{
		Server: ServerConfig{
			Port: getEnv("SERVER_PORT", "3000"),
		},
		IPFS: IPFSConfig{
			LocalURL:     getEnv("IPFS_URL", "http://localhost:5001"),
			ClusterNodes: getEnvSlice("CLUSTER_NODES", []string{"http://localhost:5001"}),
		},
		API: APIConfig{
			Keys: getEnvSlice("API_KEYS", []string{"SECRET_KEY_1", "SECRET_KEY_2"}),
		},
		Upload: UploadConfig{
			MaxFileSize:       getEnvInt64("UPLOAD_MAX_FILE_SIZE", 10*1024*1024),
			AllowedExtensions: getEnvSlice("UPLOAD_ALLOWED_EXTENSIONS", []string{"png", "svg", "jpg", "pdf", "doc", "docx", "zip", "json", "html"}),
			AllowedMimeTypes: map[string]bool{
				"image/png":               true,
				"image/svg+xml":           true,
				"image/jpeg":              true,
				"application/pdf":         true,
				"application/msword":      true,
				"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
				"application/zip":         true,
				"application/json":        true,
				"application/octet-stream": true,
				"text/html":               true,
			},
		},
		Pinning: PinningConfig{
			Retries:      getEnvInt("PINNING_RETRIES", 3),
			RetryDelayMs: getEnvInt("PINNING_RETRY_DELAY_MS", 1000),
		},
		Unpin: UnpinConfig{
			TTL:        getEnvDuration("UNPIN_TTL", 24*time.Hour),
			GCInterval: getEnvDuration("UNPIN_GC_INTERVAL", 1*time.Hour),
			StorePath:  getEnv("UNPIN_STORE_PATH", "/data/unpin-store.json"),
		},
		CORS: CORSConfig{
			AllowedOrigins: getEnvSlice("CORS_ALLOWED_ORIGINS", []string{"*"}),
			AllowedHeaders: getEnvSlice("CORS_ALLOWED_HEADERS", []string{
				"Origin", "X-Requested-With", "Content-Type", "Accept", "X-API-Key",
			}),
		},
	}
}

// --- helpers ---

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvSlice(key string, def []string) []string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			result = append(result, s)
		}
	}
	return result
}

func getEnvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getEnvInt64(key string, def int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
