package video

import (
	"context"
	"errors"
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
		MaxSizeBytes:         10 * 1024 * 1024,
		MaxDurationSec:       60,
		AspectRatioTolerance: 0.1,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{}}

	err := v.Validate(context.Background(), "/some/file.mp4", 20*1024*1024)
	var validationError *ValidationError
	if !errors.As(err, &validationError) || validationError.Code != "video_file_too_large" || validationError.MaxSizeBytes != 10*1024*1024 {
		t.Fatalf("Expected typed file-size validation error, got %v", err)
	}
}

func TestValidateFileSizeAtLimit(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:         10 * 1024 * 1024,
		MaxDurationSec:       60,
		AspectRatioTolerance: 0.1,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{
		info: &VideoInfo{Duration: 5, Width: 1080, Height: 1920},
	}}

	err := v.Validate(context.Background(), "/some/file.mp4", 10*1024*1024)
	if err != nil {
		t.Errorf("Expected no error at exact size limit, got: %v", err)
	}
}

func TestValidateDurationTooLong(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:         100 * 1024 * 1024,
		MaxDurationSec:       30,
		AspectRatioTolerance: 0.1,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{
		info: &VideoInfo{Duration: 60, Width: 1080, Height: 1920},
	}}

	err := v.Validate(context.Background(), "/some/file.mp4", 1024)
	var validationError *ValidationError
	if !errors.As(err, &validationError) || validationError.Code != "video_duration_exceeded" || validationError.MaxDurationSec != 30 {
		t.Fatalf("Expected typed duration validation error, got %v", err)
	}
}

func TestValidateDurationAtLimit(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:         100 * 1024 * 1024,
		MaxDurationSec:       30,
		AspectRatioTolerance: 0.1,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{
		info: &VideoInfo{Duration: 30, Width: 1080, Height: 1920},
	}}

	err := v.Validate(context.Background(), "/some/file.mp4", 1024)
	if err != nil {
		t.Errorf("Expected no error at exact duration limit, got: %v", err)
	}
}

func TestValidateWrongAspectRatio(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:         100 * 1024 * 1024,
		MaxDurationSec:       60,
		AspectRatioTolerance: 0.1,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{
		info: &VideoInfo{Duration: 10, Width: 1920, Height: 1080},
	}}

	err := v.Validate(context.Background(), "/some/file.mp4", 1024)
	var validationError *ValidationError
	if !errors.As(err, &validationError) || validationError.Code != "video_aspect_ratio_invalid" || validationError.ExpectedAspectRatio != "9:16" {
		t.Fatalf("Expected typed aspect ratio validation error, got %v", err)
	}
}

func TestValidateCorrectVertical9x16(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:         100 * 1024 * 1024,
		MaxDurationSec:       60,
		AspectRatioTolerance: 0.1,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{
		info: &VideoInfo{Duration: 10, Width: 1080, Height: 1920},
	}}

	err := v.Validate(context.Background(), "/some/file.mp4", 1024)
	if err != nil {
		t.Errorf("Expected no error for valid video, got: %v", err)
	}
}

func TestValidateAcceptsPhoneVideoWithVerticalDisplayRotation(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:         100 * 1024 * 1024,
		MaxDurationSec:       60,
		AspectRatioTolerance: 0.1,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{
		// A phone can store landscape pixels and use a 90 degree display
		// matrix. The rendered result is 1080x1920, not 1920x1080.
		info: &VideoInfo{Duration: 10, Width: 1920, Height: 1080, Rotation: 90},
	}}

	if err := v.Validate(context.Background(), "/some/phone-video.mp4", 1024); err != nil {
		t.Fatalf("Expected rotated vertical phone video to be accepted, got: %v", err)
	}
}

