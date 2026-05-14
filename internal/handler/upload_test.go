package handler

import (
	"bytes"
	"encoding/json"
	"io"
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
			MaxFileSize:      maxSize,
			AllowedExtensions: []string{"txt"},
			AllowedMimeTypes: map[string]bool{"text/plain; charset=utf-8": true},
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
			MaxFileSize:      maxSize,
			AllowedExtensions: []string{"txt"},
			AllowedMimeTypes: map[string]bool{"text/plain; charset=utf-8": true},
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
			MaxFileSize:      maxSize,
			AllowedExtensions: []string{"txt"},
			AllowedMimeTypes: map[string]bool{"text/plain; charset=utf-8": true},
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
