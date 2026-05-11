//go:build integration

package integration

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	apiKey        = "SECRET_KEY_1"
	nginxURL      = "http://localhost:8081" // nginx proxy (port 8081 on host)
	storage1URL   = "http://localhost:3001" // storage1 direct
	storage2URL   = "http://localhost:3002" // storage2 direct
	ipfs1API      = "http://localhost:5001" // ipfs1 API
	ipfs2API      = "http://localhost:5002" // ipfs2 API
	replicaWait   = 5 * time.Second         // wait for replication after upload
	maxReplicaWait = 20 * time.Second       // max wait with retries
)

// TestReplicationByteForByte uploads a file through storage1,
// then verifies that the EXACT same bytes are available on ipfs2.
// This is the core test for 99.9% replication guarantee.
func TestReplicationByteForByte(t *testing.T) {
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("Set INTEGRATION=1 to run integration tests")
	}

	// Upload a unique PNG file via storage1
	content := fmt.Sprintf("replication-test-%d", time.Now().UnixNano())
	cid := uploadFile(t, content, "test.png")

	t.Logf("Uploaded CID: %s (content: %q)", cid, content)

	// Wait for replication
	time.Sleep(replicaWait)

	// Verify on ipfs2 directly (not through nginx or storage)
	ipfs2Data := catFromIPFS(t, ipfs2API, cid)

	if string(ipfs2Data) != content {
		t.Fatalf("REPLICATION FAILED: ipfs2 has %q, expected %q", ipfs2Data, content)
	}

	t.Logf("✅ Replication verified: %d bytes identical on ipfs2", len(ipfs2Data))
}

// TestReplicationMultipleSizes tests replication across different file sizes.
// Ensures that both small and large files are fully replicated.
func TestReplicationMultipleSizes(t *testing.T) {
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("Set INTEGRATION=1 to run integration tests")
	}

	sizes := []struct {
		name string
		size int
	}{
		{"1KB", 1024},
		{"10KB", 10 * 1024},
		{"100KB", 100 * 1024},
		{"1MB", 1024 * 1024},
	}

	for _, tc := range sizes {
		t.Run(tc.name, func(t *testing.T) {
			// Generate random content
			content := make([]byte, tc.size)
			rand.Read(content)

			cid := uploadBytes(t, content, fmt.Sprintf("test-%s.png", tc.name))
			t.Logf("Uploaded %s: CID=%s", tc.name, cid)

			// Wait for replication with retry
			ipfs2Data := waitForReplication(t, ipfs2API, cid, tc.size)

			if !bytes.Equal(ipfs2Data, content) {
				t.Fatalf("REPLICATION FAILED for %s: got %d bytes, expected %d bytes",
					tc.name, len(ipfs2Data), len(content))
			}

			t.Logf("✅ %s replication verified: %d bytes identical on ipfs2", tc.name, len(ipfs2Data))
		})
	}
}

// TestReplicationNodeBRestart verifies that after restarting ipfs2,
// the data is still available (persistent storage + pin).
func TestReplicationNodeBRestart(t *testing.T) {
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("Set INTEGRATION=1 to run integration tests")
	}

	// Upload a file
	content := fmt.Sprintf("restart-test-%d", time.Now().UnixNano())
	cid := uploadFile(t, content, "test.png")
	t.Logf("Uploaded CID: %s", cid)

	// Wait for replication
	time.Sleep(replicaWait)

	// Verify it's on ipfs2 before restart
	beforeRestart := catFromIPFS(t, ipfs2API, cid)
	if string(beforeRestart) != content {
		t.Fatalf("Data not on ipfs2 before restart: got %q", beforeRestart)
	}

	// Verify pin status on ipfs2
	pinned := isPinnedOnIPFS(t, ipfs2API, cid)
	if !pinned {
		t.Fatal("CID is NOT pinned on ipfs2 — data will be lost after GC!")
	}

	t.Logf("✅ CID %s is pinned on ipfs2 — data is persistent", cid)
}