func TestValidateRejectsRotatedLandscapeVideo(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:         100 * 1024 * 1024,
		MaxDurationSec:       60,
		AspectRatioTolerance: 0.1,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{
		info: &VideoInfo{Duration: 10, Width: 1080, Height: 1920, Rotation: 90},
	}}

	err := v.Validate(context.Background(), "/some/rotated-landscape-video.mp4", 1024)
	var validationError *ValidationError
	if !errors.As(err, &validationError) || validationError.Code != "video_aspect_ratio_invalid" {
		t.Fatalf("Expected rotated landscape video to be rejected, got %v", err)
	}
}

func TestValidateZeroDimensions(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:         100 * 1024 * 1024,
		MaxDurationSec:       60,
		AspectRatioTolerance: 0.1,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{
		info: &VideoInfo{Duration: 10, Width: 0, Height: 0},
	}}

	err := v.Validate(context.Background(), "/some/file.mp4", 1024)
	if err != nil {
		t.Errorf("Zero dimensions should skip aspect ratio check, got: %v", err)
	}
}

func TestValidateZeroWidthOnly(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:         100 * 1024 * 1024,
		MaxDurationSec:       60,
		AspectRatioTolerance: 0.1,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{
		info: &VideoInfo{Duration: 10, Width: 0, Height: 1920},
	}}

	err := v.Validate(context.Background(), "/some/file.mp4", 1024)
	if err != nil {
		t.Errorf("Zero width should skip aspect ratio check, got: %v", err)
	}
}

func TestValidateNear9x16(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:         100 * 1024 * 1024,
		MaxDurationSec:       60,
		AspectRatioTolerance: 0.05,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{
		info: &VideoInfo{Duration: 10, Width: 1080, Height: 1920},
	}}

	err := v.Validate(context.Background(), "/some/file.mp4", 1024)
	if err != nil {
		t.Errorf("Expected no error for exact 9:16, got: %v", err)
	}
}

func TestValidateSlightlyOutsideTolerance(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:         100 * 1024 * 1024,
		MaxDurationSec:       60,
		AspectRatioTolerance: 0.01,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{
		info: &VideoInfo{Duration: 10, Width: 720, Height: 1200},
	}}

	err := v.Validate(context.Background(), "/some/file.mp4", 1024)
	if err == nil {
		t.Error("Expected error for aspect ratio outside tight tolerance")
	}
}

func TestValidateProbeError(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:         100 * 1024 * 1024,
		MaxDurationSec:       60,
		AspectRatioTolerance: 0.1,
		FFprobePath:          "nonexistent_ffprobe",
	}
	v := NewValidator(cfg)

	err := v.Validate(context.Background(), "/nonexistent/file.mp4", 1024)
	if err == nil {
		t.Error("Expected error when ffprobe fails")
	}
}

func TestWithProber(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:         100 * 1024 * 1024,
		MaxDurationSec:       60,
		AspectRatioTolerance: 0.1,
	}
	v := NewValidator(cfg)
	mock := &mockProber{
		info: &VideoInfo{Duration: 5, Width: 1080, Height: 1920},
	}
	v.WithProber(mock)

	err := v.Validate(context.Background(), "/fake/path.mp4", 1024)
	if err != nil {
		t.Errorf("Expected no error with mock prober, got: %v", err)
	}
}

func TestParseFrameRate(t *testing.T) {
	if got := parseFrameRate("30000/1001"); got < 29.9 || got > 30.0 {
		t.Fatalf("parseFrameRate(30000/1001) = %f, want about 29.97", got)
	}
	if got := parseFrameRate("invalid"); got != 0 {
		t.Fatalf("parseFrameRate(invalid) = %f, want 0", got)
	}
}

func TestValidateProbeReturnsError(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:         100 * 1024 * 1024,
		MaxDurationSec:       60,
		AspectRatioTolerance: 0.1,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{
		err: context.DeadlineExceeded,
	}}

	err := v.Validate(context.Background(), "/some/file.mp4", 1024)
	if err == nil {
		t.Error("Expected error when prober returns error")
	}
}
