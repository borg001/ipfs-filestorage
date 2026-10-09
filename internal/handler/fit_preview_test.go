package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/bundle"
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
	for _, suffix := range []string{"/fit_480", "/fit_1024", "/fit_1024", ""} {
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
		if suffix == "/fit_480" && (width != 480 || height != 240) {
			t.Fatalf("tile preview dimensions = %dx%d", width, height)
		}
		if suffix == "/fit_1024" && (width != 1024 || height != 512) {
			t.Fatalf("fit preview dimensions = %dx%d", width, height)
		}
	}
	for variant := range fitPreviewSizes {
		if _, ok := overrides.Get(store.VariantOverrideKey(manifest.CID, variant)); !ok {
			t.Fatalf("%s preview was not persisted", variant)
		}
	}
}

func TestProtectedFitPreviewUsesCurrentMask(t *testing.T) {
	cid := testCID("ProtectedFit")
	policy := policyServer(t, cid, []map[string]interface{}{{"delivery_mode": "blur_faces"}})
	defer policy.Close()
	h := setupTestHandler(&config.Config{Image: config.ImageConfig{JPEGQuality: 82}})
	h.mediaAccess = newMediaAccessResolver(config.MediaAccessConfig{URL: policy.URL, TimeoutMs: 1000})
	overrides, err := store.NewVariantOverrides(filepath.Join(t.TempDir(), "variants.json"))
	if err != nil {
		t.Fatal(err)
	}
	h.variantOverrides = overrides
	encodeColor := func(c color.Color) []byte {
		img := image.NewRGBA(image.Rect(0, 0, 1200, 600))
		for y := 0; y < 600; y++ {
			for x := 0; x < 1200; x++ {
				img.Set(x, y, c)
			}
		}
		var out bytes.Buffer
		if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 90}); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	manifest := bundle.Manifest{CID: cid, Type: "image", Original: bundle.Asset{BundlePath: bundle.OriginalFilename, ContentType: "image/jpeg"}, Variants: map[string]bundle.Asset{
		config.PrivacyBlurVariantKey:     {BundlePath: "privacy/blur.jpg", ContentType: "image/jpeg"},
		config.PrivacyFaceBlurVariantKey: {BundlePath: "privacy/blur_faces.jpg", ContentType: "image/jpeg"},
	}}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	cluster := h.cluster.(*mockCluster)
	cluster.dirs[cid] = map[string][]byte{
		bundle.ManifestFilename:  encoded,
		bundle.OriginalFilename:  encodeColor(color.RGBA{R: 220, A: 255}),
		"privacy/blur.jpg":       encodeColor(color.RGBA{B: 220, A: 255}),
		"privacy/blur_faces.jpg": encodeColor(color.RGBA{G: 220, A: 255}),
	}
	check := func(path string, expected color.RGBA) {
		w := httptest.NewRecorder()
		h.HandleFile(w, httptest.NewRequest(http.MethodGet, "/file/"+cid+path+"?token=viewer-token", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		img, err := jpeg.Decode(bytes.NewReader(w.Body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != 480 || img.Bounds().Dy() != 240 {
			t.Fatalf("%s dimensions: %v", path, img.Bounds())
		}
		r, g, b, _ := img.At(240, 120).RGBA()
		if expected.R > 0 && r < 40000 || expected.G > 0 && g < 40000 || expected.B > 0 && b < 40000 {
			t.Fatalf("%s returned wrong mask color: %d,%d,%d", path, r, g, b)
		}
	}
	check("/fit_480", color.RGBA{G: 220})
	check("/fit_480/blur", color.RGBA{B: 220})
	added, err := cluster.ClusterAdd(context.Background(), "blur_faces.jpg", bytes.NewReader(encodeColor(color.RGBA{R: 220, G: 220, A: 255})))
	if err != nil {
		t.Fatal(err)
	}
	if err := overrides.Set(store.VariantOverrideKey(cid, config.PrivacyFaceBlurVariantKey), added.CID); err != nil {
		t.Fatal(err)
	}
	check("/fit_480", color.RGBA{R: 220, G: 220})
}
