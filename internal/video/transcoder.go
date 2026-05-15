package video

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

// TranscodeResult — результат транскодирования видео в HLS/CMAF.
type TranscodeResult struct {
	// OutputDir — директория со сгенерированными файлами.
	OutputDir string
	// Variants — мапа: имя варианта ("low","medium","high") → поддиректория.
	Variants map[string]string
	// Duration — длительность видео в секундах.
	Duration float64
}

// Transcoder — транскодирует видео через ffmpeg.
type Transcoder struct {
	cfg *config.VideoConfig
}

// NewTranscoder создаёт новый Transcoder.
func NewTranscoder(cfg *config.VideoConfig) *Transcoder {
	return &Transcoder{cfg: cfg}
}

// Transcode транскодирует видео в адаптивный HLS (fMP4 CMAF).
// inputPath — путь к исходному видеофайлу.
// outputDir — директория для результата (будет создана).
func (t *Transcoder) Transcode(ctx context.Context, inputPath, outputDir string) (*TranscodeResult, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	// Получаем длительность через ffprobe
	duration, err := t.probeDuration(ctx, inputPath)
	if err != nil {
		return nil, fmt.Errorf("probe duration: %w", err)
	}

	if duration > float64(t.cfg.MaxDurationSec) {
		return nil, fmt.Errorf("video duration %.1fs exceeds max %ds", duration, t.cfg.MaxDurationSec)
	}

	// Строим ffmpeg-аргументы для multi-variant HLS
	args := t.buildHLSArgs(inputPath, outputDir)

	cmd := exec.CommandContext(ctx, t.cfg.FFmpegPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %w\noutput: %s", err, string(output))
	}

	// Собираем варианты
	variants := make(map[string]string)
	for _, br := range t.cfg.Bitrates {
		name := bitrateToName(br)
		variants[name] = filepath.Join(outputDir, name)
	}

	return &TranscodeResult{
		OutputDir: outputDir,
		Variants:  variants,
		Duration:  duration,
	}, nil
}

// buildHLSArgs формирует аргументы ffmpeg для генерации multi-variant HLS/CMAF.
func (t *Transcoder) buildHLSArgs(inputPath, outputDir string) []string {
	segDur := fmt.Sprintf("%d", t.cfg.SegmentDurationSec)

	args := []string{
		"-i", inputPath,
		"-y", // перезаписать
	}

	// Один -map + codec для каждого битрейта
	for _, br := range t.cfg.Bitrates {
		name := bitrateToName(br)
		variantDir := filepath.Join(outputDir, name)
		// ffmpeg создаст поддиректории сам через -hls_segment_filename

		args = append(args,
			"-map", "v:0",
			"-c:v", "libx264",
			"-b:v", br,
			"-maxrate", br,
			"-bufsize", multiplyBitrate(br, 2),
			"-preset", "fast",
			"-g", segDur, // keyframe interval = segment duration
			"-keyint_min", segDur,
			"-sc_threshold", "0",

			// fMP4 контейнер для CMAF
			"-f", "hls",
			"-hls_time", segDur,
			"-hls_list_size", "0",
			"-hls_flags", "independent_segments",
			"-hls_segment_type", "1", // 1 = fMP4
			"-hls_segment_filename", filepath.Join(variantDir, "seg_%d.m4s"),
			filepath.Join(variantDir, "playlist.m3u8"),
		)
	}

	return args
}

// probeDuration возвращает длительность видео через ffprobe.
func (t *Transcoder) probeDuration(ctx context.Context, inputPath string) (float64, error) {
	cmd := exec.CommandContext(ctx, t.cfg.FFprobePath,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputPath,
	)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe failed: %w", err)
	}

	var dur float64
	_, err = fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &dur)
	if err != nil {
		return 0, fmt.Errorf("parse duration: %w", err)
	}
	return dur, nil
}

// bitrateToName конвертирует битрейт в имя варианта ("500k" → "low", "1500k" → "medium", etc.)
func bitrateToName(br string) string {
	// Убираем суффикс "k" и парсим
	clean := strings.TrimSuffix(br, "k")
	clean = strings.TrimSuffix(clean, "K")

	var val int
	_, err := fmt.Sscanf(clean, "%d", &val)
	if err != nil {
		return br
	}

	switch {
	case val <= 700:
		return "low"
	case val <= 2000:
		return "medium"
	default:
		return "high"
	}
}

// multiplyBitrate умножает битрейт на множитель ("1500k" * 2 → "3000k")
func multiplyBitrate(br string, factor int) string {
	clean := strings.TrimSuffix(br, "k")
	clean = strings.TrimSuffix(clean, "K")

	var val int
	_, err := fmt.Sscanf(clean, "%d", &val)
	if err != nil {
		return br
	}
	return fmt.Sprintf("%dk", val*factor)
}
