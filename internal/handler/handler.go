package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/bundle"
	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/imageproc"
	"github.com/borg001/ipfs-filestorage/internal/ipfs"
	"github.com/borg001/ipfs-filestorage/internal/store"
	"github.com/borg001/ipfs-filestorage/internal/unpin"
)

// Response — стандартная структура ответа API.
type Response struct {
	CID      string                  `json:"cid"`
	Name     string                  `json:"name"`
	Size     int64                   `json:"size"`
	Pinned   bool                    `json:"pinned"`
	Type     string                  `json:"type,omitempty"`
	Original *bundle.Asset           `json:"original,omitempty"`
	Variants map[string]bundle.Asset `json:"variants,omitempty"`
}

// Handler содержит HTTP-хендлеры сервиса.
type Handler struct {
	cfg            *config.Config
	cluster        ipfs.Clusterer
	unpinStore     *store.UnpinStore
	unpinWorker    *unpin.Worker
	imageProcessor *imageproc.Processor
	mediaAccess    *mediaAccessResolver
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
		cfg:            cfg,
		cluster:        cluster,
		unpinStore:     unpinStore,
		imageProcessor: imageproc.NewProcessor(cfg.Image, cfg.Video.FFmpegPath),
		mediaAccess:    newMediaAccessResolver(cfg.MediaAccess),
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

func responseFromManifest(m bundle.Manifest, pinned bool) Response {
	return Response{
		CID:      m.CID,
		Name:     m.Name,
		Size:     m.Size,
		Pinned:   pinned,
		Type:     m.Type,
		Original: &m.Original,
		Variants: m.Variants,
	}
}

func formatFromFilename(filename string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
	if ext == "jpg" {
		return "jpeg"
	}
	if ext == "" {
		return "bin"
	}
	return ext
}

func (h *Handler) buildFileBundle(ctx context.Context, filename string, data []byte, contentType string) (bundle.Manifest, error) {
	manifest := bundle.NewFileManifest(filename, contentType, formatFromFilename(filename), int64(len(data)))
	entries := map[string][]byte{
		bundle.OriginalFilename: data,
	}

	imageResult, err := h.imageProcessor.Process(ctx, data, contentType)
	if err != nil {
		return bundle.Manifest{}, err
	}
	if imageResult.IsImage {
		manifest.Type = "image"
		manifest.Original.Format = imageResult.Format
		manifest.Original.Width = imageResult.Width
		manifest.Original.Height = imageResult.Height
		if len(imageResult.Variants) > 0 {
			manifest.Variants = make(map[string]bundle.Asset, len(imageResult.Variants))
			for _, variant := range imageResult.Variants {
				entries[variant.Filename] = variant.Data
				manifest.Variants[variant.Key] = bundle.Asset{
					BundlePath:  variant.Filename,
					Format:      variant.Format,
					ContentType: variant.ContentType,
					Width:       variant.Width,
					Height:      variant.Height,
					Size:        int64(len(variant.Data)),
				}
			}
		}
	}

	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return bundle.Manifest{}, err
	}
	entries[bundle.ManifestFilename] = manifestJSON
	result, err := h.cluster.ClusterAddDir(ctx, entries)
	if err != nil {
		return bundle.Manifest{}, err
	}
	manifest.Finalize(result.CID)
	return manifest, nil
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
		writeUploadError(w, r, http.StatusBadRequest, "upload_form_invalid", nil)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeUploadError(w, r, http.StatusBadRequest, "upload_missing_file", nil)
		return
	}
	defer file.Close()

	// Валидация типа
	if err := validateFile(header.Filename, header.Header.Get("Content-Type"), h.cfg); err != nil {
		writeUploadError(w, r, http.StatusBadRequest, "unsupported_file_type", map[string]any{
			"allowed_extensions": h.cfg.Upload.AllowedExtensions,
		})
		return
	}

	limitedReader := &countingReader{r: io.LimitReader(file, h.cfg.Upload.MaxFileSize+1)}
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		writeUploadError(w, r, http.StatusInternalServerError, "upload_failed", nil)
		return
	}

	// Серверная проверка: если прочитано больше MaxFileSize — файл слишком большой.
	// Это достоверная проверка, основанная на реальных байтах, а не на заголовке.
	if limitedReader.bytesRead > h.cfg.Upload.MaxFileSize {
		writeUploadError(w, r, http.StatusRequestEntityTooLarge, "file_too_large", map[string]any{
			"max_bytes": h.cfg.Upload.MaxFileSize,
		})
		return
	}

	ctx := r.Context()
	contentType := http.DetectContentType(data)
	manifest, err := h.buildFileBundle(ctx, header.Filename, data, contentType)
	if err != nil {
		writeUploadError(w, r, http.StatusInternalServerError, "upload_failed", nil)
		return
	}

	h.replicateAsync(manifest.CID)
	writeJSON(w, http.StatusOK, responseFromManifest(manifest, true))
}

