package handler

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/store"
)

func TestFitPreviewExistingBundleIsPersistedAndOriginalStaysAvailable(t *testing.T) {
	h := setupTestHandler(&config.Config{Image: config.ImageConfig{JPEGQuality: 82}})
	overrides, err := store.NewVariantOverrides(filepath.Join(t.TempDir(), "variants.json"))
	if err != nil {
		t.Fatal(err)
	}
	h.variantOverrides = overrides
	src := image.NewRGBA(image.Rect(0, 0, 1200, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 1200; x++ {
			src.Set(x, y, color.RGBA{R: 170, G: 80, B: 50, A: 255})
		}
	}
	var input bytes.Buffer
	if err := jpeg.Encode(&input, src, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	manifest, err := h.buildFileBundle(context.Background(), "photo.jpg", input.Bytes(), "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	path := "/file/" + manifest.CID
	for _, suffix := range []string{"/fit_1024", "/fit_1024", ""} {
		w := httptest.NewRecorder()
		h.HandleFile(w, httptest.NewRequest(http.MethodGet, path+suffix, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", suffix, w.Code, w.Body.String())
		}
		got, err := jpeg.Decode(bytes.NewReader(w.Body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		width, height := got.Bounds().Dx(), got.Bounds().Dy()
		if suffix == "" && (width != 1200 || height != 600) {
			t.Fatalf("original changed to %dx%d", width, height)
		}
		if suffix != "" && (width != 1024 || height != 512) {
			t.Fatalf("fit preview dimensions = %dx%d", width, height)
		}
	}
	if _, ok := overrides.Get(store.VariantOverrideKey(manifest.CID, fitPreviewVariant)); !ok {
		t.Fatal("fit preview was not persisted")
	}
}
