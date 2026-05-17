package static

import (
	"context"
	"testing"
)

func TestStatic_Authorize_ValidKey(t *testing.T) {
	p := New([]string{"secret1", "secret2"})

	result, err := p.Authorize(context.Background(), map[string]string{
		"X-Api-Key": "secret1",
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if result.Role != "api-key" {
		t.Fatalf("expected role api-key, got %s", result.Role)
	}
}

func TestStatic_Authorize_Bearer(t *testing.T) {
	p := New([]string{"mytoken"})

	result, err := p.Authorize(context.Background(), map[string]string{
		"Authorization": "Bearer mytoken",
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if result.Role != "api-key" {
		t.Fatalf("expected role api-key, got %s", result.Role)
	}
}

func TestStatic_Authorize_NoToken(t *testing.T) {
	p := New([]string{"secret"})

	_, err := p.Authorize(context.Background(), map[string]string{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestStatic_Authorize_InvalidToken(t *testing.T) {
	p := New([]string{"secret"})

	_, err := p.Authorize(context.Background(), map[string]string{
		"X-Api-Key": "wrong",
	})
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestStatic_Authorize_EmptyKeys(t *testing.T) {
	p := New([]string{})

	_, err := p.Authorize(context.Background(), map[string]string{
		"X-Api-Key": "anything",
	})
	if err == nil {
		t.Fatal("expected error with empty keys")
	}
}
