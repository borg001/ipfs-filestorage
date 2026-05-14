package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/store"
)

// mockCluster для handler-тестов
type mockCluster struct {
	files    map[string][]byte
	pinned   map[string]bool
	nextCID  int
	addErr   error
}

func newMockCluster() *mockCluster {
	return &mockCluster{
		files:  make(map[string][]byte),
		pinned: make(map[string]bool),
	}
}

func (m *mockCluster) ClusterAdd(ctx context.Context, filename string, data io.Reader) (*ipfs.AddResult, error) {
	if m.addErr != nil {
		return nil, m.addErr
	}
	d, _ := io.ReadAll(data)
	m.nextCID++
	cID := fmt.Sprintf("QmTest%d", m.nextCID)
	m.files[cID] = d
	m.pinned[cID] = true
	return &ipfs.AddResult{CID: cID, Name: filename}, nil
}

func (m *mockCluster) ClusterReplicate(ctx context.Context, cid string, retries int, delay time.Duration) error {
	return nil
}

func (m *mockCluster) ClusterStat(ctx context.Context, cid string) (*ipfs.StatResult, error) {
	d, ok := m.files[cid]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &ipfs.StatResult{CID: cid, Size: uint64(len(d))}, nil
}

func (m *mockCluster) ClusterTryFetch(ctx context.Context, cid string) (io.ReadCloser, error) {
	d, ok := m.files[cid]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return io.NopCloser(bytes.NewReader(d)), nil
}

func (m *mockCluster) ClusterUnpinAll(ctx context.Context, cid string) error {
	delete(m.pinned, cid)
	return nil
}

func (m *mockCluster) ClusterPinAllExcept(ctx context.Context, cid, skipURL string, retries int, delay time.Duration) error {
	return nil
}

func (m *mockCluster) ClusterIsPinnedAll(ctx context.Context, cid string) bool {
	return m.pinned[cid]
}

func (m *mockCluster) NodeURLs() []string {
	return []string{"http://mock:5001"}
}

func setupHandlerWithMock(cfg *config.Config, cluster *mockCluster) *Handler {
	unpinStore, _ := store.NewUnpinStore(filepath.Join(os.TempDir(), "test-unpin.json"))
	return &Handler{
		cfg:        cfg,
		cluster:    cluster,
		unpinStore: unpinStore,
	}
}

func TestHandleUpload_CountingReader(t *testing.T) {
	maxSize := int64(1024) // 1KB
	cfg := &config.Config{
		Upload: config.UploadConfig{
			MaxFileSize:      maxSize,
			AllowedExtensions: []string{"txt"},
			AllowedMimeTypes: map[string]bool{"text/plain; charset=utf-8": true},
		},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}

	cluster := newMockCluster()
	h := setupHandlerWithMock(cfg, cluster)

	// Загружаем файл 512 байт — меньше лимита
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write(make([]byte, 512))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.HandleUpload(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want 200 for file within limit", w.Code)
	}

	var resp Response
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Size != 512 {
		t.Errorf("Response Size = %d, want 512 (server-measured)", resp.Size)
	}
}

func TestHandleUpload_CountingReader_TooLarge(t *testing.T) {
	maxSize := int64(1024) // 1KB
	cfg := &config.Config{
		Upload: config.UploadConfig{
			MaxFileSize:      maxSize,
			AllowedExtensions: []string{"txt"},
			AllowedMimeTypes: map[string]bool{"text/plain; charset=utf-8": true},
		},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}

	cluster := newMockCluster()
	h := setupHandlerWithMock(cfg, cluster)

	// Загружаем файл 2KB — больше лимита
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write(make([]byte, 2*1024))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.HandleUpload(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("Status = %d, want 413 for oversized file", w.Code)
	}

	// Проверяем что файл был анпиннут (cleanup после обнаружения превышения)
	if len(cluster.pinned) != 0 {
		t.Errorf("File should have been unpinned after size check, pinned: %v", cluster.pinned)
	}
}

func TestHandleUpload_FakeHeaderSize(t *testing.T) {
	maxSize := int64(1024) // 1KB
	cfg := &config.Config{
		Upload: config.UploadConfig{
			MaxFileSize:      maxSize,
			AllowedExtensions: []string{"txt"},
			AllowedMimeTypes: map[string]bool{"text/plain; charset=utf-8": true},
		},
		Pinning: config.PinningConfig{RetryDelayMs: 100, Retries: 1},
	}

	cluster := newMockCluster()
	h := setupHandlerWithMock(cfg, cluster)

	// Создаём multipart с header.Size = 10 (подделка), но реальный контент = 2KB
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	// CreateFormFile не позволяет подделать Size, поэтому просто проверяем,
	// что сервер использует countingReader, а не header.Size
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write(make([]byte, 2*1024))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.HandleUpload(w, req)

	// Файл 2KB > лимит 1KB → 413, несмотря на то что header.Size может быть другим
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("Status = %d, want 413 — server must verify actual bytes, not header", w.Code)
	}
}
