package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

func TestHandleFile_DetectsContentType(t *testing.T) {
	h := setupTestHandler(&config.Config{})
	cid := "Qm11111111111111111111111111111111111111111111"
	cluster := h.cluster.(*mockCluster)
	cluster.files[cid] = []byte("hello from storage")

	req := httptest.NewRequest(http.MethodGet, "/file/"+cid, nil)
	w := httptest.NewRecorder()
	h.HandleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/plain; charset=utf-8", got)
	}
}
