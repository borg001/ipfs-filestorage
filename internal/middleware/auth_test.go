package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/auth/lua"
)

func testOkHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestAuthMiddleware_StaticKey(t *testing.T) {
	handler := AuthMiddleware([]string{"secret"}, nil)(testOkHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "secret")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestAuthMiddleware_BearerToken(t *testing.T) {
	handler := AuthMiddleware([]string{"mytoken"}, nil)(testOkHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer mytoken")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestAuthMiddleware_QueryToken(t *testing.T) {
	handler := AuthMiddleware([]string{"mytoken"}, nil)(testOkHandler())

	req := httptest.NewRequest(http.MethodGet, "/file/Qm123?token=mytoken", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for query token, got %d", rr.Code)
	}
}

func TestAuthMiddleware_QueryAccessToken(t *testing.T) {
	handler := AuthMiddleware([]string{"mytoken"}, nil)(testOkHandler())

	req := httptest.NewRequest(http.MethodGet, "/file/Qm123?access_token=mytoken", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for query access_token, got %d", rr.Code)
	}
}

func TestAuthMiddleware_NoToken(t *testing.T) {
	handler := AuthMiddleware([]string{"secret"}, nil)(testOkHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAuthMiddleware_InvalidKey(t *testing.T) {
	handler := AuthMiddleware([]string{"secret"}, nil)(testOkHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "wrong")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestAuthMiddleware_GenericErrorMessage(t *testing.T) {
	// Lua script that errors — ensure client gets generic message, not internals
	script := `function authorize(req) error("boom") end`
	luaProvider := lua.NewProvider(script, 3000, 0, nil)
	handler := AuthMiddleware([]string{"secret"}, luaProvider)(testOkHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Custom", "value")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	body := rr.Body.String()
	if body == "" {
		t.Fatal("expected error body")
	}
	// Should NOT contain "boom" or internal Lua error details
	if contains(body, "boom") {
		t.Fatalf("error message should not expose internal details, got: %s", body)
	}
}

func TestAuthMiddleware_LuaFallback(t *testing.T) {
	script := `
function authorize(req)
  return req.headers["X-Custom"] == "yes"
end
`
	luaProvider := lua.NewProvider(script, 3000, 0, nil)
	handler := AuthMiddleware([]string{"static-key"}, luaProvider)(testOkHandler())

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

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