// HandleUploadMultiple обрабатывает POST /upload-multiple.
func (h *Handler) HandleUploadMultiple(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeUploadError(w, r, http.StatusBadRequest, "upload_form_invalid", nil)
		return
	}
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		writeUploadError(w, r, http.StatusBadRequest, "upload_missing_file", nil)
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
		writeUploadError(w, r, http.StatusBadRequest, "unsupported_file_type", map[string]any{
			"allowed_extensions": h.cfg.Upload.AllowedExtensions,
			"invalid_files":      invalid,
		})
		return
	}

	// Параллельная загрузка
	ctx := r.Context()
	results := make([]Response, len(files))
	var wg sync.WaitGroup
	var mu sync.Mutex
	type uploadFailure struct {
		code     string
		filename string
	}
	failures := make([]uploadFailure, 0)

	for i, fh := range files {
		wg.Add(1)
		go func(idx int, fileHeader *multipart.FileHeader) {
			defer wg.Done()
			f, err := fileHeader.Open()
			if err != nil {
				mu.Lock()
				failures = append(failures, uploadFailure{code: "upload_failed", filename: fileHeader.Filename})
				mu.Unlock()
				return
			}
			defer f.Close()

			limitedReader := &countingReader{r: io.LimitReader(f, h.cfg.Upload.MaxFileSize+1)}
			data, err := io.ReadAll(limitedReader)
			if err != nil {
				mu.Lock()
				failures = append(failures, uploadFailure{code: "upload_failed", filename: fileHeader.Filename})
				mu.Unlock()
				return
			}

			if limitedReader.bytesRead > h.cfg.Upload.MaxFileSize {
				mu.Lock()
				failures = append(failures, uploadFailure{code: "file_too_large", filename: fileHeader.Filename})
				mu.Unlock()
				return
			}

			contentType := http.DetectContentType(data)
			manifest, err := h.buildFileBundle(ctx, fileHeader.Filename, data, contentType)
			if err != nil {
				mu.Lock()
				failures = append(failures, uploadFailure{code: "upload_failed", filename: fileHeader.Filename})
				mu.Unlock()
				return
			}
			h.replicateAsync(manifest.CID)

			mu.Lock()
			results[idx] = responseFromManifest(manifest, true)
			mu.Unlock()
		}(i, fh)
	}
	wg.Wait()

	if len(failures) > 0 {
		code := "upload_failed"
		status := http.StatusInternalServerError
		failedFiles := make([]string, 0, len(failures))
		for _, failure := range failures {
			failedFiles = append(failedFiles, failure.filename)
			if failure.code == "file_too_large" {
				code = failure.code
				status = http.StatusRequestEntityTooLarge
			}
		}
		details := map[string]any{"failed_files": failedFiles}
		if code == "file_too_large" {
			details["max_bytes"] = h.cfg.Upload.MaxFileSize
		}
		writeUploadError(w, r, status, code, details)
		return
	}

	writeJSON(w, http.StatusOK, results)
}

