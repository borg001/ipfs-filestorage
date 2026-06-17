package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

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

func TestHandleFile_DetectsPNGContentType(t *testing.T) {
	cfg := &config.Config{}
	h := setupTestHandler(cfg)

	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00,
	}
	cid := "QmYHB9RhEKYjk5qLFp1UaDyPVB5xaZWBM2WPE3eVLZyV2N"
	cluster := h.cluster.(*mockCluster)
	cluster.mu.Lock()
	cluster.files[cid] = png
	cluster.mu.Unlock()

	fileReq := httptest.NewRequest(http.MethodGet, "/file/"+cid, nil)
	fileW := httptest.NewRecorder()
	h.HandleFile(fileW, fileReq)

	if fileW.Code != http.StatusOK {
		t.Fatalf("file status = %d, want 200, body: %s", fileW.Code, fileW.Body.String())
	}
	if ct := fileW.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", ct)
	}
	if !bytes.Equal(fileW.Body.Bytes(), png) {
		t.Fatal("response body differs from uploaded PNG bytes")
	}
}
