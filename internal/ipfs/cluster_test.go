package ipfs

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

func getTestURLs() []string {
	url := os.Getenv("TEST_IPFS_URL")
	if url == "" {
		url = "http://127.0.0.1:5001"
	}
	return []string{url, url}
}

func TestCluster_Add_Replicate_Cat_Unpin(t *testing.T) {
	urls := getTestURLs()
	cm := NewCluster(urls)
	ctx := context.Background()
	data := []byte("cluster manager test data hello")
	result, err := cm.ClusterAdd(ctx, "test.txt", bytes.NewReader(data))
	if err != nil {
		t.Skip("IPFS node not available:", err)
	}
	t.Logf("ClusterAdd CID: %s", result.CID)

	// Replicate (Fetch + Pin)
	t.Log("Running ClusterReplicate...")
	if err := cm.ClusterReplicate(ctx, result.CID, 3, 100*time.Millisecond); err != nil {
		t.Fatalf("ClusterReplicate failed: %v", err)
	}
	t.Log("ClusterReplicate done")

	r, err := cm.ClusterTryFetch(ctx, result.CID)
	if err != nil {
		t.Fatalf("ClusterTryFetch failed: %v", err)
	}
	r.Close()
	t.Log("ClusterTryFetch ok")

	info, err := cm.ClusterStat(ctx, result.CID)
	if err != nil {
		t.Fatalf("ClusterStat failed: %v", err)
	}
	if info.CID != result.CID {
		t.Fatalf("Stat mismatch: %#v", info)
	}
	t.Logf("Stat: CID=%s Size=%d", info.CID, info.Size)

	_ = cm.ClusterUnpinAll(ctx, result.CID)
}

func TestCluster_PinAllExcept(t *testing.T) {
	urls := getTestURLs()
	cm := NewCluster(urls)
	ctx := context.Background()
	data := []byte("pin all except test")
	result, err := cm.ClusterAdd(ctx, "except.txt", bytes.NewReader(data))
	if err != nil {
		t.Skip("IPFS node not available:", err)
	}
	firstURL := urls[0]
	t.Log("Running ClusterPinAllExcept skipping first node...")
	if err := cm.ClusterPinAllExcept(ctx, result.CID, firstURL, 3, 100*time.Millisecond); err != nil {
		t.Fatalf("ClusterPinAllExcept failed: %v", err)
	}
	t.Log("ClusterPinAllExcept done")
}

func TestCluster_NoNodes(t *testing.T) {
	cm := &ClusterManager{}
	ctx := context.Background()
	if _, err := cm.ClusterAdd(ctx, "f", nil); err == nil {
		t.Fatal("expected error for empty cluster")
	}
	if _, err := cm.ClusterCat(ctx, "cid"); err == nil {
		t.Fatal("expected error for empty cluster")
	}
	if _, err := cm.ClusterStat(ctx, "cid"); err == nil {
		t	t.Fatal("expected error for empty cluster")
	}
	if cm.ClusterIsPinnedAll(ctx, "cid") {
		t.Fatal("expected false for empty cluster")
	}
	if _, err := cm.ClusterTryFetch(ctx, "cid"); err == nil {
		t.Fatal("expected error for empty cluster")
	}
}
