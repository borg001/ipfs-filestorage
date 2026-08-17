package handler

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/bundle"
	"github.com/borg001/ipfs-filestorage/internal/config"
)

func TestHandleUpload_CountingReader(t *testing.T) {
	maxSize := int64(1024) // 1KB
	cfg := &config.Config{
		Upload: config.UploadConfig{
			MaxFileSize:       maxSize,
			AllowedExtensions: []string{"txt"},
			AllowedMimeTypes:  map[string]bool{"text/plain; charset=utf-8": true},
		},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}

	h := setupTestHandler(cfg)

	// Файл 512 байт — меньше лимита
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write(make([]byte, 512))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.HandleUpload(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want 200 for file within limit, body: %s", w.Code, w.Body.String())
	}

	var resp Response
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Size != 512 {
		t.Errorf("Response Size = %d, want 512 (server-measured)", resp.Size)
	}
}

func TestHandleUpload_CountingReader_TooLarge(t *testing.T) {
	maxSize := int64(1024) // 1KB
	cfg := &config.Config{
		Upload: config.UploadConfig{
			MaxFileSize:       maxSize,
			AllowedExtensions: []string{"txt"},
			AllowedMimeTypes:  map[string]bool{"text/plain; charset=utf-8": true},
		},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}

	h := setupTestHandler(cfg)

	// Файл 2KB — больше лимита
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write(make([]byte, 2*1024))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.HandleUpload(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("Status = %d, want 413 for oversized file, body: %s", w.Code, w.Body.String())
	}
}

func TestHandleUpload_FakeHeaderSize(t *testing.T) {
	maxSize := int64(1024) // 1KB
	cfg := &config.Config{
		Upload: config.UploadConfig{
			MaxFileSize:       maxSize,
			AllowedExtensions: []string{"txt"},
			AllowedMimeTypes:  map[string]bool{"text/plain; charset=utf-8": true},
		},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}

	h := setupTestHandler(cfg)

	// Реальный контент = 2KB — сервер считает байты, а не верит заголовку
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write(make([]byte, 2*1024))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.HandleUpload(w, req)

	// Сервер использует countingReader → 413
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("Status = %d, want 413 — server must verify actual bytes, not header", w.Code)
	}
}

func TestHandleUpload_ImageBundleVariants(t *testing.T) {
	cfg := &config.Config{
		Upload: config.UploadConfig{
			MaxFileSize:       1024 * 1024,
			AllowedExtensions: []string{"png"},
			AllowedMimeTypes:  map[string]bool{"image/png": true},
		},
		Image: config.ImageConfig{
			ProcessingEnabled: true,
			Variants:          []config.ImageVariant{{Key: "100x100", Width: 100, Height: 100}},
			OutputFormat:      "jpeg",
			JPEGProgressive:   false,
			JPEGQuality:       80,
			ResizePolicy:      "cover-center",
		},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupTestHandler(cfg)

	var imageBuf bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 200, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 200; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 180, A: 255})
		}
	}
	if err := png.Encode(&imageBuf, img); err != nil {
		t.Fatal(err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "avatar.png")
	part.Write(imageBuf.Bytes())
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.HandleUpload(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Type != "image" {
		t.Fatalf("Response Type = %q, want image", resp.Type)
	}
	if resp.Original == nil || resp.Original.Width != 200 || resp.Original.Height != 120 {
		t.Fatalf("Original metadata = %+v, want 200x120", resp.Original)
	}
	if resp.PrivacyBlur == nil || resp.PrivacyBlur.CID == "" {
		t.Fatalf("Privacy blur metadata missing: %+v", resp.PrivacyBlur)
	}
	if resp.PrivacyBlur.CID == resp.CID {
		t.Fatal("Privacy blur must use an independent CID")
	}
	variant, ok := resp.Variants["100x100"]
	if !ok {
		t.Fatalf("Variant 100x100 missing: %+v", resp.Variants)
	}
	if variant.ContentType != "image/jpeg" || variant.Width != 100 || variant.Height != 100 {
		t.Fatalf("Variant metadata = %+v, want image/jpeg 100x100", variant)
	}

	bundleReq := httptest.NewRequest(http.MethodGet, "/file/"+resp.CID+"/bundle", nil)
	bundleW := httptest.NewRecorder()
	h.HandleFile(bundleW, bundleReq)
	if bundleW.Code != http.StatusOK {
		t.Fatalf("Bundle status = %d, want 200, body: %s", bundleW.Code, bundleW.Body.String())
	}
	var manifest bundle.Manifest
	if err := json.Unmarshal(bundleW.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.CID != resp.CID || manifest.Original.Path != "/file/"+resp.CID {
		t.Fatalf("Manifest paths not finalized: %+v", manifest)
	}

	variantReq := httptest.NewRequest(http.MethodGet, "/file/"+resp.CID+"/100x100", nil)
	variantW := httptest.NewRecorder()
	h.HandleFile(variantW, variantReq)
	if variantW.Code != http.StatusOK {
		t.Fatalf("Variant status = %d, want 200, body: %s", variantW.Code, variantW.Body.String())
	}
	if got := variantW.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Variant Content-Type = %q, want image/jpeg", got)
	}
	cfgJPEG, err := jpeg.DecodeConfig(bytes.NewReader(variantW.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if cfgJPEG.Width != 100 || cfgJPEG.Height != 100 {
		t.Fatalf("Variant dimensions = %dx%d, want 100x100", cfgJPEG.Width, cfgJPEG.Height)
	}

	blurReq := httptest.NewRequest(http.MethodGet, "/file/"+resp.PrivacyBlur.CID, nil)
	blurW := httptest.NewRecorder()
	h.HandleFile(blurW, blurReq)
	if blurW.Code != http.StatusOK {
		t.Fatalf("Privacy blur status = %d, want 200, body: %s", blurW.Code, blurW.Body.String())
	}
	blurCfg, err := jpeg.DecodeConfig(bytes.NewReader(blurW.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if blurCfg.Width != 200 || blurCfg.Height != 120 {
		t.Fatalf("Privacy blur dimensions = %dx%d, want 200x120", blurCfg.Width, blurCfg.Height)
	}

	deriveReq := httptest.NewRequest(http.MethodPost, "/derive-blur/"+resp.CID, nil)
	deriveW := httptest.NewRecorder()
	h.HandleDerivePrivacyBlur(deriveW, deriveReq)
	if deriveW.Code != http.StatusOK {
		t.Fatalf("Derive privacy blur status = %d, want 200, body: %s", deriveW.Code, deriveW.Body.String())
	}
	var derived struct {
		PrivacyBlur bundle.Asset `json:"privacy_blur"`
	}
	if err := json.Unmarshal(deriveW.Body.Bytes(), &derived); err != nil {
		t.Fatal(err)
	}
	if derived.PrivacyBlur.CID == "" || derived.PrivacyBlur.CID == resp.CID {
		t.Fatalf("Derived privacy blur must use an independent CID: %+v", derived.PrivacyBlur)
	}
	derivedReq := httptest.NewRequest(http.MethodGet, "/file/"+derived.PrivacyBlur.CID, nil)
	derivedW := httptest.NewRecorder()
	h.HandleFile(derivedW, derivedReq)
	if derivedW.Code != http.StatusOK {
		t.Fatalf("Derived privacy blur status = %d, want 200", derivedW.Code)
	}
	if !bytes.Equal(derivedW.Body.Bytes(), blurW.Body.Bytes()) {
		t.Fatal("Deriving a privacy blur from the same original must produce identical blurred bytes")
	}
}
