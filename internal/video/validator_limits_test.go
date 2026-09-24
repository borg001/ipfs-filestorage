package video

import (
	"context"
	"errors"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

// A frame larger than 4K or faster than 120 fps is refused before transcoding.
func TestValidateRefusesOversizedFrames(t *testing.T) {
	cfg := &config.VideoConfig{MaxSizeBytes: 100 * 1024 * 1024, MaxDurationSec: 60}
	for _, info := range []*VideoInfo{
		{Duration: 10, Width: 8192, Height: 4320},
		{Duration: 10, Width: 1920, Height: 1080, FrameRate: 240},
	} {
		v := &Validator{cfg: cfg, prober: &mockProber{info: info}}
		var validationError *ValidationError
		if err := v.Validate(context.Background(), "/some/file.mp4", 1024); !errors.As(err, &validationError) || validationError.Code != "video_resolution_exceeded" {
			t.Fatalf("%+v: err = %v, want video_resolution_exceeded", info, err)
		}
	}
}
