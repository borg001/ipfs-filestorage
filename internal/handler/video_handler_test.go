package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/bundle"
	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/store"
)

var (
	testMasterCID   = testCID("Master")
	testLowCID      = testCID("Low")
	testMissingCID  = testCID("Missing")
	testDeletedCID  = testCID("Deleted")
	testSegmentCID  = testCID("Segment")
	testPlaylistCID = testCID("Playlist")
	testPosterCID   = testCID("Poster")
)

func testCID(seed string) string {
	return "Qm" + seed + strings.Repeat("1", 44-len(seed))
}

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

	masterContent := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=500000\n" + testLowCID + "\n"
	cluster.files[testMasterCID] = []byte(masterContent)

	req := httptest.NewRequest(http.MethodGet, "/stream/"+testMasterCID+"/master.m3u8", nil)
	w := httptest.NewRecorder()
	h.HandleStreamMaster(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("Content-Type = %q, want application/vnd.apple.mpegurl", ct)
	}
}

func TestHandleStreamMasterFromBundleOriginal(t *testing.T) {
	cfg := &config.Config{
		Video:   config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)
	cluster := h.cluster.(*mockCluster)

	masterContent := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=500000\n" + testLowCID + ".m3u8\n"
	cluster.dirs[testMasterCID] = map[string][]byte{
		bundle.OriginalFilename: []byte(masterContent),
	}

	req := httptest.NewRequest(http.MethodGet, "/stream/"+testMasterCID+"/master.m3u8", nil)
	w := httptest.NewRecorder()
	h.HandleStreamMaster(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", w.Code)
	}
	if w.Body.String() != masterContent {
		t.Error("Response body mismatch")
	}
}

func TestHandleStreamMasterPropagatesQueryToken(t *testing.T) {
	cfg := &config.Config{
		Video:   config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)
	cluster := h.cluster.(*mockCluster)

	cluster.files[testMasterCID] = []byte("#EXTM3U\n../segment/" + testLowCID + ".m3u8\n")

	req := httptest.NewRequest(http.MethodGet, "/stream/"+testMasterCID+"/master.m3u8?token=abc", nil)
	w := httptest.NewRecorder()
	h.HandleStreamMaster(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "../segment/"+testLowCID+".m3u8?token=abc") {
		t.Error("master playlist should propagate query token")
	}
}

func TestHandleStreamMasterNotFound(t *testing.T) {
	cfg := &config.Config{
		Video:   config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/stream/"+testMissingCID+"/master.m3u8", nil)
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
	h.unpinStore.Add(testDeletedCID)

	req := httptest.NewRequest(http.MethodGet, "/stream/"+testDeletedCID+"/master.m3u8", nil)
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
	cluster.files[testSegmentCID] = segmentData

	req := httptest.NewRequest(http.MethodGet, "/stream/segment/"+testSegmentCID+".m4s", nil)
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

func TestHandleStreamSegmentPoster(t *testing.T) {
	cfg := &config.Config{
		Video:   config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)
	cluster := h.cluster.(*mockCluster)

	posterData := []byte("fake-jpeg-poster-data")
	cluster.files[testPosterCID] = posterData

	req := httptest.NewRequest(http.MethodGet, "/stream/segment/"+testPosterCID+".jpg", nil)
	w := httptest.NewRecorder()
	h.HandleStreamSegment(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", ct)
	}
	if !bytes.Equal(w.Body.Bytes(), posterData) {
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
	cluster.files[testPlaylistCID] = playlistData

	req := httptest.NewRequest(http.MethodGet, "/stream/segment/"+testPlaylistCID+".m3u8", nil)
	w := httptest.NewRecorder()
	h.HandleStreamSegment(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("Content-Type = %q, want application/vnd.apple.mpegurl", ct)
	}
}

func TestHandleStreamSegmentFromBundleOriginal(t *testing.T) {
	cfg := &config.Config{
		Video:   config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)
	cluster := h.cluster.(*mockCluster)

	playlistData := []byte("#EXTM3U\n#EXTINF:4,\nQmSeg1.m4s\n")
	cluster.dirs[testPlaylistCID] = map[string][]byte{
		bundle.OriginalFilename: playlistData,
	}

	req := httptest.NewRequest(http.MethodGet, "/stream/segment/"+testPlaylistCID+".m3u8", nil)
	w := httptest.NewRecorder()
	h.HandleStreamSegment(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("Content-Type = %q, want application/vnd.apple.mpegurl", ct)
	}
	if !bytes.Equal(w.Body.Bytes(), playlistData) {
		t.Error("Response body mismatch")
	}
}

func TestHandleStreamSegmentPlaylistPropagatesQueryToken(t *testing.T) {
	cfg := &config.Config{
		Video:   config.VideoConfig{TempDir: t.TempDir()},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}
	h := setupVideoTestHandler(t, cfg)
	cluster := h.cluster.(*mockCluster)

	playlistData := []byte("#EXTM3U\n#EXT-X-IAMFREE-POSTER:SIZE=180x320,URI=\"../segment/QmPoster.jpg\"\n#EXT-X-MAP:URI=\"QmInit.mp4\"\nQmSeg1.m4s\n")
	cluster.files[testPlaylistCID] = playlistData

	req := httptest.NewRequest(http.MethodGet, "/stream/segment/"+testPlaylistCID+".m3u8?token=abc", nil)
	w := httptest.NewRecorder()
	h.HandleStreamSegment(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "#EXT-X-MAP:URI=\"QmInit.mp4?token=abc\"") {
		t.Error("variant playlist should propagate query token to init map")
	}
	if !strings.Contains(body, "#EXT-X-IAMFREE-POSTER:SIZE=180x320,URI=\"../segment/QmPoster.jpg?token=abc\"") {
		t.Error("playlist should propagate query token to poster URI")
	}
	if !strings.Contains(body, "QmSeg1.m4s?token=abc") {
		t.Error("variant playlist should propagate query token to media segment")
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

	req := httptest.NewRequest(http.MethodGet, "/stream/segment/"+testMissingCID+".m4s", nil)
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
	h.unpinStore.Add(testDeletedCID)

	req := httptest.NewRequest(http.MethodGet, "/stream/segment/"+testDeletedCID+".m4s", nil)
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

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("Status = %d, want 413 for file too large", w.Code)
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