func (h *Handler) readManifest(ctx context.Context, cid string) (bundle.Manifest, error) {
	reader, err := h.cluster.ClusterTryFetchPath(ctx, cid, bundle.ManifestFilename)
	if err != nil {
		return bundle.Manifest{}, err
	}
	defer reader.Close()
	var manifest bundle.Manifest
	if err := json.NewDecoder(reader).Decode(&manifest); err != nil {
		return bundle.Manifest{}, err
	}
	manifest.Finalize(cid)
	return manifest, nil
}

// HandleFile serves legacy CID-addressed file URLs. New browser-facing callers
// use HandleFileLink so a protected asset CID never reaches the DOM.
func (h *Handler) HandleFile(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/file/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "CID required"})
		return
	}
	cid := parts[0]
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

	decision, err := h.resolveMediaDelivery(r, cid)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Media access service unavailable"})
		return
	}
	h.serveFile(w, r, cid, parts[1:], decision)
}

// HandleFileLink serves GET /file/link/{media_link}/{size}. The link ID is
// opaque to the browser; file storage resolves its asset and policy internally.
func (h *Handler) HandleFileLink(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/file/link/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Media link required"})
		return
	}
	decision, err := h.resolveMediaDeliveryLink(r, parts[0])
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Media access service unavailable"})
		return
	}
	if err := validateCID(decision.SourceCID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "File not found"})
		return
	}
	if h.unpinStore.Has(decision.SourceCID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "File not found", "message": "File was deleted"})
		return
	}
	h.serveFile(w, r, decision.SourceCID, parts[1:], decision)
}

func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request, cid string, parts []string, decision mediaDeliveryDecision) {
	ctx := r.Context()

	manifest, err := h.readManifest(ctx, cid)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "File not found"})
		return
	}

	if len(parts) == 1 && parts[0] == "bundle" {
		if decision.Managed && decision.Mode != mediaDeliveryOriginal {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Protected media bundle is unavailable"})
			return
		}
		w.Header().Set("Cache-Control", protectedMediaCacheControl(decision.Managed))
		writeJSON(w, http.StatusOK, manifest)
		return
	}

	bundlePath := manifest.Original.BundlePath
	contentType := manifest.Original.ContentType
	variantKey := ""
	if len(parts) == 1 && parts[0] != "" {
		variantKey = parts[0]
	}
	if decision.Managed && decision.Mode != mediaDeliveryOriginal {
		variantKey = string(decision.Mode)
	}
	if variantKey != "" {
		variant, ok := manifest.Variants[variantKey]
		if !ok {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Protected media variant is unavailable"})
			return
		}
		bundlePath = variant.BundlePath
		contentType = variant.ContentType
	} else if len(parts) > 1 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "File not found"})
		return
	}

	reader, err := h.cluster.ClusterTryFetchPath(ctx, cid, bundlePath)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "File not found"})
		return
	}
	defer reader.Close()

	buffered := bufio.NewReader(reader)
	if contentType == "" {
		sniff, err := buffered.Peek(512)
		if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read file"})
			return
		}
		contentType = http.DetectContentType(sniff)
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", protectedMediaCacheControl(decision.Managed))
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
	cidsToUnpin := []string{cid}
	if group := h.unpinStore.GetGroup(cid); len(group) > 0 {
		h.unpinStore.AddGroup(cid, group)
		cidsToUnpin = group
	} else {
		h.unpinStore.Add(cid)
	}

	// Асинхронный unpin на всех нодах — используем detached context
	// (request context отменяется когда клиент закрывает соединение)
	go func() {
		unpinCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, unpinCID := range cidsToUnpin {
			h.cluster.ClusterUnpinAll(unpinCtx, unpinCID)
		}
	}()

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "deleted",
		"cid":    cid,
	})
}
