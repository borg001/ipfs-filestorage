package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

func TestHandleConfig_ReturnsPublicImageConfig(t *testing.T) {
	h := setupTestHandler(&config.Config{
		Upload: config.UploadConfig{
			MaxFileSize: 10 * 1024 * 1024,
			AllowedMimeTypes: map[string]bool{
				"image/jpeg": true,
				"image/png":  true,
				"image/webp": true,
			},
		},
		Video: config.VideoConfig{MaxSizeBytes: 30 * 1024 * 1024, MaxDurationSec: 60},
		Image: config.ImageConfig{
			ProcessingEnabled: true,
			Variants:          []config.ImageVariant{{Key: "100x100", Width: 100, Height: 100}},
			OutputFormat:      "auto",
			JPEGProgressive:   true,
			ResizePolicy:      "smart-cover",
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()
	h.HandleConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want 200", w.Code)
	}

	var resp publicConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Image.Enabled || resp.Image.URLTemplate != "/file/{cid}/{size}" || resp.Image.BundleTemplate != "/file/{cid}/bundle" {
		t.Fatalf("Unexpected image config: %+v", resp.Image)
	}
	if len(resp.Image.Variants) != 1 || resp.Image.Variants[0].Key != "100x100" {
		t.Fatalf("Unexpected variants: %+v", resp.Image.Variants)
	}
	if len(resp.Image.PrivacyVariants) != 2 || resp.Image.PrivacyVariants[0].Key != config.PrivacyBlurVariantKey || resp.Image.PrivacyVariants[1].Fallback != config.PrivacyBlurVariantKey {
		t.Fatalf("Unexpected privacy variants: %+v", resp.Image.PrivacyVariants)
	}
	if resp.Upload.Media.Image.MaxSizeLabel != "10 MB" || resp.Upload.Media.Video.MaxSizeLabel != "30 MB" {
		t.Fatalf("Unexpected public size labels: %+v", resp.Upload.Media)
	}
	if resp.Upload.Media.Video.MaxDurationSec != 60 || resp.Upload.Media.Video.ExpectedAspectRatio != "9:16" {
		t.Fatalf("Unexpected public video policy: %+v", resp.Upload.Media.Video)
	}
	if resp.Upload.Media.Video.Accept == "" || len(resp.Upload.Media.Image.MimeTypes) != 3 {
		t.Fatalf("Unexpected public media accept config: %+v", resp.Upload.Media)
	}
}

func TestHandleConfig_LocalizesPublicUploadDescriptions(t *testing.T) {
	h := setupTestHandler(&config.Config{
		Upload: config.UploadConfig{MaxFileSize: 10 * 1024 * 1024},
		Video:  config.VideoConfig{MaxSizeBytes: 30 * 1024 * 1024, MaxDurationSec: 60},
	})
	req := httptest.NewRequest(http.MethodGet, "/config?lang=ru", nil)
	w := httptest.NewRecorder()
	h.HandleConfig(w, req)

	var resp publicConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Upload.Media.Image.Description != "JPEG, PNG, WebP до 10 МБ" {
		t.Fatalf("Unexpected Russian image description: %q", resp.Upload.Media.Image.Description)
	}
	if resp.Upload.Media.Video.Description != "MP4, MOV, WebM, AVI, MKV до 30 МБ, до 60 сек., вертикальное 9:16" {
		t.Fatalf("Unexpected Russian video description: %q", resp.Upload.Media.Video.Description)
	}
}
