package handler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/video"
)

// VideoResponse — ответ POST /upload-video.
type VideoResponse struct {
	MasterCID   string            `json:"master_cid"`
	VariantCIDs map[string]string `json:"variant_cids"`
	DurationSec float64           `json:"duration_sec"`
	Status      string            `json:"status"`
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

	// Сохраняем во временный файл
	tmpInput, err := os.CreateTemp(h.cfg.Video.TempDir, "video-input-*.mp4")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Temp file creation failed"})
		return
	}
	defer os.Remove(tmpInput.Name())
	defer tmpInput.Close()

	if _, err := tmpInput.ReadFrom(file); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save temp file"})
		return
	}

	ctx := r.Context()

	// Валидация видео
	validator := video.NewValidator(&h.cfg.Video)
	if err := validator.Validate(ctx, tmpInput.Name(), header.Size); err != nil {
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

	// Загрузка в IPFS
	uploader := video.NewUploader(h.cluster.LocalClient())
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

	// Сохраняем маппинг master_cid → all_cids для группового удаления
	h.unpinStore.AddGroup(uploadResult.MasterCID, uploadResult.AllCIDs)

	resp := VideoResponse{
		MasterCID:   uploadResult.MasterCID,
		VariantCIDs: uploadResult.VariantCIDs,
		DurationSec: result.Duration,
		Status:      "processing_done",
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleStreamMaster обрабатывает GET /stream/{cid}/master.m3u8.
func (h *Handler) HandleStreamMaster(w http.ResponseWriter, r *http.Request) {
	// URL: /stream/{cid}/master.m3u8
	path := r.URL.Path
	parts := strings.Split(strings.TrimPrefix(path, "/stream/"), "/")
	if len(parts) < 2 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid stream URL"})
		return
	}
	cid := parts[0]

	if h.unpinStore.Has(cid) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Video not found"})
		return
	}

	ctx := r.Context()
	reader, err := h.cluster.ClusterTryFetch(ctx, cid)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Video not found"})
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	reader.WriteTo(w)
}

// HandleStreamSegment обрабатывает GET /stream/segment/{cid}.
func (h *Handler) HandleStreamSegment(w http.ResponseWriter, r *http.Request) {
	// URL: /stream/segment/{cid} или /stream/segment/{cid}.m4s
	path := strings.TrimPrefix(r.URL.Path, "/stream/segment/")
	cid := strings.TrimSuffix(path, ".m4s")
	cid = strings.TrimSuffix(path, ".m3u8")

	if cid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "CID required"})
		return
	}

	if h.unpinStore.Has(cid) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Segment not found"})
		return
	}

	ctx := r.Context()
	reader, err := h.cluster.ClusterTryFetch(ctx, cid)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Segment not found"})
		return
	}
	defer reader.Close()

	// Определяем Content-Type
	contentType := "video/mp4"
	if strings.HasSuffix(path, ".m3u8") {
		contentType = "application/vnd.apple.mpegurl"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	reader.WriteTo(w)
}

// WriteTo — хелпер для записи io.Reader в ResponseWriter
// Нужен т.к. ClusterTryFetch возвращает io.ReadCloser, а не files.File
func writeToResponse(w http.ResponseWriter, r interface{ Read([]byte) (int, error) }) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
}
