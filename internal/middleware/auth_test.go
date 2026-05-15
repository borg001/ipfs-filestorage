package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/auth"
	"github.com/borg001/ipfs-filestorage/internal/config"
)

func TestAuthMW_NoToken(t *testing.T) {
	h := AuthMiddleware(nil, []string{"secret"})(authEchoHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Status = %d, want 401", rr.Code)
	}
}

func TestAuthMW_StaticKeyValid(t *testing.T) {
	h := AuthMiddleware(nil, []string{"secret", "key2"})(authEchoHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "secret")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, want 200", rr.Code)
	}
}

func TestAuthMW_StaticKeyInvalid(t *testing.T) {
	h := AuthMiddleware(nil, []string{"secret"})(authEchoHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "wrong")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Status = %d, want 401", rr.Code)
	}
}

func TestAuthMW_BearerAsStaticKey(t *testing.T) {
	h := AuthMiddleware(nil, []string{"mykey"})(authEchoHandler())
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

	h := AuthMiddleware(authClient, []string{"fallback"})(authEchoHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer jwt-token")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Status = %d, want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "5" {
		t.Errorf("UserID = %q, want 5", got)
	}
}

func TestAuthMW_AuthServiceUnreachable_Fallback(t *testing.T) {
	cfg := &config.AuthConfig{ServiceURL: "http://127.0.0.1:1", CacheTTLMin: 15}
	authClient := auth.NewClient(cfg)

	h := AuthMiddleware(authClient, []string{"fallback-key"})(authEchoHandler())
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

	h := AuthMiddleware(authClient, []string{"fallback-key"})(authEchoHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "unknown")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("Status = %d, want 401", rr.Code)
	}
}

func TestUserIDFromContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := contextWithUserID(req.Context(), 42)
	req = req.WithContext(ctx)
	if got := UserIDFromContext(req.Context()); got != 42 {
		t.Errorf("UserIDFromContext = %d, want 42", got)
	}
}

func authEchoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := UserIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		if userID > 0 {
			fmt.Fprintf(w, "%d", userID)
		} else {
			w.Write([]byte("ok"))
		}
	})
}
