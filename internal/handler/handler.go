package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/ipfs"
	"github.com/borg001/ipfs-filestorage/internal/middleware"
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
	cfg     *config.Config
	cluster *ipfs.ClusterManager
}

// NewHandler создаёт Handler с подключением к IPFS-кластеру.
func NewHandler(cfg *config.Config) *Handler {
	cluster := ipfs.NewCluster(cfg.ClusterNodes)
	if cluster == nil {
		cluster = ipfs.NewCluster([]string{cfg.IPFSURL})
	}
	return &Handler{cfg: cfg, cluster: cluster}
}

// Router возвращает функции хендлеров, обёрнутые middleware.
func (h *Handler) Router(method string) http.HandlerFunc {
	switch method {
	case http.MethodPost:
		return h.postHandler
	case http.MethodGet:
		return h.getHandler
	case http.MethodDelete:
		return h.deleteHandler
	default:
		return h.getHandler
	}
}

func (h *Handler) getHandler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/upload":
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Use POST"})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
	}
}

func (h *Handler) postHandler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/upload":
		h.HandleUpload(w, r)
	case "/upload-multiple":
		h.HandleUploadMultiple(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
	}
}

func (h *Handler) deleteHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "delete handler stub"})
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
	if header.Size > h.cfg.UploadMaxFileSize {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]interface{}{
			"error":   "File too large",
			"maxSize": h.cfg.UploadMaxFileSize,
		})
		return
	}

	// Валидация типа
	if err := h.validateFile(header.Filename, header.Header.Get("Content-Type")); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":        err.Error(),
			"allowedTypes": h.cfg.AllowedExtensions,
		})
		return
	}

	// Загрузка и репликация
	ctx := r.Context()
	cid, _, err := h.cluster.ClusterAdd(ctx, header.Filename, file)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Upload failed"})
		return
	}

	retryDelay := time.Duration(h.cfg.PinningRetryDelay) * time.Millisecond
	_, err = h.cluster.ClusterPinAll(ctx, cid, h.cfg.PinningRetries, retryDelay)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Pinning failed"})
		return
	}

	resp := Response{
		CID:    cid,
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
		if fh.Size > h.cfg.UploadMaxFileSize {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]interface{}{
				"error":   "File too large",
				"maxSize": h.cfg.UploadMaxFileSize,
			})
			return
		}
		if err := h.validateFile(fh.Filename, fh.Header.Get("Content-Type")); err != nil {
			invalid = append(invalid, fh.Filename)
		}
	}
	if len(invalid) > 0 {
		resp := map[string]interface{}{
			"error":        "Invalid file types",
			"allowedTypes": h.cfg.AllowedExtensions,
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
	retryDelay := time.Duration(h.cfg.PinningRetryDelay) * time.Millisecond

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

			cid, _, err := h.cluster.ClusterAdd(ctx, fileHeader.Filename, f)
			if err != nil {
				mu.Lock()
				errorsList = append(errorsList, fmt.Sprintf("%s: upload failed", fileHeader.Filename))
				mu.Unlock()
				return
			}

			_, _ = h.cluster.ClusterPinAll(ctx, cid, h.cfg.PinningRetries, retryDelay)

			mu.Lock()
			results[idx] = Response{
				CID:    cid,
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

func (h *Handler) validateFile(filename string, contentType string) error {
	ext := strings.TrimPrefix(filepath.Ext(filename), ".")
	ext = strings.ToLower(ext)
	allowedExt := false
	for _, e := range h.cfg.AllowedExtensions {
		if e == ext {
			allowedExt = true
			break
		}
	}
	if !allowedExt {
		return fmt.Errorf("Invalid file type")
	}
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = mime.TypeByExtension(filepath.Ext(filename))
	}
	if _, ok := h.cfg.AllowedMimeTypes[contentType]; !ok {
		ct := mime.TypeByExtension("." + ext)
		if ct != "" {
			if _, ok := h.cfg.AllowedMimeTypes[ct]; ok {
				return nil
			}
		}
		return fmt.Errorf("Invalid MIME type: %s", contentType)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
