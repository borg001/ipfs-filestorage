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
	Server      ServerConfig
	IPFS        IPFSConfig
	API         APIConfig
	Upload      UploadConfig
	Image       ImageConfig
	Pinning     PinningConfig
	Unpin       UnpinConfig
	CORS        CORSConfig
	Video       VideoConfig
	Auth        AuthConfig
	MediaAccess MediaAccessConfig
	RateLimit   RateLimitConfig
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

type ImageVariant struct {
	Key    string `json:"key"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

const (
	// PrivacyBlurVariantKey is the full-image blur variant. It is safe to use
	// as a restricted-media preview only when the original URL is not exposed.
	PrivacyBlurVariantKey = "blur"
	// PrivacyFaceBlurVariantKey masks detected faces while preserving the rest
	// of the image for contexts where the full image remains visible.
	PrivacyFaceBlurVariantKey = "blur_faces"
)

type ImagePrivacyConfig struct {
	// BlurRadius is the radius used for the whole-image restricted preview.
	BlurRadius int
	// FaceBlurRadius is the minimum radius used for every detected face.
	FaceBlurRadius int
	// FaceDetectionMaxDimension bounds detector work by downscaling the input.
	FaceDetectionMaxDimension int
	// FaceDetectionScoreThreshold rejects low-confidence YuNet detections.
	FaceDetectionScoreThreshold float64
	// FaceDetectionNMSThreshold controls YuNet non-maximum suppression.
	FaceDetectionNMSThreshold float64
}

func DefaultImagePrivacyConfig() ImagePrivacyConfig {
	return ImagePrivacyConfig{
		BlurRadius:                  24,
		FaceBlurRadius:              16,
		FaceDetectionMaxDimension:   1280,
		FaceDetectionScoreThreshold: 0.8,
		FaceDetectionNMSThreshold:   0.3,
	}
}

// NormalizeImagePrivacyConfig gives programmatic configs the same safe defaults
// as environment-based configuration.
func NormalizeImagePrivacyConfig(value ImagePrivacyConfig) ImagePrivacyConfig {
	defaults := DefaultImagePrivacyConfig()
	if value.BlurRadius <= 0 {
		value.BlurRadius = defaults.BlurRadius
	}
	if value.FaceBlurRadius <= 0 {
		value.FaceBlurRadius = defaults.FaceBlurRadius
	}
	if value.FaceDetectionMaxDimension <= 0 {
		value.FaceDetectionMaxDimension = defaults.FaceDetectionMaxDimension
	}
	if value.FaceDetectionScoreThreshold <= 0 {
		value.FaceDetectionScoreThreshold = defaults.FaceDetectionScoreThreshold
	}
	if value.FaceDetectionNMSThreshold <= 0 {
		value.FaceDetectionNMSThreshold = defaults.FaceDetectionNMSThreshold
	}
	value.FaceDetectionScoreThreshold = clampFloat(value.FaceDetectionScoreThreshold, 0.1, 0.99)
	value.FaceDetectionNMSThreshold = clampFloat(value.FaceDetectionNMSThreshold, 0.1, 0.99)
	return value
}

type ImageConfig struct {
	ProcessingEnabled bool
	Variants          []ImageVariant
	OutputFormat      string
	JPEGProgressive   bool
	JPEGQuality       int
	WebPQuality       int
	ResizePolicy      string
	Privacy           ImagePrivacyConfig
}

type PinningConfig struct {
	Retries      int
	RetryDelayMs int
}

type UnpinConfig struct {
	// TTL после которого GC физически анпиннит файл из unpin-списка
	TTL time.Duration
	// Интервал запуска GC-воркера
	GCInterval time.Duration
	// Путь к JSON-файлу для персистентности unpin-списка
	StorePath string
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
	// Размеры JPEG-превью, которые генерируются для видео.
	ThumbnailVariants []ImageVariant
	// Время кадра для превью (сек)
	ThumbnailTimeSec float64
	// JPEG qscale для ffmpeg (1 лучше, 31 хуже)
	ThumbnailQScale int
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

// MediaAccessConfig points to authenticated generator resources that decide
// which rendition of media a caller may receive.
type MediaAccessConfig struct {
	// URL resolves legacy CID requests through the bounded list policy resource.
	URL string
	// LinkURL resolves opaque /file/link/{id} requests. An empty value falls
	// back to URL for single-endpoint deployments.
	LinkURL   string
	TimeoutMs int
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
			Keys: getEnvSlice("API_KEYS", []string{}),
		},
		Upload: UploadConfig{
			MaxFileSize:       getEnvInt64("UPLOAD_MAX_FILE_SIZE", 10*1024*1024),
			AllowedExtensions: getEnvSlice("UPLOAD_ALLOWED_EXTENSIONS", []string{"png", "svg", "jpg", "jpeg", "webp", "pdf", "doc", "docx", "zip", "json", "html", "txt", "mp4", "mov", "webm", "avi", "mkv"}),
			AllowedMimeTypes: map[string]bool{
				"image/png":          true,
				"image/svg+xml":      true,
				"image/jpeg":         true,
				"image/webp":         true,
				"application/pdf":    true,
				"application/msword": true,
				"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
				"application/zip":           true,
				"application/json":          true,
				"application/octet-stream":  true,
				"text/html":                 true,
				"text/plain":                true,
				"text/plain; charset=utf-8": true,
				"video/mp4":                 true,
				"video/quicktime":           true,
				"video/webm":                true,
				"video/x-msvideo":           true,
				"video/x-matroska":          true,
			},
		},
		Image: ImageConfig{
			ProcessingEnabled: getEnvBool("IMAGE_PROCESSING_ENABLED", true),
			Variants: getEnvImageVariants("IMAGE_VARIANTS", []ImageVariant{
				{Key: "100x100", Width: 100, Height: 100},
				{Key: "320x320", Width: 320, Height: 320},
				{Key: "480x640", Width: 480, Height: 640},
				{Key: "640x640", Width: 640, Height: 640},
				{Key: "768x1024", Width: 768, Height: 1024},
				{Key: "1024x1024", Width: 1024, Height: 1024},
			}),
			OutputFormat:    validateChoice(getEnv("IMAGE_OUTPUT_FORMAT", "auto"), []string{"auto", "jpeg", "webp"}, "auto"),
			JPEGProgressive: getEnvBool("IMAGE_JPEG_PROGRESSIVE", true),
			JPEGQuality:     clampInt(getEnvInt("IMAGE_JPEG_QUALITY", 82), 1, 100),
			WebPQuality:     clampInt(getEnvInt("IMAGE_WEBP_QUALITY", 82), 1, 100),
			ResizePolicy:    validateChoice(getEnv("IMAGE_RESIZE_POLICY", "smart-cover"), []string{"fit", "cover-center", "smart-cover"}, "smart-cover"),
			Privacy: ImagePrivacyConfig{
				BlurRadius:                  clampInt(getEnvInt("IMAGE_BLUR_RADIUS", 24), 1, 64),
				FaceBlurRadius:              clampInt(getEnvInt("IMAGE_FACE_BLUR_RADIUS", 16), 1, 64),
				FaceDetectionMaxDimension:   clampInt(getEnvInt("IMAGE_FACE_DETECTION_MAX_DIMENSION", 1280), 128, 4096),
				FaceDetectionScoreThreshold: clampFloat(getEnvFloat("IMAGE_FACE_DETECTION_SCORE_THRESHOLD", 0.8), 0.1, 0.99),
				FaceDetectionNMSThreshold:   clampFloat(getEnvFloat("IMAGE_FACE_DETECTION_NMS_THRESHOLD", 0.3), 0.1, 0.99),
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
			MaxDurationSec:       getEnvInt("VIDEO_MAX_DURATION_SEC", 2400),
			MaxSizeBytes:         getEnvInt64("VIDEO_MAX_SIZE_MB", 1024) * 1024 * 1024,
			AspectRatioTolerance: getEnvFloat("VIDEO_ASPECT_RATIO_TOLERANCE", 0.1),
			SegmentDurationSec:   getEnvInt("VIDEO_SEGMENT_DURATION_SEC", 4),
			Bitrates:             getEnvSlice("VIDEO_BITRATES", []string{"500k", "1500k", "4000k"}),
			FFmpegPath:           validateBinaryPath(getEnv("FFMPEG_PATH", "ffmpeg"), "ffmpeg"),
			FFprobePath:          validateBinaryPath(getEnv("FFPROBE_PATH", "ffprobe"), "ffprobe"),
			TempDir:              getEnv("VIDEO_TEMP_DIR", "/tmp/video_processing"),
			ThumbnailVariants: getEnvImageVariants("VIDEO_THUMBNAIL_VARIANTS", []ImageVariant{
				{Key: "180x320", Width: 180, Height: 320},
				{Key: "360x640", Width: 360, Height: 640},
				{Key: "720x1280", Width: 720, Height: 1280},
			}),
			ThumbnailTimeSec: getEnvFloat("VIDEO_THUMBNAIL_TIME_SEC", 1.0),
			ThumbnailQScale:  clampInt(getEnvInt("VIDEO_THUMBNAIL_QSCALE", 3), 1, 31),
		},
		Auth: AuthConfig{
			LuaScript:       getEnv("AUTH_LUA_SCRIPT", ""),
			LuaTimeoutMs:    getEnvInt("AUTH_LUA_TIMEOUT_MS", 3000),
			LuaEnvWhitelist: getEnv("AUTH_LUA_ENV_WHITELIST", "AUTH_SERVICE_URL"),
			LuaMaxMemoryMB:  getEnvInt("AUTH_LUA_MAX_MEMORY_MB", 32),
		},
		MediaAccess: MediaAccessConfig{
			URL:       getEnv("MEDIA_ACCESS_URL", ""),
			LinkURL:   getEnv("MEDIA_LINK_ACCESS_URL", ""),
			TimeoutMs: clampInt(getEnvInt("MEDIA_ACCESS_TIMEOUT_MS", 2500), 100, 30000),
		},
		RateLimit: RateLimitConfig{
			RPS:   getEnvFloat("RATE_LIMIT_RPS", 10),
			Burst: getEnvInt("RATE_LIMIT_BURST", 20),
		},
	}
}

// validateBinaryPath accepts a binary name or an absolute path, but rejects traversal.
func validateBinaryPath(path string, def string) string {
	if path == "" || strings.Contains(path, "..") {
		fmt.Fprintf(os.Stderr, "[WARN] Invalid binary path %q. Using default %q.\n", path, def)
		return def
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

func getEnvImageVariants(key string, def []ImageVariant) []ImageVariant {
	parts := getEnvSlice(key, nil)
	if len(parts) == 0 {
		return def
	}
	var variants []ImageVariant
	for _, part := range parts {
		wh := strings.Split(strings.ToLower(strings.TrimSpace(part)), "x")
		if len(wh) != 2 {
			return def
		}
		w, errW := strconv.Atoi(wh[0])
		h, errH := strconv.Atoi(wh[1])
		if errW != nil || errH != nil || w <= 0 || h <= 0 {
			return def
		}
		variants = append(variants, ImageVariant{Key: fmt.Sprintf("%dx%d", w, h), Width: w, Height: h})
	}
	return variants
}

func validateChoice(value string, allowed []string, def string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, item := range allowed {
		if value == item {
			return value
		}
	}
	return def
}

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func clampFloat(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func getEnvBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
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
