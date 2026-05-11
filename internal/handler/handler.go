package handler

import (
	"fmt"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/ipfs"
	"github.com/borg001/ipfs-filestorage/internal/store"
	"github.com/borg001/ipfs-filestorage/internal/unpin"
)

// Response — стандартная структура ответа API.
type Response struct {
	CID    string `json:"cid"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Pinned bool   `json:"pinned"`
}

// Handler содержит HTTP-хендлеры сервиса.
type Handler struct {
	cfg         *config.Config
	cluster     ipfs.Clusterer
	unpinStore  *store.UnpinStore
	unpinWorker *unpin.Worker
}

// NewHandler создаёт Handler с подключением к IPFS-кластеру.
func NewHandler(cfg *config.Config) *Handler {
	cluster := ipfs.NewCluster(cfg.IPFS.ClusterNodes)
	if cluster == nil {
		cluster = ipfs.NewCluster([]string{cfg.IPFS.LocalURL})
	}

	unpinStore, err := store.NewUnpinStore(cfg.Unpin.StorePath)
	if err != nil {
		unpinStore, _ = store.NewUnpinStore("/tmp/unpin-store.json")
	}

	h := &Handler{
		cfg:        cfg,
		cluster:    cluster,
		unpinStore: unpinStore,
	}

	// Запускаем TTL worker
	if cfg.Unpin.TTL > 0 {
		h.unpinWorker = unpin.NewWorker(cluster, unpinStore, cfg.Unpin.TTL, cfg.Unpin.GCInterval)
		h.unpinWorker.Start()
	}

	return h
}

// HandleUpload обрабатывает POST /upload (один файл).
func (h *Handler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "No file uploaded"})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "No file uploaded"})
		return
	}
	defer file.Close()

	// Валидация размера
	if header.Size > h.cfg.Upload.MaxFileSize {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]interface{}{
			"error":   "File too large",
			"maxSize": h.cfg.Upload.MaxFileSize,
		})
		return
	}

	// Валидация типа
	if err := validateFile(header.Filename, header.Header.Get("Content-Type"), h.cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":        err.Error(),
			"allowedTypes": h.cfg.Upload.AllowedExtensions,
		})
		return
	}

	// Загрузка на первую ноду
	ctx := r.Context()
	result, err := h.cluster.ClusterAdd(ctx, header.Filename, file)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Upload failed"})
		return
	}

	// Репликация на все ноды кластера (Fetch + Pin)
	retryDelay := time.Duration(h.cfg.Pinning.RetryDelayMs) * time.Millisecond
	if err := h.cluster.ClusterReplicate(ctx, result.CID, h.cfg.Pinning.Retries, retryDelay); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Replication failed"})
		return
	}

	resp := Response{
		CID:    result.CID,
		Name:   header.Filename,
		Size:   header.Size,
		Pinned: true,
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleUploadMultiple обрабатывает POST /upload-multiple.
func (h *Handler) HandleUploadMultiple(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "No files uploaded"})
		return
	}
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "No files uploaded"})
		return
	}

	// Валидация
	invalid := make([]string, 0)
	for _, fh := range files {
		if fh.Size > h.cfg.Upload.MaxFileSize {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]interface{}{
				"error":   "File too large",
				"maxSize": h.cfg.Upload.MaxFileSize,
			})
			return
		}
		if err := validateFile(fh.Filename, fh.Header.Get("Content-Type"), h.cfg); err != nil {
			invalid = append(invalid, fh.Filename)
		}
	}
	if len(invalid) > 0 {
		resp := map[string]interface{}{
			"error":        "Invalid file types",
			"allowedTypes": h.cfg.Upload.AllowedExtensions,
			"invalidFiles": invalid,
		}
		writeJSON(w, http.StatusBadRequest, resp)
		return
	}

	// Параллельная загрузка
	ctx := r.Context()
	results := make([]Response, len(files))
	var wg sync.WaitGroup
	var mu sync.Mutex
	errorsList := make([]string, 0)
	retryDelay := time.Duration(h.cfg.Pinning.RetryDelayMs) * time.Millisecond

	for i, fh := range files {
		wg.Add(1)
		go func(idx int, fileHeader *multipart.FileHeader) {
			defer wg.Done()
			f, err := fileHeader.Open()
			if err != nil {
				mu.Lock()
				errorsList = append(errorsList, fmt.Sprintf("%s: open failed", fileHeader.Filename))
				mu.Unlock()
				return
			}
			defer f.Close()

			result, err := h.cluster.ClusterAdd(ctx, fileHeader.Filename, f)
			if err != nil {
				mu.Lock()
				errorsList = append(errorsList, fmt.Sprintf("%s: upload failed", fileHeader.Filename))
				mu.Unlock()
				return
			}

			// Репликация на все ноды кластера (Fetch + Pin)
			_ = h.cluster.ClusterReplicate(ctx, result.CID, h.cfg.Pinning.Retries, retryDelay)

			mu.Lock()
			results[idx] = Response{
				CID:    result.CID,
				Name:   fileHeader.Filename,
				Size:   fileHeader.Size,
				Pinned: true,
			}
			mu.Unlock()
		}(i, fh)
	}
	wg.Wait()

	if len(errorsList) > 0 {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":   "Some uploads failed",
			"details": errorsList,
		})
		return
	}

	writeJSON(w, http.StatusOK, results)
}

// HandleFile обрабатывает GET /file/{cid}.
func (h *Handler) HandleFile(w http.ResponseWriter, r *http.Request) {
	cid := strings.TrimPrefix(r.URL.Path, "/file/")
	if cid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "CID required"})
		return
	}

	// Проверяем unpin-список (soft-delete)
	if h.unpinStore.Has(cid) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error":   "File not found",
			"message": "File was deleted",
		})
		return
	}

	ctx := r.Context()

	// Пытаемся получить данные из кластера
	reader, err := h.cluster.ClusterTryFetch(ctx, cid)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "File not found"})
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, reader)
}

// HandleDelete обрабатывает DELETE /file/{cid}.
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	cid := strings.TrimPrefix(r.URL.Path, "/file/")
	if cid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "CID required"})
		return
	}

	// Добавляем в unpin-список (soft-delete)
	h.unpinStore.Add(cid)

	// Асинхронный unpin на всех нодах
	ctx := r.Context()
	go func() {
		h.cluster.ClusterUnpinAll(ctx, cid)
	}()

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "deleted",
		"cid":    cid,
	})
}