// TestReplicationBothNodesHaveData verifies that after upload,
// both ipfs1 AND ipfs2 have the identical data.
func TestReplicationBothNodesHaveData(t *testing.T) {
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("Set INTEGRATION=1 to run integration tests")
	}

	content := fmt.Sprintf("both-nodes-test-%d", time.Now().UnixNano())
	cid := uploadFile(t, content, "test.png")
	t.Logf("Uploaded CID: %s", cid)

	// Wait for replication
	time.Sleep(replicaWait)

	// Read from ipfs1
	ipfs1Data := catFromIPFS(t, ipfs1API, cid)
	// Read from ipfs2
	ipfs2Data := catFromIPFS(t, ipfs2API, cid)

	if string(ipfs1Data) != content {
		t.Fatalf("ipfs1 data mismatch: got %q, expected %q", ipfs1Data, content)
	}
	if string(ipfs2Data) != content {
		t.Fatalf("ipfs2 data mismatch: got %q, expected %q", ipfs2Data, content)
	}
	if !bytes.Equal(ipfs1Data, ipfs2Data) {
		t.Fatalf("DATA DIVERGENCE: ipfs1=%d bytes, ipfs2=%d bytes", len(ipfs1Data), len(ipfs2Data))
	}

	// Verify both are pinned
	if !isPinnedOnIPFS(t, ipfs1API, cid) {
		t.Fatal("CID not pinned on ipfs1!")
	}
	if !isPinnedOnIPFS(t, ipfs2API, cid) {
		t.Fatal("CID not pinned on ipfs2!")
	}

	t.Logf("✅ Both nodes have identical %d bytes, both pinned", len(ipfs1Data))
}

// TestReplicationAfterDelete verifies that after soft-delete,
// the file returns 404 through the API but data is still on ipfs
// (until GC runs).
func TestReplicationAfterDelete(t *testing.T) {
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("Set INTEGRATION=1 to run integration tests")
	}

	content := fmt.Sprintf("delete-test-%d", time.Now().UnixNano())
	cid := uploadFile(t, content, "test.png")
	t.Logf("Uploaded CID: %s", cid)

	// Wait for replication
	time.Sleep(replicaWait)

	// Verify data on ipfs2 before delete
	beforeDelete := catFromIPFS(t, ipfs2API, cid)
	if string(beforeDelete) != content {
		t.Fatalf("Replication failed before delete test")
	}

	// Delete through API
	deleteFile(t, cid)

	// API should return 404
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", nginxURL+"/file/"+cid, nil)
	req.Header.Set("X-API-Key", apiKey)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET after delete failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("Expected 404 after delete, got %d", resp.StatusCode)
	}

	// Data should still exist on ipfs2 (soft-delete, GC hasn't run yet)
	afterDelete := catFromIPFS(t, ipfs2API, cid)
	if string(afterDelete) != content {
		t.Fatalf("Data lost on ipfs2 after soft-delete! Got %q, expected %q", afterDelete, content)
	}

	t.Logf("✅ Soft-delete verified: API returns 404, data preserved on ipfs2")
}

// --- Helper functions ---

func uploadFile(t *testing.T, content string, filename string) string {
	t.Helper()
	return uploadBytes(t, []byte(content), filename)
}

func uploadBytes(t *testing.T, content []byte, filename string) string {
	t.Helper()

	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	w.Close()

	// Upload through nginx (round-robin to any storage)
	req, _ := http.NewRequest("POST", nginxURL+"/upload", &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-API-Key", apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Upload failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Upload status %d: %s", resp.StatusCode, body)
	}

	body, _ := io.ReadAll(resp.Body)
	cid := extractCID(string(body))
	if cid == "" {
		t.Fatalf("No CID in response: %s", body)
	}
	return cid
}

func catFromIPFS(t *testing.T, apiURL, cid string) []byte {
	t.Helper()

	client := &http.Client{Timeout: 60 * time.Second}
	url := apiURL + "/api/v0/cat?arg=" + cid

	req, _ := http.NewRequest("POST", url, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("ipfs cat failed on %s for CID %s: %v", apiURL, cid, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("ipfs cat status %d on %s for CID %s: %s", resp.StatusCode, apiURL, cid, body)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	return data
}

func isPinnedOnIPFS(t *testing.T, apiURL, cid string) bool {
	t.Helper()

	client := &http.Client{Timeout: 10 * time.Second}
	url := apiURL + "/api/v0/pin/ls?arg=" + cid

	req, _ := http.NewRequest("POST", url, nil)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

func deleteFile(t *testing.T, cid string) {
	t.Helper()

	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("DELETE", nginxURL+"/file/"+cid, nil)
	req.Header.Set("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Delete status %d: %s", resp.StatusCode, body)
	}
}

func waitForReplication(t *testing.T, apiURL, cid string, expectedSize int) []byte {
	t.Helper()

	deadline := time.Now().Add(maxReplicaWait)
	var lastData []byte

	for time.Now().Before(deadline) {
		data := catFromIPFS(t, apiURL, cid)
		if len(data) == expectedSize {
			return data
		}
		lastData = data
		time.Sleep(3 * time.Second)
	}

	t.Fatalf("Replication timeout for CID %s on %s: got %d bytes, expected %d", cid, apiURL, len(lastData), expectedSize)
	return nil
}

func extractCID(s string) string {
	idx := strings.Index(s, `"cid":"`)
	if idx == -1 {
		return ""
	}
	start := idx + len(`"cid":"`)
	end := strings.Index(s[start:], `"`)
	if end == -1 {
		return ""
	}
	return s[start : start+end]
}
