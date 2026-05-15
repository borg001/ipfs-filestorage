package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/store"
)

func setupVideoTestHandler(t *testing.T, cfg *config.Config) *Handler {
	t.Helper()
	cluster := newMockCluster()
	unpinStore, err := store.NewUnpinStore(filepath.Join(t.TempDir(), "test-unpin.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &Handler{
		cfg:        cfg,
		cluster:    cluster,
		unpinStore: unpinStore,
	}
}

func TestHandleStreamMaster(t *testing.T) {
	cfg := &config.Config{
		Video:   config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)
	cluster := h.cluster.(*mockCluster)

	masterContent := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=500000\nQmLow\n"
	cluster.files["QmMasterTest"] = []byte(masterContent)

	req := httptest.NewRequest(http.MethodGet, "/stream/QmMasterTest/master.m3u8", nil)
	w := httptest.NewRecorder()
	h.HandleStreamMaster(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("Content-Type = %q, want application/vnd.apple.mpegurl", ct)
	}
}

func TestHandleStreamMasterNotFound(t *testing.T) {
	cfg := &config.Config{
		Video:   config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/stream/QmNonExistent/master.m3u8", nil)
	w := httptest.NewRecorder()
	h.HandleStreamMaster(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", w.Code)
	}
}

func TestHandleStreamMasterInvalidURL(t *testing.T) {
	cfg := &config.Config{
		Video:   config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/stream/", nil)
	w := httptest.NewRecorder()
	h.HandleStreamMaster(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", w.Code)
	}
}

func TestHandleStreamMasterDeletedVideo(t *testing.T) {
	cfg := &config.Config{
		Video:   config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)
	h.unpinStore.Add("QmDeleted")

	req := httptest.NewRequest(http.MethodGet, "/stream/QmDeleted/master.m3u8", nil)
	w := httptest.NewRecorder()
	h.HandleStreamMaster(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want 404 for deleted video", w.Code)
	}
}

func TestHandleStreamSegment(t *testing.T) {
	cfg := &config.Config{
		Video:   config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)
	cluster := h.cluster.(*mockCluster)

	segmentData := []byte("fake-m4s-segment-data")
	cluster.files["QmSegTest"] = segmentData

	req := httptest.NewRequest(http.MethodGet, "/stream/segment/QmSegTest.m4s", nil)
	w := httptest.NewRecorder()
	h.HandleStreamSegment(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("Content-Type = %q, want video/mp4", ct)
	}
	if !bytes.Equal(w.Body.Bytes(), segmentData) {
		t.Error("Response body mismatch")
	}
}

func TestHandleStreamSegmentPlaylist(t *testing.T) {
	cfg := &config.Config{
		Video:   config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)
	cluster := h.cluster.(*mockCluster)

	playlistData := []byte("#EXTM3U\n#EXTINF:4,\nQmSeg1.m4s\n")
	cluster.files["QmPlaylistTest"] = playlistData

	req := httptest.NewRequest(http.MethodGet, "/stream/segment/QmPlaylistTest.m3u8", nil)
	w := httptest.NewRecorder()
	h.HandleStreamSegment(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("Content-Type = %q, want application/vnd.apple.mpegurl", ct)
	}
}

func TestHandleStreamSegmentNoCID(t *testing.T) {
	cfg := &config.Config{
		Video:   config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/stream/segment/", nil)
	w := httptest.NewRecorder()
	h.HandleStreamSegment(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", w.Code)
	}
}

func TestHandleStreamSegmentNotFound(t *testing.T) {
	cfg := &config.Config{
		Video:   config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/stream/segment/QmNonExistent.m4s", nil)
	w := httptest.NewRecorder()
	h.HandleStreamSegment(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", w.Code)
	}
}

func TestHandleStreamSegmentDeleted(t *testing.T) {
	cfg := &config.Config{
		Video:   config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)
	h.unpinStore.Add("QmDeletedSeg")

	req := httptest.NewRequest(http.MethodGet, "/stream/segment/QmDeletedSeg.m4s", nil)
	w := httptest.NewRecorder()
	h.HandleStreamSegment(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want 404 for deleted segment", w.Code)
	}
}

func TestHandleUploadVideoNoFile(t *testing.T) {
	cfg := &config.Config{
		Video:   config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)

	req := httptest.NewRequest(http.MethodPost, "/upload-video", nil)
	w := httptest.NewRecorder()
	h.HandleUploadVideo(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400 for no file", w.Code)
	}
}

func TestHandleUploadVideoNotVideoExt(t *testing.T) {
	cfg := &config.Config{
		Video:   config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "document.pdf")
	io.Copy(part, bytes.NewReader([]byte("fake pdf content")))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload-video", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.HandleUploadVideo(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400 for non-video file", w.Code)
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "Not a video file" {
		t.Errorf("Error = %q, want 'Not a video file'", resp["error"])
	}
}

func TestHandleUploadVideoTooLarge(t *testing.T) {
	tmpDir := t.TempDir()
	maxBytes := int64(1024)

	cfg := &config.Config{
		Video: config.VideoConfig{
			TempDir:      tmpDir,
			MaxSizeBytes: maxBytes,
		},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "video.mp4")
	io.Copy(part, bytes.NewReader(make([]byte, 2*1024)))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload-video", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.HandleUploadVideo(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400 for file too large", w.Code)
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] == "" {
		t.Error("Expected error message for oversized file")
	}
}

func TestHandleUploadVideoFFprobeFails(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Video: config.VideoConfig{
			TempDir:      tmpDir,
			MaxSizeBytes: 100 * 1024 * 1024,
			FFprobePath:  "nonexistent_ffprobe_binary",
		},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "clip.mp4")
	io.Copy(part, bytes.NewReader([]byte("fake mp4 content")))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload-video", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.HandleUploadVideo(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400 when ffprobe fails", w.Code)
	}
}
