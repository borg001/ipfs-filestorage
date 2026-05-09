package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/ipfs"
	"github.com/borg001/ipfs-filestorage/internal/middleware"
)

func TestHandler_Upload_SingleIPFS(t *testing.T) {
	// Используем совместный IPFS (в CI — 127.0.0.1:5001)
	cfg := newTestConfig()
	cluster := createClusterRetry(cfg, 10)
	if cluster == nil {
		t.Skip("IPFS недоступен — пропуск интеграционного теста")
	}

	h := &Handler{cfg: &cfg, cluster: cluster}

	// Строим multipart
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("file", "test.json")
	fw.Write([]byte(`{"test":true}`))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-API-Key", "secret")
	rr := httptest.NewRecorder()

	// Оборачиваем middleware: PanicRecovery → CORS → Auth → Upload
	chain := middleware.Chain(
		http.HandlerFunc(h.Upload),
		middleware.PanicRecovery(),
		middleware.CORS([]string{"*"}, []string{"Content-Type", "X-API-Key"}),
		middleware.APIKeyAuth([]string{"secret"}),
	)
	chain.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp.CID == "" {
		t.Fatal("CID пустой")
	}
	if resp.Name != "test.json" {
		t.Fatalf("expected name test.json, got %s", resp.Name)
	}
}

func TestHandler_Upload_InvalidType(t *testing.T) {
	cfg := newTestConfig()
	h := &Handler{cfg: &cfg, cluster: nil}
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("file", "virus.exe")
	fw.Write([]byte("MZ"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-API-Key", "secret")
	rr := httptest.NewRecorder()

	wrapped := middleware.APIKeyAuth([]string{"secret"})(http.HandlerFunc(h.Upload))
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Invalid file type") {
		t.Fatalf("expected Invalid file type, got %s", rr.Body.String())
	}
}

func TestHandler_Upload_MissingKey(t *testing.T) {
	cfg := newTestConfig()
	h := &Handler{cfg: &cfg, cluster: nil}
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("file", "test.png")
	fw.Write([]byte("pngdata"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// Нет X-API-Key
	rr := httptest.NewRecorder()

	wrapped := middleware.APIKeyAuth([]string{"secret"})(http.HandlerFunc(h.Upload))
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestHandler_Upload_TooLarge(t *testing.T) {
	cfg := newTestConfig()
	cfg.UploadMaxFileSize = 10
	h := &Handler{cfg: &cfg, cluster: nil}
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("file", "test.png")
	fw.Write([]byte("this is more than 10 bytes"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-API-Key", "secret")
	rr := httptest.NewRecorder()

	wrapped := middleware.APIKeyAuth([]string{"secret"})(http.HandlerFunc(h.Upload))
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_UploadMultiple(t *testing.T) {
	cfg := newTestConfig()
	cluster := createClusterRetry(cfg, 10)
	if cluster == nil {
		t.Skip("IPFS недоступен — пропуск")
	}
	h := &Handler{cfg: &cfg, cluster: cluster}

	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for _, name := range []string{"a.json", "b.json"} {
		fw, _ := mw.CreateFormFile("files", name)
		fw.Write([]byte(fmt.Sprintf(`{"file":"%s"}`, name)))
	}
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload-multiple", &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-API-Key", "secret")
	rr := httptest.NewRecorder()

	chain := middleware.Chain(
		http.HandlerFunc(h.UploadMultiple),
		middleware.PanicRecovery(),
		middleware.CORS([]string{"*"}, []string{"Content-Type", "X-API-Key"}),
		middleware.APIKeyAuth([]string{"secret"}),
	)
	chain.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp []Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp))
	}
}

// --- helpers ---

func newTestConfig() config.Config {
	return config.Config{
		UploadMaxFileSize: 10 * 1024 * 1024,
		AllowedExtensions: []string{"json", "png", "svg", "jpg", "pdf", "doc", "docx", "zip", "html"},
		AllowedMimeTypes: map[string]bool{
			"application/json": true,
			"image/png":        true,
			"text/html":        true,
			"application/octet-stream": true,
		},
		PinningRetries:    3,
		PinningRetryDelay: time.Millisecond * 100,
	}
}

func createClusterRetry(cfg config.Config, retries int) *ipfs.ClusterManager {
	// Находим IPFS URL из env если есть, иначе localhost
	url := os.Getenv("TEST_IPFS_URL")
	if url == "" {
		url = "http://127.0.0.1:5001"
	}

	for i := 0; i < retries; i++ {
		c := ipfs.NewCluster([]string{url})
		if c != nil {
			return c
		}
		time.Sleep(time.Millisecond * 200)
	}
	return nil
}
