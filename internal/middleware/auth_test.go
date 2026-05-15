package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/auth"
	"github.com/borg001/ipfs-filestorage/internal/config"
)

func TestAuthMW_NoToken(t *testing.T) {
	h := AuthMiddleware(nil, []string{"secret"})(roleEchoHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Status = %d, want 401", rr.Code)
	}
}

func TestAuthMW_StaticKeyValid(t *testing.T) {
	h := AuthMiddleware(nil, []string{"secret", "key2"})(roleEchoHandler())
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

func TestAuthMW_StaticKeyInvalid(t *testing.T) {
	h := AuthMiddleware(nil, []string{"secret"})(roleEchoHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "wrong")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Status = %d, want 401", rr.Code)
	}
}

func TestAuthMW_BearerAsStaticKey(t *testing.T) {
	h := AuthMiddleware(nil, []string{"mykey"})(roleEchoHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer mykey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, want 200", rr.Code)
	}
}

func TestAuthMW_AuthServiceValid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jwt-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": 5, "role": "manager", "email": "t@t.com",
		})
	}))
	defer srv.Close()

	cfg := &config.AuthConfig{ServiceURL: srv.URL, CacheTTLMin: 15}
	authClient := auth.NewClient(cfg)

	h := AuthMiddleware(authClient, []string{"fallback"})(roleEchoHandler())
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

func TestAuthMW_AuthServiceUnreachable_Fallback(t *testing.T) {
	cfg := &config.AuthConfig{ServiceURL: "http://127.0.0.1:1", CacheTTLMin: 15}
	authClient := auth.NewClient(cfg)

	h := AuthMiddleware(authClient, []string{"fallback-key"})(roleEchoHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "fallback-key")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, want 200 (fallback)", rr.Code)
	}
}

func TestAuthMW_AuthServiceUnreachable_NoFallback(t *testing.T) {
	cfg := &config.AuthConfig{ServiceURL: "http://127.0.0.1:1", CacheTTLMin: 15}
	authClient := auth.NewClient(cfg)

	h := AuthMiddleware(authClient, []string{"fallback-key"})(roleEchoHandler())
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
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.role != "" {
				ctx := contextWithRole(req.Context(), tt.role)
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
	ctx := contextWithRole(req.Context(), "admin")
	req = req.WithContext(ctx)
	if got := RoleFromContext(req.Context()); got != "admin" {
		t.Errorf("RoleFromContext = %q, want admin", got)
	}
}

func roleEchoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := RoleFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(role))
	})
}
