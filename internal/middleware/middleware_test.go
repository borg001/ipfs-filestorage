package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/auth"
	"github.com/borg001/ipfs-filestorage/internal/config"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := RoleFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(role))
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
	if rr.Body.String() != "" {
		t.Fatalf("expected empty body, got %s", rr.Body.String())
	}
}

func TestAuthMiddleware_NoToken(t *testing.T) {
	h := AuthMiddleware(nil, []string{"secret"})(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Status = %d, want 401", rr.Code)
	}
}

func TestAuthMiddleware_StaticKeyValid(t *testing.T) {
	h := AuthMiddleware(nil, []string{"secret", "key2"})(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "secret")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, want 200", rr.Code)
	}
	if rr.Body.String() != "api-key" {
		t.Errorf("Role = %q, want api-key", rr.Body.String())
	}
}

func TestAuthMiddleware_StaticKeyInvalid(t *testing.T) {
	h := AuthMiddleware(nil, []string{"secret"})(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "wrong")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Status = %d, want 401", rr.Code)
	}
}

func TestAuthMiddleware_BearerAsStaticKey(t *testing.T) {
	h := AuthMiddleware(nil, []string{"mykey"})(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer mykey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, want 200", rr.Code)
	}
}

func TestAuthMiddleware_AuthServiceValid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jwt-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":5,"role":"manager","email":"t@t.com"}`))
	}))
	defer srv.Close()

	cfg := &config.AuthConfig{ServiceURL: srv.URL, CacheTTLMin: 15}
	authClient := auth.NewClient(cfg)

	h := AuthMiddleware(authClient, []string{"fallback"})(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer jwt-token")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, want 200", rr.Code)
	}
	if rr.Body.String() != "manager" {
		t.Errorf("Role = %q, want manager", rr.Body.String())
	}
}

func TestAuthMiddleware_AuthServiceUnreachable_Fallback(t *testing.T) {
	cfg := &config.AuthConfig{ServiceURL: "http://127.0.0.1:1", CacheTTLMin: 15}
	authClient := auth.NewClient(cfg)

	h := AuthMiddleware(authClient, []string{"fallback-key"})(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "fallback-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, want 200 (fallback)", rr.Code)
	}
}

func TestAuthMiddleware_AuthServiceUnreachable_NoFallback(t *testing.T) {
	cfg := &config.AuthConfig{ServiceURL: "http://127.0.0.1:1", CacheTTLMin: 15}
	authClient := auth.NewClient(cfg)

	h := AuthMiddleware(authClient, []string{"fallback-key"})(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "unknown")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Status = %d, want 401", rr.Code)
	}
}

func TestRequireRole(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := RequireRole("manager", "agency")(inner)

	tests := []struct {
		role   string
		status int
	}{
		{"manager", http.StatusOK},
		{"model", http.StatusForbidden},
		{"agency", http.StatusOK},
		{"", http.StatusUnauthorized},
		{"api-key", http.StatusOK}, // api-key — полный доступ
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.role != "" {
				ctx := context.WithValue(req.Context(), ContextKeyRole, tt.role)
				req = req.WithContext(ctx)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tt.status {
				t.Errorf("Role %q: status = %d, want %d", tt.role, rr.Code, tt.status)
			}
		})
	}
}

func TestRoleFromContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), ContextKeyRole, "admin")
	req = req.WithContext(ctx)
	if got := RoleFromContext(req.Context()); got != "admin" {
		t.Errorf("RoleFromContext = %q, want admin", got)
	}
}

func TestAuthMiddleware_AuthServiceForbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	cfg := &config.AuthConfig{ServiceURL: srv.URL, CacheTTLMin: 15}
	authClient := auth.NewClient(cfg)

	h := AuthMiddleware(authClient, []string{"fallback"})(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer banned-user")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("Status = %d, want 403", rr.Code)
	}
}

func TestAuthMiddleware_AuthServiceInvalidToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := &config.AuthConfig{ServiceURL: srv.URL, CacheTTLMin: 15}
	authClient := auth.NewClient(cfg)

	h := AuthMiddleware(authClient, []string{"fallback"})(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer bad-token")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Status = %d, want 401", rr.Code)
	}
}

func TestRequireRole_ModelCannotUpload(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := RequireRole("manager", "agency")(inner)

	req := httptest.NewRequest(http.MethodPost, "/upload", nil)
	ctx := context.WithValue(req.Context(), ContextKeyRole, "model")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("model on upload: status = %d, want 403", rr.Code)
	}
}

func TestAuthMiddleware_CacheHit(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":1,"role":"model","email":"m@m.com"}`))
	}))
	defer srv.Close()

	cfg := &config.AuthConfig{ServiceURL: srv.URL, CacheTTLMin: 15}
	authClient := auth.NewClient(cfg)

	h := AuthMiddleware(authClient, nil)(okHandler())

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer cached")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("Request %d: status = %d, want 200", i, rr.Code)
		}
	}

	if calls != 1 {
		t.Errorf("Expected 1 call to auth-service (rest cached), got %d", calls)
	}
}
