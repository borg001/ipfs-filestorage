package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	envKeys := []string{
		"SERVER_PORT", "IPFS_URL", "CLUSTER_NODES",
		"API_KEYS", "UPLOAD_MAX_FILE_SIZE", "UPLOAD_ALLOWED_EXTENSIONS",
		"PINNING_RETRIES", "PINNING_RETRY_DELAY_MS",
		"UNPIN_TTL", "UNPIN_GC_INTERVAL", "UNPIN_STORE_PATH",
		"CORS_ALLOWED_ORIGINS", "CORS_ALLOWED_HEADERS",
		"VIDEO_MAX_DURATION_SEC", "VIDEO_MAX_SIZE_MB",
		"VIDEO_ASPECT_RATIO_TOLERANCE", "VIDEO_SEGMENT_DURATION_SEC",
		"VIDEO_BITRATES", "FFMPEG_PATH", "FFPROBE_PATH", "VIDEO_TEMP_DIR",
		"IMAGE_BLUR_RADIUS", "IMAGE_FACE_BLUR_RADIUS", "IMAGE_FACE_DETECTION_MAX_DIMENSION",
		"IMAGE_FACE_DETECTION_SCORE_THRESHOLD", "IMAGE_FACE_DETECTION_NMS_THRESHOLD",
		"MEDIA_ACCESS_URL", "MEDIA_ACCESS_TIMEOUT_MS",
	}
	for _, k := range envKeys {
		os.Unsetenv(k)
	}

	cfg := Load()

	if cfg.Server.Port != "3000" {
		t.Errorf("Default Server.Port = %q, want 3000", cfg.Server.Port)
	}
	if cfg.IPFS.LocalURL != "http://localhost:5001" {
		t.Errorf("Default IPFS.LocalURL = %q, want http://localhost:5001", cfg.IPFS.LocalURL)
	}
	if cfg.Pinning.Retries != 3 {
		t.Errorf("Default Pinning.Retries = %d, want 3", cfg.Pinning.Retries)
	}
	if cfg.Unpin.TTL != 24*time.Hour {
		t.Errorf("Default Unpin.TTL = %v, want 24h", cfg.Unpin.TTL)
	}
	if cfg.Video.MaxDurationSec != 2400 {
		t.Errorf("Default Video.MaxDurationSec = %d, want 2400", cfg.Video.MaxDurationSec)
	}
	if cfg.Video.MaxSizeBytes != 1024*1024*1024 {
		t.Errorf("Default Video.MaxSizeBytes = %d, want 1024MB", cfg.Video.MaxSizeBytes)
	}
	if cfg.Video.AspectRatioTolerance != 0.1 {
		t.Errorf("Default Video.AspectRatioTolerance = %f, want 0.1", cfg.Video.AspectRatioTolerance)
	}
	if cfg.Video.SegmentDurationSec != 4 {
		t.Errorf("Default Video.SegmentDurationSec = %d, want 4", cfg.Video.SegmentDurationSec)
	}
	if len(cfg.Video.Bitrates) != 3 {
		t.Errorf("Default Video.Bitrates len = %d, want 3", len(cfg.Video.Bitrates))
	}
	if cfg.Video.FFmpegPath != "ffmpeg" {
		t.Errorf("Default Video.FFmpegPath = %q, want ffmpeg", cfg.Video.FFmpegPath)
	}
	if cfg.Video.FFprobePath != "ffprobe" {
		t.Errorf("Default Video.FFprobePath = %q, want ffprobe", cfg.Video.FFprobePath)
	}
	if cfg.Image.Privacy != DefaultImagePrivacyConfig() {
		t.Errorf("Default Image.Privacy = %+v, want %+v", cfg.Image.Privacy, DefaultImagePrivacyConfig())
	}
	if cfg.MediaAccess.URL != "" || cfg.MediaAccess.TimeoutMs != 2500 {
		t.Errorf("Default MediaAccess = %+v, want disabled with 2500ms timeout", cfg.MediaAccess)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	t.Setenv("SERVER_PORT", "8080")
	t.Setenv("VIDEO_MAX_DURATION_SEC", "120")
	t.Setenv("VIDEO_MAX_SIZE_MB", "50")
	t.Setenv("VIDEO_SEGMENT_DURATION_SEC", "6")
	t.Setenv("VIDEO_BITRATES", "800k,2000k")
	t.Setenv("FFMPEG_PATH", "/usr/bin/ffmpeg")
	t.Setenv("FFPROBE_PATH", "/usr/bin/ffprobe")
	t.Setenv("IMAGE_BLUR_RADIUS", "31")
	t.Setenv("IMAGE_FACE_BLUR_RADIUS", "19")
	t.Setenv("IMAGE_FACE_DETECTION_MAX_DIMENSION", "960")
	t.Setenv("IMAGE_FACE_DETECTION_SCORE_THRESHOLD", "0.87")
	t.Setenv("IMAGE_FACE_DETECTION_NMS_THRESHOLD", "0.42")
	t.Setenv("MEDIA_ACCESS_URL", "http://api.internal/api/media_delivery")
	t.Setenv("MEDIA_ACCESS_TIMEOUT_MS", "1800")

	cfg := Load()

	if cfg.Server.Port != "8080" {
		t.Errorf("Server.Port = %q, want 8080", cfg.Server.Port)
	}
	if cfg.Video.MaxDurationSec != 120 {
		t.Errorf("Video.MaxDurationSec = %d, want 120", cfg.Video.MaxDurationSec)
	}
	if cfg.Video.MaxSizeBytes != 50*1024*1024 {
		t.Errorf("Video.MaxSizeBytes = %d, want 50MB", cfg.Video.MaxSizeBytes)
	}
	if cfg.Video.SegmentDurationSec != 6 {
		t.Errorf("Video.SegmentDurationSec = %d, want 6", cfg.Video.SegmentDurationSec)
	}
	if len(cfg.Video.Bitrates) != 2 {
		t.Errorf("Video.Bitrates len = %d, want 2", len(cfg.Video.Bitrates))
	}
	if cfg.Video.Bitrates[0] != "800k" {
		t.Errorf("Video.Bitrates[0] = %q, want 800k", cfg.Video.Bitrates[0])
	}
	if cfg.Video.FFmpegPath != "/usr/bin/ffmpeg" {
		t.Errorf("Video.FFmpegPath = %q, want /usr/bin/ffmpeg", cfg.Video.FFmpegPath)
	}
	if cfg.Video.FFprobePath != "/usr/bin/ffprobe" {
		t.Errorf("Video.FFprobePath = %q, want /usr/bin/ffprobe", cfg.Video.FFprobePath)
	}
	if got := cfg.Image.Privacy; got.BlurRadius != 31 || got.FaceBlurRadius != 19 || got.FaceDetectionMaxDimension != 960 || got.FaceDetectionScoreThreshold != 0.87 || got.FaceDetectionNMSThreshold != 0.42 {
		t.Errorf("Image.Privacy override = %+v", got)
	}
	if cfg.MediaAccess.URL != "http://api.internal/api/media_delivery" || cfg.MediaAccess.TimeoutMs != 1800 {
		t.Errorf("MediaAccess override = %+v", cfg.MediaAccess)
	}
}

