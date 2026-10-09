package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateCIDTakesNothingAfterTheCID(t *testing.T) {
	good := "QmTaUGmo3MkNcRHsphuj5D9zuEfdRTUVX6GmyXbsQdygkT"
	if err := validateCID(good); err != nil {
		t.Fatalf("a real CID was refused: %v", err)
	}
	for _, bad := range []string{good + "/original", good + "/../x", good[:40] + "/orig", "Qm" + "000000000000000000000000000000000000000000.o", "bafy..", ""} {
		if validateCID(bad) == nil {
			t.Fatalf("%q passed as a CID", bad)
		}
	}
}

func TestStoredFilesNeverRunAsPages(t *testing.T) {
	for contentType, inline := range map[string]bool{
		"image/jpeg": true, "video/mp4": true, "image/webp": true,
		"text/html": false, "image/svg+xml": false, "application/pdf": false, "text/html; charset=utf-8": false,
	} {
		w := httptest.NewRecorder()
		w.Header().Set("Content-Type", contentType)
		setUserFileHeaders(w, contentType)
		if w.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("%s: no CSP", contentType)
		}
		if got := w.Header().Get("Content-Disposition") == ""; got != inline {
			t.Fatalf("%s: inline=%v, want %v", contentType, got, inline)
		}
	}
}

func TestDeletingAFileNeedsTheServerKey(t *testing.T) {
	h := &Handler{}
	w := httptest.NewRecorder()
	h.HandleDelete(w, httptest.NewRequest(http.MethodDelete, "/file/QmTaUGmo3MkNcRHsphuj5D9zuEfdRTUVX6GmyXbsQdygkT", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("a signed-in session deleted a file: %d", w.Code)
	}
}
