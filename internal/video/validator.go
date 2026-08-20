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
	Rotation  int     `json:"rotation"`
	CodecName string  `json:"codec_name"`
	FrameRate float64 `json:"frame_rate"`
	BitRate   int64   `json:"bit_rate"`
}

// ValidationError describes a rejected input without exposing an ffprobe or
// filesystem error to an HTTP client. The handler turns Code and limits into a
// localized public response.
type ValidationError struct {
	Code                string
	MaxSizeBytes        int64
	MaxDurationSec      int
	ExpectedAspectRatio string
}

func (e *ValidationError) Error() string {
	if e == nil || e.Code == "" {
		return "video validation failed"
	}
	return e.Code
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
		return &ValidationError{Code: "video_file_too_large", MaxSizeBytes: v.cfg.MaxSizeBytes}
	}

	info, err := v.prober.Probe(ctx, inputPath)
	if err != nil {
		return &ValidationError{Code: "video_metadata_invalid"}
	}

	if info.Duration > float64(v.cfg.MaxDurationSec) {
		return &ValidationError{Code: "video_duration_exceeded", MaxDurationSec: v.cfg.MaxDurationSec}
	}

	width, height := info.DisplayDimensions()
	if width > 0 && height > 0 {
		ratio := float64(width) / float64(height)
		target := 9.0 / 16.0
		diff := ratio - target
		if diff < 0 {
			diff = -diff
		}
		if diff > v.cfg.AspectRatioTolerance {
			return &ValidationError{Code: "video_aspect_ratio_invalid", ExpectedAspectRatio: "9:16"}
		}
	}

	return nil
}

// DisplayDimensions applies the stream rotation metadata before validating the
// visual frame. Phone cameras commonly store landscape pixels with a 90/270
// degree display matrix; those files are still vertical videos to a viewer.
func (info *VideoInfo) DisplayDimensions() (int, int) {
	if info == nil {
		return 0, 0
	}
	rotation := info.Rotation % 360
	if rotation < 0 {
		rotation += 360
	}
	if rotation == 90 || rotation == 270 {
		return info.Height, info.Width
	}
	return info.Width, info.Height
}

// Probe получает метаданные видео через ffprobe.
func (v *Validator) Probe(ctx context.Context, inputPath string) (*VideoInfo, error) {
	return probeVideoInfo(ctx, v.cfg.FFprobePath, inputPath)
}

// probeVideoInfo reads the video stream metadata used by both validation and
// transcoding. Keeping one parser prevents the output ladder from disagreeing
// with the validation that accepted the original upload.
func probeVideoInfo(ctx context.Context, ffprobePath, inputPath string) (*VideoInfo, error) {
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_streams",
		"-show_format",
		"-of", "json",
		inputPath,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	var result struct {
		Format struct {
			Duration string `json:"duration"`
			BitRate  string `json:"bit_rate"`
		} `json:"format"`
		Streams []struct {
			Duration  string `json:"duration"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			CodecName string `json:"codec_name"`
			FrameRate string `json:"avg_frame_rate"`
			Tags      struct {
				Rotate string `json:"rotate"`
			} `json:"tags"`
			SideDataList []struct {
				Rotation float64 `json:"rotation"`
			} `json:"side_data_list"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}
	if len(result.Streams) == 0 {
		return nil, fmt.Errorf("no video streams found")
	}

	stream := result.Streams[0]
	duration := result.Format.Duration
	if duration == "" {
		duration = stream.Duration
	}
	parsedDuration, err := strconv.ParseFloat(strings.TrimSpace(duration), 64)
	if err != nil {
		return nil, fmt.Errorf("parse duration: %w", err)
	}
	rotation, _ := strconv.Atoi(strings.TrimSpace(stream.Tags.Rotate))
	for _, sideData := range stream.SideDataList {
		if sideData.Rotation != 0 {
			rotation = int(sideData.Rotation)
			break
		}
	}
	frameRate := parseFrameRate(stream.FrameRate)
	bitRate, _ := strconv.ParseInt(strings.TrimSpace(result.Format.BitRate), 10, 64)
	return &VideoInfo{
		Duration:  parsedDuration,
		Width:     stream.Width,
		Height:    stream.Height,
		Rotation:  rotation,
		CodecName: stream.CodecName,
		FrameRate: frameRate,
		BitRate:   bitRate,
	}, nil
}

func parseFrameRate(value string) float64 {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if len(parts) != 2 {
		return 0
	}
	numerator, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}
	denominator, err := strconv.ParseFloat(parts[1], 64)
	if err != nil || denominator <= 0 {
		return 0
	}
	return numerator / denominator
}
