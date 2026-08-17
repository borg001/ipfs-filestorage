package handler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/borg001/ipfs-filestorage/internal/imageproc"
)

// buildVideoPrivacyPosters creates privacy copies after ffmpeg has extracted
// the ordinary poster JPEGs. The original posters remain untouched; the
// uploader exposes the generated variants as separate CIDs.
func (h *Handler) buildVideoPrivacyPosters(ctx context.Context, outputDir string) error {
	postersDir := filepath.Join(outputDir, "posters")
	entries, err := os.ReadDir(postersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read posters directory: %w", err)
	}

	processor := imageproc.NewProcessor(h.cfg.Image, h.cfg.Video.FFmpegPath)
	for _, entry := range entries {
		if entry.IsDir() || !isPosterImage(entry.Name()) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		posterPath := filepath.Join(postersDir, entry.Name())
		data, err := os.ReadFile(posterPath)
		if err != nil {
			return fmt.Errorf("read poster %s: %w", entry.Name(), err)
		}
		variants, err := processor.ProcessPrivacy(ctx, data, "image/jpeg")
		if err != nil {
			return fmt.Errorf("process privacy poster %s: %w", entry.Name(), err)
		}
		for _, variant := range variants {
			dir := filepath.Join(postersDir, variant.Key)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create poster variant directory %s: %w", variant.Key, err)
			}
			filename := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())) + "." + extensionForFormat(variant.Format)
			if err := os.WriteFile(filepath.Join(dir, filename), variant.Data, 0o644); err != nil {
				return fmt.Errorf("write privacy poster %s/%s: %w", variant.Key, entry.Name(), err)
			}
		}
	}
	return nil
}

func isPosterImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	default:
		return false
	}
}

func extensionForFormat(format string) string {
	if format == "jpeg" {
		return "jpg"
	}
	return format
}
