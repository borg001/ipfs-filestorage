package video

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

// VideoInfo — метаданные видео, полученные через ffprobe.
type VideoInfo struct {
	Duration  float64 `json:"duration"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	CodecName string `json:"codec_name"`
}

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

// Validate проверяет видео на соответствие требованиям:
// - размер файла ≤ MaxSizeBytes
// - длительность ≤ MaxDurationSec
// - вертикальная ориентация (9:16 с допуском AspectRatioTolerance)
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

// Probe получает метаданные видео через ffprobe.
func (v *Validator) Probe(ctx context.Context, inputPath string) (*VideoInfo, error) {
	duration, err := v.probeField(ctx, inputPath, "duration")
	if err != nil {
		return nil, fmt.Errorf("probe duration: %w", err)
	}

	widthStr, err := v.probeField(ctx, inputPath, "width")
	if err != nil {
		return nil, fmt.Errorf("probe width: %w", err)
	}

	heightStr, err := v.probeField(ctx, inputPath, "height")
	if err != nil {
		return nil, fmt.Errorf("probe height: %w", err)
	}

	codec, err := v.probeCodec(ctx, inputPath)
	if err != nil {
		return nil, fmt.Errorf("probe codec: %w", err)
	}

	dur, _ := strconv.ParseFloat(duration, 64)
	w, _ := strconv.Atoi(widthStr)
	h, _ := strconv.Atoi(heightStr)

	return &VideoInfo{
		Duration:  dur,
		Width:     w,
		Height:    h,
		CodecName: codec,
	}, nil
}

// probeField получает одно поле через ffprobe.
func (v *Validator) probeField(ctx context.Context, inputPath, field string) (string, error) {
	cmd := exec.CommandContext(ctx, v.cfg.FFprobePath,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", fmt.Sprintf("stream=%s", field),
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputPath,
	)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ffprobe %s: %w", field, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// probeCodec получает имя видеокодека.
func (v *Validator) probeCodec(ctx context.Context, inputPath string) (string, error) {
	cmd := exec.CommandContext(ctx, v.cfg.FFprobePath,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name",
		"-of", "json",
		inputPath,
	)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ffprobe codec: %w", err)
	}

	var result struct {
		Streams []struct {
			CodecName string `json:"codec_name"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return "", fmt.Errorf("parse codec: %w", err)
	}
	if len(result.Streams) == 0 {
		return "", fmt.Errorf("no video streams found")
	}
	return result.Streams[0].CodecName, nil
}
