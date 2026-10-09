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
		MaxSizeBytes:   10 * 1024 * 1024,
		MaxDurationSec: 60,
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
		MaxSizeBytes:   10 * 1024 * 1024,
		MaxDurationSec: 60,
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
		MaxSizeBytes:   100 * 1024 * 1024,
		MaxDurationSec: 30,
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
		MaxSizeBytes:   100 * 1024 * 1024,
		MaxDurationSec: 30,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{
		info: &VideoInfo{Duration: 30, Width: 1080, Height: 1920},
	}}

	err := v.Validate(context.Background(), "/some/file.mp4", 1024)
	if err != nil {
		t.Errorf("Expected no error at exact duration limit, got: %v", err)
	}
}

// A publication carries the frame it is shown in, so a video is accepted as it
// was shot - vertical, square, landscape or rotated by a phone - and cropped to
// that frame where it is presented.
func TestValidateAcceptsEveryShape(t *testing.T) {
	cfg := &config.VideoConfig{MaxSizeBytes: 100 * 1024 * 1024, MaxDurationSec: 60}
	for _, testCase := range []struct {
		name string
		info *VideoInfo
	}{
		{name: "vertical", info: &VideoInfo{Duration: 10, Width: 1080, Height: 1920}},
		{name: "square", info: &VideoInfo{Duration: 10, Width: 1080, Height: 1080}},
		{name: "landscape", info: &VideoInfo{Duration: 10, Width: 1920, Height: 1080}},
		{name: "wide", info: &VideoInfo{Duration: 10, Width: 1080, Height: 566}},
		{name: "rotated by a phone", info: &VideoInfo{Duration: 10, Width: 1920, Height: 1080, Rotation: 90}},
		{name: "dimensions unknown", info: &VideoInfo{Duration: 10}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			v := &Validator{cfg: cfg, prober: &mockProber{info: testCase.info}}
			if err := v.Validate(context.Background(), "/some/file.mp4", 1024); err != nil {
				t.Fatalf("Expected %s video to be accepted, got: %v", testCase.name, err)
			}
		})
	}
}

func TestValidateProbeError(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:   100 * 1024 * 1024,
		MaxDurationSec: 60,
		FFprobePath:    "nonexistent_ffprobe",
	}
	v := NewValidator(cfg)

	err := v.Validate(context.Background(), "/nonexistent/file.mp4", 1024)
	if err == nil {
		t.Error("Expected error when ffprobe fails")
	}
}

func TestWithProber(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxSizeBytes:   100 * 1024 * 1024,
		MaxDurationSec: 60,
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
		MaxSizeBytes:   100 * 1024 * 1024,
		MaxDurationSec: 60,
	}
	v := &Validator{cfg: cfg, prober: &mockProber{
		err: context.DeadlineExceeded,
	}}

	err := v.Validate(context.Background(), "/some/file.mp4", 1024)
	if err == nil {
		t.Error("Expected error when prober returns error")
	}
}
