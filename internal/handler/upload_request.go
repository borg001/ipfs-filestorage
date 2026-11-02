package handler

import (
	"errors"
	"io"
	"net/http"
)

// parseUploadForm applies a total body limit before parsing or spilling to disk.
// Content-Length is only a fast rejection; MaxBytesReader also bounds chunked bodies.
func (h *Handler) parseUploadForm(w http.ResponseWriter, r *http.Request, routeLimit int64) bool {
	limit := routeLimit
	if h.cfg != nil && h.cfg.Upload.MaxRequestSize > 0 && (limit <= 0 || h.cfg.Upload.MaxRequestSize < limit) {
		limit = h.cfg.Upload.MaxRequestSize
	}
	tooLarge := func() {
		writeUploadError(w, r, http.StatusRequestEntityTooLarge, "upload_request_too_large", map[string]any{
			"max_request_bytes": limit,
		})
	}
	if limit > 0 {
		if r.ContentLength > limit {
			tooLarge()
			return false
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
	err := r.ParseMultipartForm(32 << 20)
	if err == nil && limit > 0 {
		// Multipart parsing may stop at the final boundary. Count an epilogue
		// too, so an unknown-length request cannot hide bytes after that boundary.
		_, err = io.Copy(io.Discard, r.Body)
	}
	if err != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			tooLarge()
		} else {
			writeUploadError(w, r, http.StatusBadRequest, "upload_form_invalid", nil)
		}
		return false
	}
	return true
}
