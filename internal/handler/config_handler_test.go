package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if resp.Upload.Media.Video.MaxDurationSec != 60 {
		t.Fatalf("Unexpected public video policy: %+v", resp.Upload.Media.Video)
	}
	if resp.Upload.Media.Video.Accept == "" || len(resp.Upload.Media.Image.MimeTypes) != 3 {
		t.Fatalf("Unexpected public media accept config: %+v", resp.Upload.Media)
	}
}

// A phone shooting in "High Efficiency" offers a HEIC photo, and the file
// picker is told about it by both type and extension.
func TestHandleConfig_OffersHEICWhenItIsAllowed(t *testing.T) {
	h := setupTestHandler(&config.Config{
		Upload: config.UploadConfig{
			MaxFileSize: 10 * 1024 * 1024,
			AllowedMimeTypes: map[string]bool{
				"image/jpeg": true,
				"image/png":  true,
				"image/webp": true,
				"image/heic": true,
				"image/heif": true,
			},
		},
		Video: config.VideoConfig{MaxSizeBytes: 30 * 1024 * 1024, MaxDurationSec: 60},
	})

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()
	h.HandleConfig(w, req)

	var resp publicConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Upload.Media.Image.MimeTypes) != 5 {
		t.Fatalf("Unexpected image mime types: %+v", resp.Upload.Media.Image.MimeTypes)
	}
	for _, want := range []string{"image/heic", "image/heif", ".heic", ".heif"} {
		if !strings.Contains(resp.Upload.Media.Image.Accept, want) {
			t.Fatalf("Image accept %q does not offer %q", resp.Upload.Media.Image.Accept, want)
		}
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
	if resp.Upload.Media.Image.Description != "JPEG, PNG, WebP, HEIC до 10 МБ" {
		t.Fatalf("Unexpected Russian image description: %q", resp.Upload.Media.Image.Description)
	}
	if resp.Upload.Media.Video.Description != "MP4, MOV, WebM, AVI, MKV до 30 МБ, до 60 сек." {
		t.Fatalf("Unexpected Russian video description: %q", resp.Upload.Media.Video.Description)
	}
}

func TestHandleConfig_FormatsLargeVideoLimitsForPeople(t *testing.T) {
	h := setupTestHandler(&config.Config{
		Video: config.VideoConfig{MaxSizeBytes: 1024 * 1024 * 1024, MaxDurationSec: 2400},
	})

	for _, testCase := range []struct {
		name        string
		locale      string
		sizeLabel   string
		description string
	}{
		{name: "english", sizeLabel: "1 GB", description: "MP4, MOV, WebM, AVI, MKV up to 1 GB, up to 40 min"},
		{name: "russian", locale: "ru", sizeLabel: "1 ГБ", description: "MP4, MOV, WebM, AVI, MKV до 1 ГБ, до 40 мин."},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/config?lang="+testCase.locale, nil)
			response := httptest.NewRecorder()
			h.HandleConfig(response, request)

			var payload publicConfigResponse
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Upload.Media.Video.MaxSizeLabel != testCase.sizeLabel {
				t.Fatalf("MaxSizeLabel = %q, want %q", payload.Upload.Media.Video.MaxSizeLabel, testCase.sizeLabel)
			}
			if payload.Upload.Media.Video.Description != testCase.description {
				t.Fatalf("Description = %q, want %q", payload.Upload.Media.Video.Description, testCase.description)
			}
		})
	}
}
