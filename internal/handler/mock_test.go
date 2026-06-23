package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/ipfs"
	"github.com/borg001/ipfs-filestorage/internal/store"
)

// mockCluster реализует ipfs.Clusterer для unit-тестов
type mockCluster struct {
	mu       sync.Mutex
	files    map[string][]byte // CID -> content
	dirs     map[string]map[string][]byte
	pinned   map[string]bool   // CID -> pinned
	nodeAddr map[string]string // CID -> node URL
}

func newMockCluster() ipfs.Clusterer {
	return &mockCluster{
		files:    make(map[string][]byte),
		dirs:     make(map[string]map[string][]byte),
		pinned:   make(map[string]bool),
		nodeAddr: make(map[string]string),
	}
}

func (m *mockCluster) ClusterAddDir(ctx context.Context, entries map[string][]byte) (*ipfs.AddResult, error) {
	var combined []byte
	for name, data := range entries {
		combined = append(combined, []byte(name)...)
		combined = append(combined, data...)
	}
	cid := fmt.Sprintf("Qm%s", mockHash(combined))
	copied := make(map[string][]byte, len(entries))
	for name, data := range entries {
		copied[name] = append([]byte(nil), data...)
	}
	m.mu.Lock()
	m.dirs[cid] = copied
	m.mu.Unlock()
	return &ipfs.AddResult{CID: cid, Name: ""}, nil
}

func (m *mockCluster) ClusterAdd(ctx context.Context, filename string, r io.Reader) (*ipfs.AddResult, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	cid := fmt.Sprintf("Qm%s", mockHash(data))
	m.mu.Lock()
	m.files[cid] = data
	m.mu.Unlock()
	return &ipfs.AddResult{CID: cid, Name: filename}, nil
}

func (m *mockCluster) ClusterReplicate(ctx context.Context, cid string, retries int, delay time.Duration) error {
	m.mu.Lock()
	m.pinned[cid] = true
	m.mu.Unlock()
	return nil
}

func (m *mockCluster) ClusterPinAllExcept(ctx context.Context, cid string, skipURL string, retries int, delay time.Duration) error {
	return m.ClusterReplicate(ctx, cid, retries, delay)
}

func (m *mockCluster) ClusterStat(ctx context.Context, cid string) (*ipfs.StatResult, error) {
	m.mu.Lock()
	data, ok := m.files[cid]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &ipfs.StatResult{CID: cid, Size: uint64(len(data))}, nil
}

func (m *mockCluster) ClusterTryFetch(ctx context.Context, cid string) (io.ReadCloser, error) {
	m.mu.Lock()
	data, ok := m.files[cid]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("file not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *mockCluster) ClusterTryFetchPath(ctx context.Context, cid string, filePath string) (io.ReadCloser, error) {
	m.mu.Lock()
	dir, ok := m.dirs[cid]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("directory not found")
	}
	data, ok := dir[filePath]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("file not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *mockCluster) ClusterUnpinAll(ctx context.Context, cid string) error {
	m.mu.Lock()
	delete(m.pinned, cid)
	m.mu.Unlock()
	return nil
}

func (m *mockCluster) ClusterIsPinnedAll(ctx context.Context, cid string) bool {
	m.mu.Lock()
	ok := m.pinned[cid]
	m.mu.Unlock()
	return ok
}

func (m *mockCluster) NodeURLs() []string {
	return []string{"http://mock1:5001", "http://mock2:5001"}
}

// Простой hash для mock CID
var hashCounter int
var hashMu sync.Mutex

func mockHash(data []byte) string {
	hashMu.Lock()
	defer hashMu.Unlock()
	hashCounter++
	raw := fmt.Sprintf("mock%d%x", hashCounter, len(data))
	if len(raw) < 44 {
		raw += strings.Repeat("a", 44-len(raw))
	}
	return raw[:44]
}

// Создаёт тестовый handler с mock cluster
func setupTestHandler(cfg *config.Config) *Handler {
	cluster := newMockCluster()
	unpinStore, _ := store.NewUnpinStore("/tmp/test-unpin-store.json")
	return &Handler{
		cfg:        cfg,
		cluster:    cluster,
		unpinStore: unpinStore,
	}
}
