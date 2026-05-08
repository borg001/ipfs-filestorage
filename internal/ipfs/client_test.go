package ipfs_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/ipfs"
)

func getTestClient(t *testing.T) *ipfs.Client {
	t.Helper()
	url := os.Getenv("TEST_IPFS_URL")
	if url == "" {
		t.Skip("TEST_IPFS_URL not set, skipping integration test")
	}
	c, err := ipfs.New(url)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	return c
}

func TestClient_Add(t *testing.T) {
	c := getTestClient(t)
	ctx := context.Background()

	content := []byte("hello ipfs from test")
	result, err := c.Add(ctx, "test.txt", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if result.CID == "" {
		t.Fatal("expected non-empty CID")
	}
	t.Logf("Added CID: %s", result.CID)
}

func TestClient_Add_Pin_Cat_Unpin(t *testing.T) {
	c := getTestClient(t)
	ctx := context.Background()

	// 1. Add
	content := []byte("hello ipfs integration test")
	result, err := c.Add(ctx, "integration-test.txt", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	t.Logf("CID: %s", result.CID)

	// 2. Pin
	err = c.Pin(ctx, result.CID, 3, time.Second)
	if err != nil {
		t.Fatalf("Pin failed: %v", err)
	}

	// 3. Cat — читаем содержимое
	r, err := c.Cat(ctx, result.CID)
	if err != nil {
		t.Fatalf("Cat failed: %v", err)
	}
	defer r.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("content mismatch: got %q, want %q", got, content)
	}

	// 4. Unpin
	err = c.Unpin(ctx, result.CID)
	if err != nil {
		t.Fatalf("Unpin failed: %v", err)
	}
}

func TestClient_Stat(t *testing.T) {
	c := getTestClient(t)
	ctx := context.Background()

	content := []byte("stat test content")
	result, err := c.Add(ctx, "stat-test.txt", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	stat, err := c.Stat(ctx, result.CID)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if stat.CID != result.CID {
		t.Fatalf("CID mismatch: got %s, want %s", stat.CID, result.CID)
	}
	t.Logf("Stat: CID=%s Size=%d", stat.CID, stat.Size)
}

func TestClient_Pin_Idempotent(t *testing.T) {
	c := getTestClient(t)
	ctx := context.Background()

	content := []byte("idempotent pin test")
	result, err := c.Add(ctx, "idempotent.txt", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Pin дважды — не должно быть ошибки
	if err := c.Pin(ctx, result.CID, 3, time.Second); err != nil {
		t.Fatalf("first Pin failed: %v", err)
	}
	if err := c.Pin(ctx, result.CID, 3, time.Second); err != nil {
		t.Fatalf("second Pin (idempotent) failed: %v", err)
	}

	// Cleanup
	_ = c.Unpin(ctx, result.CID)
}
