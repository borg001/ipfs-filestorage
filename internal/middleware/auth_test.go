package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/auth/lua"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestAuthMiddleware_StaticKey(t *testing.T) {
	handler := AuthMiddleware([]string{"secret"}, nil)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "secret")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestAuthMiddleware_BearerToken(t *testing.T) {
	handler := AuthMiddleware([]string{"mytoken"}, nil)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer mytoken")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestAuthMiddleware_NoToken(t *testing.T) {
	handler := AuthMiddleware([]string{"secret"}, nil)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAuthMiddleware_InvalidKey(t *testing.T) {
	handler := AuthMiddleware([]string{"secret"}, nil)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "wrong")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAuthMiddleware_LuaFallback(t *testing.T) {
	script := `
function authorize(req)
  return req.headers["X-Custom"] == "yes"
end
`
	luaProvider := lua.NewProvider(script, 3000, nil)
	handler := AuthMiddleware([]string{"static-key"}, luaProvider)(okHandler())

	// Static key works
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "static-key")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for static key, got %d", rr.Code)
	}

	// Lua fallback works
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("X-Custom", "yes")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200 for lua auth, got %d", rr2.Code)
	}

	// Both fail
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when no auth matches, got %d", rr3.Code)
	}
}

func TestAuthMiddleware_UserIDInContext(t *testing.T) {
	var userID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID = UserIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := AuthMiddleware([]string{"secret"}, nil)(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "secret")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if userID != "api-key" {
		t.Fatalf("expected user_id 'api-key', got %s", userID)
	}
}
