package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

func TestHandleFile_DetectsContentType(t *testing.T) {
	h := setupTestHandler(&config.Config{})
	manifest, err := h.buildFileBundle(context.Background(), "hello.txt", []byte("hello from storage"), "text/plain; charset=utf-8")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/file/"+manifest.CID, nil)
	w := httptest.NewRecorder()
	h.HandleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/plain; charset=utf-8", got)
	}
}
