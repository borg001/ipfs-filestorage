package video

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

type hlsVariant struct {
	Name    string
	Bitrate string
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

	// Validation has already accepted this file. Reuse the same metadata
	// contract to avoid creating output renditions that exceed its source.
	info, err := probeVideoInfo(ctx, t.cfg.FFprobePath, inputPath)
	if err != nil {
		return nil, fmt.Errorf("probe duration: %w", err)
	}
	duration := info.Duration

	if duration > float64(t.cfg.MaxDurationSec) {
		return nil, fmt.Errorf("video duration %.1fs exceeds max %ds", duration, t.cfg.MaxDurationSec)
	}

	if err := t.generateThumbnails(ctx, inputPath, outputDir); err != nil {
		return nil, fmt.Errorf("generate thumbnails: %w", err)
	}

	hlsVariants := t.selectVariants(info)
	// Generate only useful HLS renditions. A 500 kbps phone clip must not be
	// expanded into medium and high copies before IPFS upload.
	args := t.buildHLSArgsForVariants(inputPath, outputDir, hlsVariants, info.FrameRate)
	for _, variant := range hlsVariants {
		variantDir := filepath.Join(outputDir, variant.Name)
		if err := os.MkdirAll(variantDir, 0o755); err != nil {
			return nil, fmt.Errorf("create variant dir %s: %w", variantDir, err)
		}
	}

	cmd := exec.CommandContext(ctx, t.cfg.FFmpegPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %w\noutput: %s", err, string(output))
	}

	// Собираем варианты
	variants := make(map[string]string)
	for _, variant := range hlsVariants {
		variants[variant.Name] = filepath.Join(outputDir, variant.Name)
	}

	return &TranscodeResult{
		OutputDir: outputDir,
		Variants:  variants,
		Duration:  duration,
	}, nil
}

// buildHLSArgs формирует аргументы ffmpeg для генерации multi-variant HLS/CMAF.
func (t *Transcoder) buildHLSArgs(inputPath, outputDir string) []string {
	return t.buildHLSArgsForVariants(inputPath, outputDir, t.selectVariants(nil), 0)
}

func (t *Transcoder) buildHLSArgsForVariants(inputPath, outputDir string, variants []hlsVariant, frameRate float64) []string {
	segDur := fmt.Sprintf("%d", t.cfg.SegmentDurationSec)
	gop := fmt.Sprintf("%d", t.gopSize(frameRate))
	forceKeyFrames := fmt.Sprintf("expr:gte(t,n_forced*%d)", t.cfg.SegmentDurationSec)

	args := []string{
		"-i", inputPath,
		"-y", // перезаписать
	}

	// One video and optional audio map for every selected rendition.
	for _, variant := range variants {
		variantDir := filepath.Join(outputDir, variant.Name)
		// ffmpeg создаст поддиректории сам через -hls_segment_filename

		args = append(args,
			"-map", "v:0",
			"-map", "0:a?",
			"-c:v", "libx264",
			"-b:v", variant.Bitrate,
			"-maxrate", variant.Bitrate,
			"-bufsize", multiplyBitrate(variant.Bitrate, 2),
			"-preset", "fast",
			"-g", gop,
			"-keyint_min", gop,
			"-sc_threshold", "0",
			"-force_key_frames", forceKeyFrames,
			"-c:a", "aac",
			"-b:a", "128k",

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

// selectVariants keeps the configured ladder for source videos that can use
// it. A rendition above the source bitrate only increases processing time and
// IPFS storage without adding visual detail, so it is omitted. The lowest
// rendition is retained as the universal fallback.
func (t *Transcoder) selectVariants(info *VideoInfo) []hlsVariant {
	variants := make([]hlsVariant, 0, len(t.cfg.Bitrates))
	seen := make(map[string]struct{}, len(t.cfg.Bitrates))
	maxBitrate := int64(0)
	if info != nil && info.BitRate > 0 {
		maxBitrate = int64(float64(info.BitRate) * 1.2)
	}
	for _, bitrate := range t.cfg.Bitrates {
		name := bitrateToName(bitrate)
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		value, ok := bitrateValue(bitrate)
		if len(variants) > 0 && maxBitrate > 0 && (!ok || value > maxBitrate) {
			continue
		}
		variants = append(variants, hlsVariant{Name: name, Bitrate: bitrate})
		seen[name] = struct{}{}
	}
	if len(variants) == 0 && len(t.cfg.Bitrates) > 0 {
		variants = append(variants, hlsVariant{Name: bitrateToName(t.cfg.Bitrates[0]), Bitrate: t.cfg.Bitrates[0]})
	}
	return variants
}

func (t *Transcoder) gopSize(frameRate float64) int {
	if frameRate <= 0 {
		frameRate = 30
	}
	segmentDuration := t.cfg.SegmentDurationSec
	if segmentDuration <= 0 {
		segmentDuration = 4
	}
	return max(1, int(math.Round(frameRate*float64(segmentDuration))))
}

func (t *Transcoder) generateThumbnails(ctx context.Context, inputPath, outputDir string) error {
	if len(t.cfg.ThumbnailVariants) == 0 {
		return nil
	}
	postersDir := filepath.Join(outputDir, "posters")
	if err := os.MkdirAll(postersDir, 0o755); err != nil {
		return fmt.Errorf("create posters dir: %w", err)
	}
	for _, variant := range t.cfg.ThumbnailVariants {
		if variant.Width <= 0 || variant.Height <= 0 {
			continue
		}
		outputPath := filepath.Join(postersDir, thumbnailFilename(variant))
		args := t.buildThumbnailArgs(inputPath, outputPath, variant)
		cmd := exec.CommandContext(ctx, t.cfg.FFmpegPath, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("ffmpeg thumbnail %s failed: %w\noutput: %s", variant.Key, err, string(output))
		}
	}
	return nil
}

func (t *Transcoder) buildThumbnailArgs(inputPath, outputPath string, variant config.ImageVariant) []string {
	size := fmt.Sprintf("%d:%d", variant.Width, variant.Height)
	filter := fmt.Sprintf("scale=%s:force_original_aspect_ratio=increase,crop=%s", size, size)
	seek := fmt.Sprintf("%.3f", t.cfg.ThumbnailTimeSec)
	qscale := fmt.Sprintf("%d", t.cfg.ThumbnailQScale)
	return []string{
		"-ss", seek,
		"-i", inputPath,
		"-frames:v", "1",
		"-vf", filter,
		"-q:v", qscale,
		"-y",
		outputPath,
	}
}

func thumbnailFilename(variant config.ImageVariant) string {
	key := strings.TrimSpace(variant.Key)
	if key == "" {
		key = fmt.Sprintf("%dx%d", variant.Width, variant.Height)
	}
	key = strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_").Replace(key)
	return key + ".jpg"
}

func (t *Transcoder) probeDuration(ctx context.Context, inputPath string) (float64, error) {
	info, err := probeVideoInfo(ctx, t.cfg.FFprobePath, inputPath)
	if err != nil {
		return 0, err
	}
	return info.Duration, nil
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

func bitrateValue(bitrate string) (int64, bool) {
	value := strings.TrimSpace(strings.ToLower(bitrate))
	multiplier := int64(1)
	if strings.HasSuffix(value, "k") {
		multiplier = 1000
		value = strings.TrimSuffix(value, "k")
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, false
	}
	return parsed * multiplier, true
}
