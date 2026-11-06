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
	Code           string
	MaxSizeBytes   int64
	MaxDurationSec int
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
//
// The shape of the frame is not one of them. A publication carries the frame
// it is shown in, chosen when it is composed, so a video is accepted as it was
// shot and cropped to that frame where it is presented.
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
	// A frame larger than 4K or faster than 120 fps costs a transcode far
	// beyond what a phone records.
	if info.Width > maxVideoSide || info.Height > maxVideoSide || info.FrameRate > maxVideoFrameRate {
		return &ValidationError{Code: "video_resolution_exceeded"}
	}

	return nil
}

const (
	maxVideoSide      = 4096
	maxVideoFrameRate = 120
)

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
	if err != nil || parsedDuration <= 0 {
		// A video recorded in a browser (MediaRecorder WebM) carries no
		// duration in its header: it is read from the last frame instead.
		parsedDuration, err = probePacketDuration(ctx, ffprobePath, inputPath)
		if err != nil {
			return nil, fmt.Errorf("parse duration: %w", err)
		}
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

// probePacketDuration measures a video that does not state its duration: the
// end of its last video frame, read from the packets.
func probePacketDuration(ctx context.Context, ffprobePath, inputPath string) (float64, error) {
	out, err := exec.CommandContext(ctx, ffprobePath,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "packet=pts_time,duration_time",
		"-of", "csv=p=0",
		inputPath,
	).Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe packets: %w", err)
	}
	return lastFrameEnd(string(out))
}

// lastFrameEnd is the latest frame start plus its length, from ffprobe's
// "pts_time,duration_time" lines; a frame without either is skipped.
func lastFrameEnd(lines string) (float64, error) {
	end := 0.0
	for _, line := range strings.Split(lines, "\n") {
		fields := strings.Split(strings.TrimSpace(line), ",")
		pts, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		length := 0.0
		if len(fields) > 1 {
			length, _ = strconv.ParseFloat(fields[1], 64)
		}
		if pts+length > end {
			end = pts + length
		}
	}
	if end <= 0 {
		return 0, fmt.Errorf("no timed video frames")
	}
	return end, nil
}
