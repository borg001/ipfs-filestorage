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
	"github.com/borg001/ipfs-filestorage/internal/imageproc"
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
	testInitCID     = testCID("Init")
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
		cfg:            cfg,
		cluster:        cluster,
		unpinStore:     unpinStore,
		imageProcessor: imageproc.NewProcessor(cfg.Image, cfg.Video.FFmpegPath),
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

func TestHandleStreamLinkRewritesEveryHLSReferenceWithoutCIDs(t *testing.T) {
	cfg := &config.Config{Video: config.VideoConfig{TempDir: t.TempDir()}}
	h := setupVideoTestHandler(t, cfg)
	cluster := h.cluster.(*mockCluster)

	policy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/view/id/77" {
			t.Fatalf("policy path = %q, want /view/id/77", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer viewer-token" {
			t.Fatalf("Authorization = %q, want forwarded token", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"item": map[string]interface{}{
			"delivery_mode": map[string]interface{}{"value": "original"},
			"storage_uri":   map[string]interface{}{"value": "video://" + testMasterCID},
			"poster_uri":    map[string]interface{}{"value": "ipfs://" + testPosterCID},
		}})
	}))
	defer policy.Close()
	h.mediaAccess = newMediaAccessResolver(config.MediaAccessConfig{URL: policy.URL, TimeoutMs: 1000})

	cluster.files[testMasterCID] = []byte("#EXTM3U\n#EXT-X-IAMFREE-POSTER:SIZE=360x640,URI=\"../segment/" + testPosterCID + ".jpg\"\n#EXT-X-STREAM-INF:BANDWIDTH=500000\n../segment/" + testLowCID + ".m3u8\n")
	cluster.files[testLowCID] = []byte("#EXTM3U\n#EXT-X-MAP:URI=\"../segment/" + testInitCID + ".mp4\"\n#EXTINF:4,\n../segment/" + testSegmentCID + ".m4s\n")
	cluster.files[testInitCID] = []byte("init")
	cluster.files[testSegmentCID] = []byte("segment")

	masterRequest := httptest.NewRequest(http.MethodGet, "/stream/link/77/master.m3u8?token=viewer-token", nil)
	masterResponse := httptest.NewRecorder()
	h.HandleStreamLink(masterResponse, masterRequest)
	if masterResponse.Code != http.StatusOK {
		t.Fatalf("master status = %d, want 200: %s", masterResponse.Code, masterResponse.Body.String())
	}
	master := masterResponse.Body.String()
	if !strings.Contains(master, "playlist/0.m3u8?token=viewer-token") || !strings.Contains(master, "URI=\"poster.jpg?token=viewer-token\"") {
		t.Fatalf("opaque master was not rewritten: %s", master)
	}
	for _, cid := range []string{testMasterCID, testPosterCID, testLowCID, testInitCID, testSegmentCID} {
		if strings.Contains(master, cid) {
			t.Fatalf("master exposes CID %q: %s", cid, master)
		}
	}

	playlistRequest := httptest.NewRequest(http.MethodGet, "/stream/link/77/playlist/0.m3u8?token=viewer-token", nil)
	playlistResponse := httptest.NewRecorder()
	h.HandleStreamLink(playlistResponse, playlistRequest)
	if playlistResponse.Code != http.StatusOK {
		t.Fatalf("playlist status = %d, want 200: %s", playlistResponse.Code, playlistResponse.Body.String())
	}
	playlist := playlistResponse.Body.String()
	if !strings.Contains(playlist, "URI=\"../segment/0/0.mp4?token=viewer-token\"") || !strings.Contains(playlist, "../segment/0/1.m4s?token=viewer-token") {
		t.Fatalf("opaque variant was not rewritten: %s", playlist)
	}
	for _, cid := range []string{testMasterCID, testPosterCID, testLowCID, testInitCID, testSegmentCID} {
		if strings.Contains(playlist, cid) {
			t.Fatalf("variant exposes CID %q: %s", cid, playlist)
		}
	}

	segmentRequest := httptest.NewRequest(http.MethodGet, "/stream/link/77/segment/0/1.m4s?token=viewer-token", nil)
	segmentResponse := httptest.NewRecorder()
	h.HandleStreamLink(segmentResponse, segmentRequest)
	if segmentResponse.Code != http.StatusOK || segmentResponse.Body.String() != "segment" {
		t.Fatalf("opaque segment = status %d, body %q", segmentResponse.Code, segmentResponse.Body.String())
	}
}

func TestHandleStreamLinkServesBlurredPosterForPrivateVideo(t *testing.T) {
	cfg := &config.Config{Video: config.VideoConfig{TempDir: t.TempDir()}}
	h := setupVideoTestHandler(t, cfg)
	cluster := h.cluster.(*mockCluster)
	blurredPosterCID := testCID("OpaqueBlurPoster")
	cluster.files[testPosterCID] = []byte("original-poster")
	cluster.files[blurredPosterCID] = []byte("blurred-poster")

	policy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/view/id/77" {
			t.Fatalf("policy path = %q, want /view/id/77", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"item": map[string]interface{}{
			"delivery_mode": map[string]interface{}{"value": "blur"},
			"storage_uri":   map[string]interface{}{"value": "video://" + testMasterCID},
			"poster_uri":    map[string]interface{}{"value": "ipfs://" + testPosterCID},
			"metadata": map[string]interface{}{"value": map[string]interface{}{
				"poster_aliases": map[string]interface{}{
					testPosterCID: map[string]interface{}{"blur": blurredPosterCID},
				},
			}},
		}})
	}))
	defer policy.Close()
	h.mediaAccess = newMediaAccessResolver(config.MediaAccessConfig{URL: policy.URL, TimeoutMs: 1000})

	posterRequest := httptest.NewRequest(http.MethodGet, "/stream/link/77/poster.jpg?token=viewer-token", nil)
	posterResponse := httptest.NewRecorder()
	h.HandleStreamLink(posterResponse, posterRequest)
	if posterResponse.Code != http.StatusOK || posterResponse.Body.String() != "blurred-poster" {
		t.Fatalf("private opaque poster = status %d, body %q", posterResponse.Code, posterResponse.Body.String())
	}
	if got := posterResponse.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("poster Cache-Control = %q, want private no-store", got)
	}

	masterRequest := httptest.NewRequest(http.MethodGet, "/stream/link/77/master.m3u8?token=viewer-token", nil)
	masterResponse := httptest.NewRecorder()
	h.HandleStreamLink(masterResponse, masterRequest)
	if masterResponse.Code != http.StatusForbidden {
		t.Fatalf("private opaque master = %d, want 403", masterResponse.Code)
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

	var resp uploadErrorResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "unsupported_video_format" {
		t.Errorf("Code = %q, want unsupported_video_format", resp.Code)
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

	var resp uploadErrorResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "video_file_too_large" || resp.Message == "" {
		t.Fatalf("Expected structured oversized video error, got %+v", resp)
	}
}

func TestHandleUploadVideo_LocalizesAspectRatioFailure(t *testing.T) {
	response := uploadErrorResponse{}
	// The direct handler response is covered here because ffprobe is deliberately
	// mocked in validator tests; HTTP presentation must remain localized.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/upload-video?lang=ru", nil)
	writeUploadError(w, req, http.StatusBadRequest, "video_aspect_ratio_invalid", map[string]any{"expected_aspect_ratio": "9:16"})
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != "video_aspect_ratio_invalid" || response.Message != "Можно загрузить только вертикальное видео 9:16." {
		t.Fatalf("Unexpected localized aspect error: %+v", response)
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
