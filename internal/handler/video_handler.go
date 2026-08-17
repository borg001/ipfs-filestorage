package handler

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/bundle"
	"github.com/borg001/ipfs-filestorage/internal/ipfs"
	"github.com/borg001/ipfs-filestorage/internal/video"
)

// VideoResponse — ответ POST /upload-video.
type VideoResponse struct {
	MasterCID         string                       `json:"master_cid"`
	VariantCIDs       map[string]string            `json:"variant_cids"`
	PosterCIDs        map[string]string            `json:"poster_cids,omitempty"`
	PrivacyPosterCIDs map[string]map[string]string `json:"privacy_poster_cids,omitempty"`
	DurationSec       float64                      `json:"duration_sec"`
	Status            string                       `json:"status"`
}

// clusterAdder адаптирует ipfs.Clusterer к интерфейсу video.IPFSAdder.
type clusterAdder struct {
	cluster ipfs.Clusterer
}

func (c *clusterAdder) Add(ctx context.Context, filename string, r io.Reader) (*ipfs.AddResult, error) {
	return c.cluster.ClusterAdd(ctx, filename, r)
}

// HandleUploadVideo обрабатывает POST /upload-video.
func (h *Handler) HandleUploadVideo(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Failed to parse form"})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "No file uploaded"})
		return
	}
	defer file.Close()

	// Проверяем, что это видео по расширению
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(header.Filename), "."))
	videoExts := map[string]bool{"mp4": true, "mov": true, "webm": true, "avi": true, "mkv": true}
	if !videoExts[ext] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Not a video file"})
		return
	}

	// Проверяем свободное место на диске (3x от максимального размера)
	if err := os.MkdirAll(h.cfg.Video.TempDir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Temp dir creation failed"})
		return
	}
	if err := checkDiskSpace(h.cfg.Video.TempDir, h.cfg.Video.MaxSizeBytes*3); err != nil {
		writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": err.Error()})
		return
	}

	// Сохраняем во временный файл с серверной проверкой размера
	tmpInput, err := os.CreateTemp(h.cfg.Video.TempDir, "video-input-*.mp4")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Temp file creation failed"})
		return
	}
	defer os.Remove(tmpInput.Name())
	defer tmpInput.Close()

	// Серверная проверка размера: ограничиваем чтение через LimitReader
	limitedReader := &countingReader{r: io.LimitReader(file, h.cfg.Video.MaxSizeBytes+1)}
	if _, err := tmpInput.ReadFrom(limitedReader); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save temp file"})
		return
	}

	// Если прочитано больше лимита — файл слишком большой
	if limitedReader.bytesRead > h.cfg.Video.MaxSizeBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]interface{}{
			"error":   "Video file too large",
			"maxSize": h.cfg.Video.MaxSizeBytes,
		})
		return
	}

	ctx := r.Context()

	// Валидация видео (используем реальный размер, а не header.Size)
	validator := video.NewValidator(&h.cfg.Video)
	if err := validator.Validate(ctx, tmpInput.Name(), limitedReader.bytesRead); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Транскодирование
	outputDir, err := os.MkdirTemp(h.cfg.Video.TempDir, "video-output-*")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Temp dir creation failed"})
		return
	}
	defer os.RemoveAll(outputDir)

	transcoder := video.NewTranscoder(&h.cfg.Video)
	result, err := transcoder.Transcode(ctx, tmpInput.Name(), outputDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Transcoding failed"})
		return
	}
	if err := h.buildVideoPrivacyPosters(ctx, outputDir); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Poster processing failed"})
		return
	}

	// Загрузка в IPFS — через Clusterer → clusterAdder → video.IPFSAdder
	uploader := video.NewUploader(&clusterAdder{cluster: h.cluster})
	uploadResult, err := uploader.UploadDir(ctx, outputDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "IPFS upload failed"})
		return
	}

	// Репликация всех CID
	retryDelay := time.Duration(h.cfg.Pinning.RetryDelayMs) * time.Millisecond
	for _, cid := range uploadResult.AllCIDs {
		_ = h.cluster.ClusterReplicate(ctx, cid, h.cfg.Pinning.Retries, retryDelay)
	}

	// Сохраняем маппинг master_cid → all_cids для будущего группового удаления.
	h.unpinStore.TrackGroup(uploadResult.MasterCID, uploadResult.AllCIDs)

	resp := VideoResponse{
		MasterCID:         uploadResult.MasterCID,
		VariantCIDs:       uploadResult.VariantCIDs,
		PosterCIDs:        uploadResult.PosterCIDs,
		PrivacyPosterCIDs: uploadResult.PrivacyPosterCIDs,
		DurationSec:       result.Duration,
		Status:            "processing_done",
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleStreamMaster обрабатывает GET /stream/{cid}/master.m3u8.
func (h *Handler) HandleStreamMaster(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	parts := strings.Split(strings.TrimPrefix(path, "/stream/"), "/")
	if len(parts) < 2 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid stream URL"})
		return
	}
	cid := parts[0]

	if err := validateCID(cid); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if h.unpinStore.Has(cid) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Video not found"})
		return
	}

	ctx := r.Context()
	reader, err := h.fetchVideoAsset(ctx, cid)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Video not found"})
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	writePlaylist(w, reader, playlistAuthSuffix(r.URL.Query()))
}

