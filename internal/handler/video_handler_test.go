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

func TestHandleStreamMaster(t *testing.T) {
	cfg := &config.Config{
		Video: config.VideoConfig{
			TempDir: t.TempDir(),
		},
		Pinning: config.PinningConfig{
			RetryDelayMs: 100,
			Retries:      1,
		},
	}
	h := setupTestHandler(cfg)
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
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=3600" {
		t.Errorf("Cache-Control = %q, want public, max-age=3600", cc)
	}
}

func TestHandleStreamMasterNotFound(t *testing.T) {
	cfg := &config.Config{
		Video: config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupTestHandler(cfg)

	req := httptest.NewRequest(http.MethodGet, "/stream/QmNonExistent/master.m3u8", nil)
	w := httptest.NewRecorder()
	h.HandleStreamMaster(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", w.Code)
	}
}

func TestHandleStreamMasterInvalidURL(t *testing.T) {
	cfg := &config.Config{
		Video: config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupTestHandler(cfg)

	req := httptest.NewRequest(http.MethodGet, "/stream/", nil)
	w := httptest.NewRecorder()
	h.HandleStreamMaster(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", w.Code)
	}
}

func TestHandleStreamMasterDeletedVideo(t *testing.T) {
	cfg := &config.Config{
		Video: config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupTestHandler(cfg)
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
		Video: config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupTestHandler(cfg)
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
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
		t.Errorf("Cache-Control = %q, want public, max-age=86400", cc)
	}
}

func TestHandleStreamSegmentPlaylist(t *testing.T) {
	cfg := &config.Config{
		Video: config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupTestHandler(cfg)
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
		Video: config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupTestHandler(cfg)

	req := httptest.NewRequest(http.MethodGet, "/stream/segment/", nil)
	w := httptest.NewRecorder()
	h.HandleStreamSegment(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", w.Code)
	}
}

func TestHandleStreamSegmentNotFound(t *testing.T) {
	cfg := &config.Config{
		Video: config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupTestHandler(cfg)

	req := httptest.NewRequest(http.MethodGet, "/stream/segment/QmNonExistent.m4s", nil)
	w := httptest.NewRecorder()
	h.HandleStreamSegment(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", w.Code)
	}
}

func TestHandleStreamSegmentDeleted(t *testing.T) {
	cfg := &config.Config{
		Video: config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupTestHandler(cfg)
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
		Video: config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupTestHandler(cfg)

	req := httptest.NewRequest(http.MethodPost, "/upload-video", nil)
	w := httptest.NewRecorder()
	h.HandleUploadVideo(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400 for no file", w.Code)
	}
}

func TestHandleUploadVideoNotVideoExt(t *testing.T) {
	cfg := &config.Config{
		Video: config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupTestHandler(cfg)

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
