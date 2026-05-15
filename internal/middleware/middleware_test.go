package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestPanicRecovery(t *testing.T) {
	handler := PanicRecovery()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "internal server error") {
		t.Fatalf("expected error body, got %s", rr.Body.String())
	}
}

func TestCORS(t *testing.T) {
	handler := CORS([]string{"http://test.com"}, []string{"Content-Type", "X-API-Key"})(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "http://test.com" {
		t.Fatalf("expected origin \"http://test.com\", got %s", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type,X-API-Key" {
		t.Fatalf("expected headers Content-Type,X-API-Key, got %s", got)
	}
}

func TestCORSPreflight(t *testing.T) {
	handler := CORS([]string{"*"}, []string{"Content-Type"})(okHandler())
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for OPTIONS, got %d", rr.Code)
	}
}

func TestChain(t *testing.T) {
	handler := Chain(okHandler(), PanicRecovery(), CORS([]string{"*"}, []string{"Content-Type"}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}
