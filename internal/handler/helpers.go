package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

// writeJSON пишет JSON-ответ
func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// validateFile проверяет расширение и MIME-тип
func validateFile(filename string, contentType string, cfg *config.Config) error {
	ext := strings.TrimPrefix(filepath.Ext(filename), ".")
	ext = strings.ToLower(ext)
	allowedExt := false
	for _, e := range cfg.AllowedExtensions {
		if e == ext {
			allowedExt = true
			break
		}
	}
	if !allowedExt {
		return fmt.Errorf("Invalid file type")
	}

	// Если Content-Type пустой — пробуем определить по расширению
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = mime.TypeByExtension(filepath.Ext(filename))
	}

	if _, ok := cfg.AllowedMimeTypes[contentType]; !ok {
		// Fallback: если Content-Type не найден — проверяем по расширению
		ct := mime.TypeByExtension("." + ext)
		if ct != "" {
			if _, ok := cfg.AllowedMimeTypes[ct]; ok {
				return nil
			}
		}
		return fmt.Errorf("Invalid MIME type: %s", contentType)
	}
	return nil
}
