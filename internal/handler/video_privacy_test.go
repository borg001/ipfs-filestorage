package handler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

func TestBuildVideoPrivacyPosters(t *testing.T) {
	outputDir := t.TempDir()
	postersDir := filepath.Join(outputDir, "posters")
	if err := os.MkdirAll(postersDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "imageproc", "testdata", "portrait-single-face.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(postersDir, "180x320.jpg"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	h := setupTestHandler(&config.Config{
		Image: config.ImageConfig{
			OutputFormat: "jpeg",
			JPEGQuality:  82,
			Privacy:      config.DefaultImagePrivacyConfig(),
		},
	})
	if err := h.buildVideoPrivacyPosters(context.Background(), outputDir); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{config.PrivacyBlurVariantKey, config.PrivacyFaceBlurVariantKey} {
		path := filepath.Join(postersDir, key, "180x320.jpg")
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("privacy poster %q missing: %v", key, err)
		}
		if info.Size() == 0 {
			t.Fatalf("privacy poster %q is empty", key)
		}
	}
}
