package video

import (
	"context"
	"fmt"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

// VideoProber — интерфейс для получения метаданных видео.
// В продакшене реализуется *Validator (через ffprobe), в тестах — mock.
type VideoProber interface {
	Probe(ctx context.Context, inputPath string) (*VideoInfo, error)
}

// Validator валидирует видео перед транскодированием.
type Validator struct {
	cfg    *config.VideoConfig
	prober VideoProber
}

// NewValidator создаёт новый Validator.
func NewValidator(cfg *config.VideoConfig) *Validator {
	v := &Validator{cfg: cfg}
	v.prober = v // self — по умолчанию используем реальный ffprobe
	return v
}

// WithProber подставляет мок для тестов.
func (v *Validator) WithProber(p VideoProber) *Validator {
	v.prober = p
	return v
}

// Validate проверяет видео на соответствие требованиям.
func (v *Validator) Validate(ctx context.Context, inputPath string, fileSize int64) error {
	if fileSize > v.cfg.MaxSizeBytes {
		maxMB := v.cfg.MaxSizeBytes / (1024 * 1024)
		return fmt.Errorf("file size %d bytes exceeds max %dMB", fileSize, maxMB)
	}

	info, err := v.prober.Probe(ctx, inputPath)
	if err != nil {
		return fmt.Errorf("probe video: %w", err)
	}

	if info.Duration > float64(v.cfg.MaxDurationSec) {
		return fmt.Errorf("video duration %.1fs exceeds max %ds", info.Duration, v.cfg.MaxDurationSec)
	}

	if info.Width > 0 && info.Height > 0 {
		ratio := float64(info.Width) / float64(info.Height)
		target := 9.0 / 16.0
		diff := ratio - target
		if diff < 0 {
			diff = -diff
		}
		if diff > v.cfg.AspectRatioTolerance {
			return fmt.Errorf("aspect ratio %.3f not vertical 9:16 (tolerance %.2f)", ratio, v.cfg.AspectRatioTolerance)
		}
	}

	return nil
}
