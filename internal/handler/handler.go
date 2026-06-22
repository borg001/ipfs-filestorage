package handler

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"syscall"
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

func (h *Handler) replicateAsync(cid string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		retryDelay := time.Duration(h.cfg.Pinning.RetryDelayMs) * time.Millisecond
		if err := h.cluster.ClusterReplicate(ctx, cid, h.cfg.Pinning.Retries, retryDelay); err != nil {
			log.Printf("[replication] async replication failed for %s: %v", cid, err)
		}
	}()
}

// countingReader оборачивает io.Reader и считает прочитанные байты.
type countingReader struct {
	r         io.Reader
	bytesRead int64
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	cr.bytesRead += int64(n)
	return n, err
}

// validateCID checks that a string looks like a valid IPFS CID.
func validateCID(cid string) error {
	if cid == "" {
		return fmt.Errorf("CID required")
	}
	// Accept Qm... (CIDv0) and bafy.../bafk... (CIDv1)
	if strings.HasPrefix(cid, "Qm") && len(cid) >= 46 {
		return nil
	}
	if (strings.HasPrefix(cid, "bafy") || strings.HasPrefix(cid, "bafk")) && len(cid) >= 50 {
		return nil
	}
	return fmt.Errorf("invalid CID format")
}

// checkDiskSpace verifies there is enough free disk space for an operation.
// Returns error with HTTP 507 if insufficient.
func checkDiskSpace(dir string, requiredBytes int64) error {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		// If we can't check, allow the operation (best effort)
		return nil
	}
	freeBytes := int64(stat.Bavail) * int64(stat.Bsize)
	if freeBytes < requiredBytes {
		return fmt.Errorf("insufficient disk space: need %d bytes, %d available", requiredBytes, freeBytes)
	}
	return nil
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

	// Валидация типа
	if err := validateFile(header.Filename, header.Header.Get("Content-Type"), h.cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":        err.Error(),
			"allowedTypes": h.cfg.Upload.AllowedExtensions,
		})
		return
	}

	// Оборачиваем reader в countingReader для серверной проверки размера.
	// Не доверяем header.Size из multipart — его легко подделать.
	limitedReader := &countingReader{r: io.LimitReader(file, h.cfg.Upload.MaxFileSize+1)}

	// Загрузка на первую ноду
	ctx := r.Context()
	result, err := h.cluster.ClusterAdd(ctx, header.Filename, limitedReader)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Upload failed"})
		return
	}

	// Серверная проверка: если прочитано больше MaxFileSize — файл слишком большой.
	// Это достоверная проверка, основанная на реальных байтах, а не на заголовке.
	if limitedReader.bytesRead > h.cfg.Upload.MaxFileSize {
		_ = h.cluster.ClusterUnpinAll(ctx, result.CID)
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]interface{}{
			"error":   "File too large",
			"maxSize": h.cfg.Upload.MaxFileSize,
		})
		return
	}

	h.replicateAsync(result.CID)

	// Используем проверенный сервером размер, а не header.Size
	resp := Response{
		CID:    result.CID,
		Name:   header.Filename,
		Size:   limitedReader.bytesRead,
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

	// Валидация типов
	invalid := make([]string, 0)
	for _, fh := range files {
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

			// Серверная проверка размера через countingReader
			limitedReader := &countingReader{r: io.LimitReader(f, h.cfg.Upload.MaxFileSize+1)}

			result, err := h.cluster.ClusterAdd(ctx, fileHeader.Filename, limitedReader)
			if err != nil {
				mu.Lock()
				errorsList = append(errorsList, fmt.Sprintf("%s: upload failed", fileHeader.Filename))
				mu.Unlock()
				return
			}

			if limitedReader.bytesRead > h.cfg.Upload.MaxFileSize {
				_ = h.cluster.ClusterUnpinAll(ctx, result.CID)
				mu.Lock()
				errorsList = append(errorsList, fmt.Sprintf("%s: file too large", fileHeader.Filename))
				mu.Unlock()
				return
			}

			h.replicateAsync(result.CID)

			mu.Lock()
			results[idx] = Response{
				CID:    result.CID,
				Name:   fileHeader.Filename,
				Size:   limitedReader.bytesRead,
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
	if err := validateCID(cid); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
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

	buffered := bufio.NewReader(reader)
	sniff, err := buffered.Peek(512)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read file"})
		return
	}
	contentType := http.DetectContentType(sniff)

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, buffered)
}

// HandleDelete обрабатывает DELETE /file/{cid}.
func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	cid := strings.TrimPrefix(r.URL.Path, "/file/")
	if err := validateCID(cid); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Добавляем в unpin-список (soft-delete)
	h.unpinStore.Add(cid)

	// Асинхронный unpin на всех нодах — используем detached context
	// (request context отменяется когда клиент закрывает соединение)
	go func() {
		unpinCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		h.cluster.ClusterUnpinAll(unpinCtx, cid)
	}()

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "deleted",
		"cid":    cid,
	})
}
