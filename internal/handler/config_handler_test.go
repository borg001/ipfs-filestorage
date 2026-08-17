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
}
