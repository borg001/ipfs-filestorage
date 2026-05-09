//go:build integration

package integration

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	apiKey   = "SECRET_KEY_1"
	baseURL  = "http://localhost:8080" // nginx proxy
)

func TestUploadAndDownload(t *testing.T) {
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("Set INTEGRATION=1 to run integration tests")
	}

	client := &http.Client{Timeout: 30 * time.Second}

	// --- POST /upload ---
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fw, err := w.CreateFormFile("file", "test.txt")
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(fw, "hello integration test")
	w.Close()

	req, _ := http.NewRequest("POST", baseURL+"/upload", &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status %d: %s", resp.StatusCode, body)
	}

	cid := extractField(string(body), `"cid":"`)
	if cid == "" {
		t.Fatalf("no CID in response: %s", body)
	}
	t.Logf("Uploaded CID: %s", cid)

	// --- GET /file/{cid} ---
	req2, _ := http.NewRequest("GET", baseURL+"/file/"+cid, nil)
	req2.Header.Set("X-API-Key", apiKey)
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("download status %d: %s", resp2.StatusCode, body2)
	}
	if string(body2) != "hello integration test" {
		t.Fatalf("content mismatch: got %q, want %q", body2, "hello integration test")
	}

	// --- DELETE /file/{cid} ---
	req3, _ := http.NewRequest("DELETE", baseURL+"/file/"+cid, nil)
	req3.Header.Set("X-API-Key", apiKey)
	resp3, err := client.Do(req3)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	body3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()

	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("delete status %d: %s", resp3.StatusCode, body3)
	}

	// --- GET /file/{cid} after delete → 404 ---
	req4, _ := http.NewRequest("GET", baseURL+"/file/"+cid, nil)
	req4.Header.Set("X-API-Key", apiKey)
	resp4, err := client.Do(req4)
	if err != nil {
		t.Fatalf("post-delete get failed: %v", err)
	}
	resp4.Body.Close()

	if resp4.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", resp4.StatusCode)
	}

	t.Log("Upload → Download → Delete → 404: OK")
}

func TestUploadMultiple(t *testing.T) {
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("Set INTEGRATION=1 to run integration tests")
	}

	client := &http.Client{Timeout: 30 * time.Second}

	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for i, name := range []string{"file1", "file2"} {
		fw, _ := w.CreateFormFile(name, name+".txt")
		io.WriteString(fw, "content-"+string(rune('0'+i)))
	}
	w.Close()

	req, _ := http.NewRequest("POST", baseURL+"/upload-multiple", &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("upload-multiple failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload-multiple status %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"cid"`) {
		t.Fatalf("expected cids in response, got: %s", body)
	}

	t.Logf("Multiple upload response: %s", body)
}

func TestAuth(t *testing.T) {
	if os.Getenv("INTEGRATION") == "" {
		t.Skip("Set INTEGRATION=1 to run integration tests")
	}

	client := &http.Client{Timeout: 10 * time.Second}

	// No API key → 401
	resp, _ := client.Get(baseURL + "/file/QmTest")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without key, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Wrong API key → 403
	req, _ := http.NewRequest("GET", baseURL+"/file/QmTest", nil)
	req.Header.Set("X-API-Key", "wrong-key")
	resp2, _ := client.Do(req)
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 with bad key, got %d", resp2.StatusCode)
	}
	resp2.Body.Close()

	t.Log("Auth checks: OK")
}

// extractField extracts a JSON string field value, e.g. extractField(s, `"cid":"`) returns the CID.
func extractField(s, key string) string {
	idx := strings.Index(s, key)
	if idx == -1 {
		return ""
	}
	start := idx + len(key)
	end := strings.Index(s[start:], `"`)
	if end == -1 {
		return ""
	}
	return s[start : start+end]
}
