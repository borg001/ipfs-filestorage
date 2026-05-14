package video

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

func TestBitrateToName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"500k", "low"},
		{"700k", "low"},
		{"1500k", "medium"},
		{"2000k", "medium"},
		{"4000k", "high"},
		{"8000k", "high"},
		{"invalid", "invalid"},
	}

	for _, tt := range tests {
		got := bitrateToName(tt.input)
		if got != tt.want {
			t.Errorf("bitrateToName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMultiplyBitrate(t *testing.T) {
	tests := []struct {
		input  string
		factor int
		want   string
	}{
		{"500k", 2, "1000k"},
		{"1500k", 2, "3000k"},
		{"4000k", 3, "12000k"},
		{"invalid", 2, "invalid"},
	}

	for _, tt := range tests {
		got := multiplyBitrate(tt.input, tt.factor)
		if got != tt.want {
			t.Errorf("multiplyBitrate(%q, %d) = %q, want %q", tt.input, tt.factor, got, tt.want)
		}
	}
}

func TestMultiplyBitrateUppercase(t *testing.T) {
	got := multiplyBitrate("500K", 2)
	if got != "1000k" {
		t.Errorf("multiplyBitrate(500K, 2) = %q, want 1000k", got)
	}
}

func TestNewTranscoder(t *testing.T) {
	cfg := &config.VideoConfig{
		MaxDurationSec:     60,
		SegmentDurationSec: 4,
		Bitrates:           []string{"500k", "1500k", "4000k"},
		FFmpegPath:         "ffmpeg",
		FFprobePath:        "ffprobe",
		TempDir:            t.TempDir(),
	}

	tr := NewTranscoder(cfg)
	if tr == nil {
		t.Fatal("NewTranscoder returned nil")
	}
	if tr.cfg != cfg {
		t.Error("Transcoder config not set correctly")
	}
}

func TestBuildHLSArgs(t *testing.T) {
	cfg := &config.VideoConfig{
		SegmentDurationSec: 4,
		Bitrates:           []string{"500k", "1500k", "4000k"},
		FFmpegPath:         "ffmpeg",
		TempDir:            t.TempDir(),
	}

	tr := NewTranscoder(cfg)
	args := tr.buildHLSArgs("/tmp/input.mp4", "/tmp/output")

	// Проверяем ключевые аргументы
	hasInput := false
	hasHlsTime := false
	hasHlsSegmentType := false
	hasGopSize := false
	hasScThreshold := false
	hasKeyintMin := false

	for i, arg := range args {
		if arg == "-i" && i+1 < len(args) && args[i+1] == "/tmp/input.mp4" {
			hasInput = true
		}
		if arg == "-hls_time" {
			hasHlsTime = true
		}
		if arg == "-hls_segment_type" {
			hasHlsSegmentType = true
		}
		if arg == "-g" {
			hasGopSize = true
		}
		if arg == "-sc_threshold" {
			hasScThreshold = true
		}
		if arg == "-keyint_min" {
			hasKeyintMin = true
		}
	}

	if !hasInput {
		t.Error("HLS args missing -i input")
	}
	if !hasHlsTime {
		t.Error("HLS args missing -hls_time")
	}
	if !hasHlsSegmentType {
		t.Error("HLS args missing -hls_segment_type (fMP4)")
	}
	if !hasGopSize {
		t.Error("HLS args missing -g (keyframe interval)")
	}
	if !hasScThreshold {
		t.Error("HLS args missing -sc_threshold")
	}
	if !hasKeyintMin {
		t.Error("HLS args missing -keyint_min")
	}

	// Проверяем, что для каждого битрейта есть свой выходной файл
	outputFiles := 0
	for _, arg := range args {
		if filepath.Ext(arg) == ".m3u8" {
			outputFiles++
		}
	}
	if outputFiles != 3 {
		t.Errorf("Expected 3 variant playlists in args, got %d", outputFiles)
	}
}

func TestBuildHLSArgsBitrateParams(t *testing.T) {
	cfg := &config.VideoConfig{
		SegmentDurationSec: 4,
		Bitrates:           []string{"500k"},
		FFmpegPath:         "ffmpeg",
		TempDir:            t.TempDir(),
	}

	tr := NewTranscoder(cfg)
	args := tr.buildHLSArgs("/tmp/input.mp4", "/tmp/output")
	argsStr := strings.Join(args, " ")

	// Проверяем что для low битрейта правильные параметры
	if !strings.Contains(argsStr, "-b:v 500k") {
		t.Error("Missing -b:v 500k for low bitrate")
	}
	if !strings.Contains(argsStr, "-maxrate 500k") {
		t.Error("Missing -maxrate 500k for low bitrate")
	}
	if !strings.Contains(argsStr, "-bufsize 1000k") {
		t.Error("Missing -bufsize (2x bitrate) for low bitrate")
	}
	if !strings.Contains(argsStr, "-preset fast") {
		t.Error("Missing -preset fast")
	}
	if !strings.Contains(argsStr, "-c:v libx264") {
		t.Error("Missing -c:v libx264")
	}
}

func TestBuildHLSArgsSegmentFilenames(t *testing.T) {
	cfg := &config.VideoConfig{
		SegmentDurationSec: 4,
		Bitrates:           []string{"500k", "1500k"},
		FFmpegPath:         "ffmpeg",
		TempDir:            t.TempDir(),
	}

	tr := NewTranscoder(cfg)
	args := tr.buildHLSArgs("/tmp/input.mp4", "/tmp/output")
	argsStr := strings.Join(args, " ")

	// Проверяем что сегменты лежат в правильных поддиректориях
	if !strings.Contains(argsStr, "low/seg_%d.m4s") {
		t.Error("Missing low/seg_%d.m4s segment filename pattern")
	}
	if !strings.Contains(argsStr, "medium/seg_%d.m4s") {
		t.Error("Missing medium/seg_%d.m4s segment filename pattern")
	}
}

func TestTranscodeMissingInput(t *testing.T) {
	cfg := &config.VideoConfig{
		SegmentDurationSec: 4,
		Bitrates:           []string{"500k"},
		FFmpegPath:         "ffmpeg",
		FFprobePath:        "ffprobe",
		TempDir:            t.TempDir(),
		MaxDurationSec:     60,
	}

	tr := NewTranscoder(cfg)
	ctx := t.Context()

	_, err := tr.Transcode(ctx, "/nonexistent/input.mp4", t.TempDir())
	if err == nil {
		t.Error("Expected error for missing input file")
	}
}

func TestProbeDurationInvalidPath(t *testing.T) {
	cfg := &config.VideoConfig{
		FFprobePath: "ffprobe",
	}

	tr := NewTranscoder(cfg)
	ctx := t.Context()

	_, err := tr.probeDuration(ctx, "/nonexistent/file.mp4")
	if err == nil {
		t.Error("Expected error for nonexistent file in probeDuration")
	}
}
