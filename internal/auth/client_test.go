package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

func TestValidate_ValidToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/me" {
			t.Errorf("Expected path /auth/me, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer valid-token" {
			t.Errorf("Expected Bearer valid-token, got %s", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":            42,
			"email":        "user@example.com",
			"phone":        "+79001234567",
			"role":         "manager",
			"verify_status": "verified",
		})
	}))
	defer srv.Close()

	cfg := &config.AuthConfig{ServiceURL: srv.URL, CacheTTLMin: 15}
	client := NewClient(cfg)

	user, err := client.Validate(context.Background(), "valid-token")
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	if user.UserID != 42 {
		t.Errorf("UserID = %d, want 42", user.UserID)
	}
	if user.Role != "manager" {
		t.Errorf("Role = %q, want manager", user.Role)
	}
}

func TestValidate_InvalidToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := &config.AuthConfig{ServiceURL: srv.URL, CacheTTLMin: 15}
	client := NewClient(cfg)

	_, err := client.Validate(context.Background(), "bad-token")
	if err == nil {
		t.Fatal("Expected error for invalid token")
	}
	if err != ErrInvalidToken {
		t.Errorf("Error = %v, want ErrInvalidToken", err)
	}
}

func TestValidate_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	cfg := &config.AuthConfig{ServiceURL: srv.URL, CacheTTLMin: 15}
	client := NewClient(cfg)

	_, err := client.Validate(context.Background(), "banned-token")
	if err == nil {
		t.Fatal("Expected error for forbidden")
	}
	if err != ErrForbidden {
		t.Errorf("Error = %v, want ErrForbidden", err)
	}
}

func TestValidate_CacheHit(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": 1, "role": "model", "verify_status": "verified",
		})
	}))
	defer srv.Close()

	cfg := &config.AuthConfig{ServiceURL: srv.URL, CacheTTLMin: 15}
	client := NewClient(cfg)

	_, err := client.Validate(context.Background(), "cached-token")
	if err != nil {
		t.Fatal(err)
	}
	if callCount != 1 {
		t.Fatalf("Expected 1 call, got %d", callCount)
	}

	_, err = client.Validate(context.Background(), "cached-token")
	if err != nil {
		t.Fatal(err)
	}
	if callCount != 1 {
		t.Errorf("Expected 1 call (cache hit), got %d", callCount)
	}
}

func TestValidate_ExpiredCacheRevalidates(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": 2, "role": "model", "email": "re@test.com",
		})
	}))
	defer srv.Close()

	cfg := &config.AuthConfig{ServiceURL: srv.URL, CacheTTLMin: 15}
	client := NewClient(cfg)
	client.cacheTTL = 0

	_, _ = client.Validate(context.Background(), "expiring-token")
	_, _ = client.Validate(context.Background(), "expiring-token")

	if callCount != 2 {
		t.Errorf("Expected 2 calls (expired cache), got %d", callCount)
	}
}

func TestValidate_Unreachable(t *testing.T) {
	cfg := &config.AuthConfig{ServiceURL: "http://127.0.0.1:1", CacheTTLMin: 15}
	client := NewClient(cfg)

	_, err := client.Validate(context.Background(), "any-token")
	if err == nil {
		t.Fatal("Expected error for unreachable")
	}
	if !IsUnreachable(err) {
		t.Errorf("Expected IsUnreachable, got: %v", err)
	}
}

func TestNewClient_NilConfig(t *testing.T) {
	if NewClient(nil) != nil {
		t.Error("Expected nil for nil config")
	}
}

func TestNewClient_EmptyURL(t *testing.T) {
	if NewClient(&config.AuthConfig{ServiceURL: ""}) != nil {
		t.Error("Expected nil for empty URL")
	}
}

func TestInvalidate(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		json.NewEncoder(w).Encode(map[string]interface{}{"id": 1, "role": "model"})
	}))
	defer srv.Close()

	cfg := &config.AuthConfig{ServiceURL: srv.URL, CacheTTLMin: 15}
	client := NewClient(cfg)

	_, _ = client.Validate(context.Background(), "inv-token")
	client.Invalidate("inv-token")
	_, _ = client.Validate(context.Background(), "inv-token")

	if callCount != 2 {
		t.Errorf("Expected 2 calls after invalidate, got %d", callCount)
	}
}

func TestValidate_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()

	cfg := &config.AuthConfig{ServiceURL: srv.URL, CacheTTLMin: 15}
	client := NewClient(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := client.Validate(ctx, "slow-token")
	if err == nil {
		t.Fatal("Expected error for timeout")
	}
	if !IsUnreachable(err) {
		t.Errorf("Expected IsUnreachable, got: %v", err)
	}
}

func TestValidate_NilClient(t *testing.T) {
	var client *Client
	_, err := client.Validate(context.Background(), "any")
	if err == nil {
		t.Fatal("Expected error for nil client")
	}
}
