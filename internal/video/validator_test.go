package video

import (
	"context"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

type mockProber struct {
	info *VideoInfo
	err  error
}

func (m *mockProber) Probe(ctx context.Context, inputPath string) (*VideoInfo, error) {
	return m.info, m.err
}

func TestValidateFileSizeTooLarge(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:        10 * 1024 * 1024, // 10MB
		MaxDurationSec:     60,
		AspectRatioTolerance: 0.1,
	}
	v := &Validator{cfg: cfg}

	err := v.Validate(t.Context(), "/some/file.mp4", 20*1024*1024)
	if err == nil {
		t.Error("Expected error for file too large")
	}
}

func TestValidateDurationTooLong(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:        100 * 1024 * 1024,
		MaxDurationSec:     30,
		AspectRatioTolerance: 0.1,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{
		info: &VideoInfo{Duration: 60, Width: 1080, Height: 1920},
	}}

	err := v.Validate(t.Context(), "/some/file.mp4", 1024)
	if err == nil {
		t.Error("Expected error for video too long")
	}
}

func TestValidateWrongAspectRatio(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:        100 * 1024 * 1024,
		MaxDurationSec:     60,
		AspectRatioTolerance: 0.1,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{
		info: &VideoInfo{Duration: 10, Width: 1920, Height: 1080}, // 16:9 horizontal
	}}

	err := v.Validate(t.Context(), "/some/file.mp4", 1024)
	if err == nil {
		t.Error("Expected error for horizontal aspect ratio")
	}
}

func TestValidateCorrectVertical9x16(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:        100 * 1024 * 1024,
		MaxDurationSec:     60,
		AspectRatioTolerance: 0.1,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{
		info: &VideoInfo{Duration: 10, Width: 1080, Height: 1920}, // 9:16 vertical
	}}

	err := v.Validate(t.Context(), "/some/file.mp4", 1024)
	if err != nil {
		t.Errorf("Expected no error for valid video, got: %v", err)
	}
}

func TestValidateNear9x16(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:        100 * 1024 * 1024,
		MaxDurationSec:     60,
		AspectRatioTolerance: 0.05,
	}
	// 1080x1920 = 0.5625, target = 0.5625, diff = 0 → OK
	v := &Validator{cfg: cfg, prober: &mockProber{
		info: &VideoInfo{Duration: 10, Width: 1080, Height: 1920},
	}}

	err := v.Validate(t.Context(), "/some/file.mp4", 1024)
	if err != nil {
		t.Errorf("Expected no error for exact 9:16, got: %v", err)
	}
}

func TestValidateProbeError(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:        100 * 1024 * 1024,
		MaxDurationSec:     60,
		AspectRatioTolerance: 0.1,
		FFprobePath:         "nonexistent_ffprobe",
	}
	v := NewValidator(cfg)

	err := v.Validate(t.Context(), "/nonexistent/file.mp4", 1024)
	if err == nil {
		t.Error("Expected error when ffprobe fails")
	}
}
