package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration for the service.
type Config struct {
	Server   ServerConfig
	IPFS     IPFSConfig
	API      APIConfig
	Upload   UploadConfig
	Pinning  PinningConfig
	Unpin    UnpinConfig
	CORS     CORSConfig
	Video    VideoConfig
	Auth     AuthConfig
	RateLimit RateLimitConfig
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
	Retries      int
	RetryDelayMs int
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

// VideoConfig — настройки видеопроцессинга и стриминга.
type VideoConfig struct {
	// Макс. длительность видео (сек)
	MaxDurationSec int
	// Макс. размер исходного файла (байт)
	MaxSizeBytes int64
	// Допустимое отклонение от пропорции 9:16
	AspectRatioTolerance float64
	// Длительность одного чанка (сек)
	SegmentDurationSec int
	// Список битрейтов для адаптивного стриминга (напр. ["500k","1500k","4000k"])
	Bitrates []string
	// Путь к бинарнику ffmpeg
	FFmpegPath string
	// Путь к бинарнику ffprobe
	FFprobePath string
	// Временная директория для обработки
	TempDir string
}

// AuthConfig — настройки аутентификации.
type AuthConfig struct {
	// LuaScript is the path to .lua file (empty = disabled).
	LuaScript string
	// LuaTimeoutMs is the max execution time in milliseconds.
	LuaTimeoutMs int
	// LuaEnvWhitelist is a comma-separated list of env var names accessible via env.get().
	LuaEnvWhitelist string
	// LuaMaxMemoryMB is the max VM memory in megabytes.
	LuaMaxMemoryMB int
}

// RateLimitConfig — настройки rate limiting.
type RateLimitConfig struct {
	RPS   float64
	Burst int
}

// Load читает конфигурацию из переменных окружения.
func Load() *Config {
	corsOrigins := getEnvSlice("CORS_ALLOWED_ORIGINS", []string{})
	if len(corsOrigins) == 0 {
		fmt.Fprintln(os.Stderr, "[WARN] CORS_ALLOWED_ORIGINS is empty — no CORS headers will be set. Set explicit origins for production.")
	}

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
			AllowedExtensions: getEnvSlice("UPLOAD_ALLOWED_EXTENSIONS", []string{"png", "svg", "jpg", "pdf", "doc", "docx", "zip", "json", "html", "mp4", "mov", "webm", "avi"}),
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
				"video/mp4":               true,
				"video/quicktime":         true,
				"video/webm":              true,
				"video/x-msvideo":         true,
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
			AllowedOrigins: corsOrigins,
			AllowedHeaders: getEnvSlice("CORS_ALLOWED_HEADERS", []string{
				"Origin", "X-Requested-With", "Content-Type", "Accept", "X-API-Key",
			}),
		},
		Video: VideoConfig{
			MaxDurationSec:       getEnvInt("VIDEO_MAX_DURATION_SEC", 60),
			MaxSizeBytes:         getEnvInt64("VIDEO_MAX_SIZE_MB", 30) * 1024 * 1024,
			AspectRatioTolerance: getEnvFloat("VIDEO_ASPECT_RATIO_TOLERANCE", 0.1),
			SegmentDurationSec:   getEnvInt("VIDEO_SEGMENT_DURATION_SEC", 4),
			Bitrates:             getEnvSlice("VIDEO_BITRATES", []string{"500k", "1500k", "4000k"}),
			FFmpegPath:           validateBinaryName(getEnv("FFMPEG_PATH", "ffmpeg")),
			FFprobePath:          validateBinaryName(getEnv("FFPROBE_PATH", "ffprobe")),
			TempDir:              getEnv("VIDEO_TEMP_DIR", "/tmp/video_processing"),
		},
		Auth: AuthConfig{
			LuaScript:      getEnv("AUTH_LUA_SCRIPT", ""),
			LuaTimeoutMs:   getEnvInt("AUTH_LUA_TIMEOUT_MS", 3000),
			LuaEnvWhitelist: getEnv("AUTH_LUA_ENV_WHITELIST", "AUTH_SERVICE_URL"),
			LuaMaxMemoryMB: getEnvInt("AUTH_LUA_MAX_MEMORY_MB", 32),
		},
		RateLimit: RateLimitConfig{
			RPS:   getEnvFloat("RATE_LIMIT_RPS", 10),
			Burst: getEnvInt("RATE_LIMIT_BURST", 20),
		},
	}
}

// validateBinaryName ensures the path is just a binary name (no slashes, no path traversal).
func validateBinaryName(path string) string {
	if strings.Contains(path, "/") || strings.Contains(path, "..") {
		fmt.Fprintf(os.Stderr, "[WARN] Invalid binary path %q — must be a bare name (e.g. 'ffmpeg'). Using default.\n", path)
		return "ffmpeg"
	}
	return path
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

func getEnvFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}
