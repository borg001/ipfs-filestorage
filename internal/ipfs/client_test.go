package ipfs

import (
	"context"
	"os"
	"strings"
	"testing"
)

func ipfsURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_IPFS_URL")
	if url == "" {
		t.Skip("TEST_IPFS_URL not set, skipping integration test")
	}
	return url
}

func TestClient_Add_Pin_Cat_Unpin(t *testing.T) {
	url := ipfsURL(t)

	client, err := New(url)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	content := "hello ipfs integration test"

	// Add
	result, err := client.Add(ctx, "test.txt", strings.NewReader(content))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if result.CID == "" {
		t.Fatal("Add: empty CID")
	}
	t.Logf("Added CID: %s", result.CID)

	// Pin
	err = client.Pin(ctx, result.CID, 3, 0)
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}

	// Cat
	rc, err := client.Cat(ctx, result.CID)
	if err != nil {
		t.Fatalf("Cat: %v", err)
	}
	defer rc.Close()

	// Unpin
	err = client.Unpin(ctx, result.CID)
	if err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	t.Logf("Unpin OK: %s", result.CID)
}

func TestClient_Stat(t *testing.T) {
	url := ipfsURL(t)

	client, err := New(url)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()

	// Add сначала чтобы был CID
	result, err := client.Add(ctx, "stat-test.txt", strings.NewReader("stat test content"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	stat, err := client.Stat(ctx, result.CID)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if stat.CID != result.CID {
		t.Errorf("Stat CID mismatch: got %s, want %s", stat.CID, result.CID)
	}
	t.Logf("Stat: CID=%s Size=%d Type=%s", stat.CID, stat.Size, stat.Type)
}
