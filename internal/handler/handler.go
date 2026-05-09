package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/ipfs"
	ipfscluster "github.com/borg001/ipfs-filestorage/internal/ipfs"
)

// Response стандартная структура ответа
type Response struct {
	CID    string `json:"cid"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Pinned bool   `json:"pinned"`
}

// Handler HTTP-хендлеры
type Handler struct {
	cfg     *config.Config
	cluster *ipfscluster.ClusterManager
}

// NewHandler создаёт хендлер
func NewHandler(cfg *config.Config) *Handler {
	cluster := ipfscluster.NewCluster(cfg.ClusterNodes)
	if cluster == nil {
		// fallback — хотя бы локальная нода
		cluster = ipfscluster.NewCluster([]string{cfg.IPFSURL})
	}
	return &Handler{cfg: cfg, cluster: cluster}
}

// ---------------------------------------------------------------------------
// POST /upload
// ---------------------------------------------------------------------------
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Парсинг multipart (32 MB в памяти, остальное — на диск)
	r.ParseMultipartForm(32 << 20)
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"No file uploaded"}`, http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Валидация
	if header.Size > h.cfg.UploadMaxFileSize {
		resp := map[string]interface{}{
			"error":    "File too large",
			"maxSize":  h.cfg.UploadMaxFileSize,
		}
		writeJSON(w, http.StatusRequestEntityTooLarge, resp)
		return
	}

	if err := validateFile(header.Filename, header.Header.Get("Content-Type"), h.cfg); err != nil {
		resp := map[string]interface{}{
			"error":        err.Error(),
			"allowedTypes": h.cfg.AllowedExtensions,
		}
		writeJSON(w, http.StatusBadRequest, resp)
		return
	}

	// Загрузка в IPFS (на первую ноду)
	cid, _, err := h.cluster.ClusterAdd(r.Context(), header.Filename, file)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Upload failed"})
		return
	}

	// Репликация — пиннит на ВСЕ ноды кластера параллельно
	_, err = h.cluster.ClusterPinAll(r.Context(), cid, h.cfg.PinningRetries, h.cfg.PinningRetryDelay)
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

// ---------------------------------------------------------------------------
// POST /upload-multiple
// ---------------------------------------------------------------------------
func (h *Handler) UploadMultiple(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// multipart parsing
	r.ParseMultipartForm(32 << 20)
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		http.Error(w, `{"error":"No files uploaded"}`, http.StatusBadRequest)
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
		contentType := fh.Header.Get("Content-Type")
		if err := validateFile(fh.Filename, contentType, h.cfg); err != nil {
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
	errors := make([]string, 0)

	for i, fh := range files {
		wg.Add(1)
		go func(idx int, fileHeader *multipart.FileHeader) {
			defer wg.Done()
			f, err := fileHeader.Open()
			if err != nil {
				mu.Lock()
				errors = append(errors, fmt.Sprintf("%s: open failed", fileHeader.Filename))
				mu.Unlock()
				return
			}
			defer f.Close()

			cid, _, err := h.cluster.ClusterAdd(ctx, fileHeader.Filename, f)
			if err != nil {
				mu.Lock()
				errors = append(errors, fmt.Sprintf("%s: upload failed", fileHeader.Filename))
				mu.Unlock()
				return
			}

			// Пиннинг (одна нода — локальная, остальные — репликация)
			_, _ = h.cluster.ClusterPinAll(ctx, cid, h.cfg.PinningRetries, h.cfg.PinningRetryDelay)

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
	
	if len(errors) > 0 {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error":  "Some uploads failed",
			"details": errors,
		})
		return
	}

	writeJSON(w, http.StatusOK, results)
}

// import multipart нужен, но не было в файле выше. Добавлю в финальный файл