// HandleStreamSegment обрабатывает GET /stream/segment/{cid}.
func (h *Handler) HandleStreamSegment(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/stream/segment/")

	cid := path
	if dotIdx := strings.LastIndex(path, "."); dotIdx > 0 {
		cid = path[:dotIdx]
	}

	if err := validateCID(cid); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if h.unpinStore.Has(cid) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Segment not found"})
		return
	}

	ctx := r.Context()
	reader, err := h.fetchVideoAsset(ctx, cid)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Segment not found"})
		return
	}
	defer reader.Close()

	contentType := streamSegmentContentType(path)

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	if contentType == "application/vnd.apple.mpegurl" {
		writePlaylist(w, reader, playlistAuthSuffix(r.URL.Query()))
		return
	}
	io.Copy(w, reader)
}

func streamSegmentContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return "video/mp4"
	}
}

func (h *Handler) fetchVideoAsset(ctx context.Context, cid string) (io.ReadCloser, error) {
	reader, err := h.cluster.ClusterTryFetch(ctx, cid)
	if err == nil {
		return reader, nil
	}
	return h.cluster.ClusterTryFetchPath(ctx, cid, bundle.OriginalFilename)
}

func playlistAuthSuffix(query url.Values) string {
	key := "token"
	token := query.Get(key)
	if token == "" {
		key = "access_token"
		token = query.Get(key)
	}
	if token == "" {
		return ""
	}
	values := url.Values{}
	values.Set(key, token)
	return "?" + values.Encode()
}

func writePlaylist(w io.Writer, reader io.Reader, authSuffix string) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return
	}
	content := string(data)
	if authSuffix == "" {
		io.WriteString(w, content)
		return
	}
	io.WriteString(w, appendPlaylistAuth(content, authSuffix))
}

func appendPlaylistAuth(content, authSuffix string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, `URI="`) {
			lines[i] = appendURIAuth(line, authSuffix)
			continue
		}
		if strings.HasPrefix(trimmed, "#") || strings.Contains(trimmed, "?") {
			continue
		}
		lines[i] = line + authSuffix
	}
	return strings.Join(lines, "\n")
}

func appendURIAuth(line, authSuffix string) string {
	const marker = `URI="`
	start := strings.Index(line, marker)
	if start < 0 {
		return line
	}
	start += len(marker)
	end := strings.Index(line[start:], `"`)
	if end < 0 {
		return line
	}
	end += start
	uri := line[start:end]
	if strings.Contains(uri, "?") {
		return line
	}
	return line[:start] + uri + authSuffix + line[end:]
}