func TestNormalizeImagePrivacyConfig(t *testing.T) {
	got := NormalizeImagePrivacyConfig(ImagePrivacyConfig{FaceDetectionScoreThreshold: 0.7})
	want := DefaultImagePrivacyConfig()
	want.FaceDetectionScoreThreshold = 0.7
	if got != want {
		t.Errorf("NormalizeImagePrivacyConfig = %+v, want %+v", got, want)
	}
}

func TestGetEnvIntInvalid(t *testing.T) {
	t.Setenv("PINNING_RETRIES", "notanumber")
	cfg := Load()

	if cfg.Pinning.Retries != 3 {
		t.Errorf("Pinning.Retries with invalid env = %d, want default 3", cfg.Pinning.Retries)
	}
}

func TestGetEnvInt64Invalid(t *testing.T) {
	t.Setenv("UPLOAD_MAX_FILE_SIZE", "notanumber")
	cfg := Load()

	if cfg.Upload.MaxFileSize != 10*1024*1024 {
		t.Errorf("Upload.MaxFileSize with invalid env = %d, want default 10MB", cfg.Upload.MaxFileSize)
	}
}

func TestGetEnvDurationInvalid(t *testing.T) {
	t.Setenv("UNPIN_TTL", "notaduration")
	cfg := Load()

	if cfg.Unpin.TTL != 24*time.Hour {
		t.Errorf("Unpin.TTL with invalid env = %v, want default 24h", cfg.Unpin.TTL)
	}
}

func TestGetEnvFloatInvalid(t *testing.T) {
	t.Setenv("VIDEO_ASPECT_RATIO_TOLERANCE", "notafloat")
	cfg := Load()

	if cfg.Video.AspectRatioTolerance != 0.1 {
		t.Errorf("Video.AspectRatioTolerance with invalid env = %f, want default 0.1", cfg.Video.AspectRatioTolerance)
	}
}

func TestGetEnvSliceEmpty(t *testing.T) {
	t.Setenv("API_KEYS", "")
	cfg := Load()

	if len(cfg.API.Keys) != 0 {
		t.Errorf("API.Keys with empty env len = %d, want 0", len(cfg.API.Keys))
	}
}

func TestGetEnvSliceWithSpaces(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", " http://a.com , http://b.com ")
	cfg := Load()

	if len(cfg.CORS.AllowedOrigins) != 2 {
		t.Errorf("CORS.AllowedOrigins len = %d, want 2", len(cfg.CORS.AllowedOrigins))
	}
	if cfg.CORS.AllowedOrigins[0] != "http://a.com" {
		t.Errorf("CORS.AllowedOrigins[0] = %q, want http://a.com", cfg.CORS.AllowedOrigins[0])
	}
}
