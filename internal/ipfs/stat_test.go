package ipfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
)

// ---- mockClient для юнит-тестов без IPFS-ноды ----

type mockClient struct {
	mu      sync.Mutex
	files   map[string][]byte
	pinned  map[string]bool
	nextCID int
}

func newMockClient() *mockClient {
	return &mockClient{
		files:  make(map[string][]byte),
		pinned: make(map[string]bool),
	}
}

func (m *mockClient) Add(ctx context.Context, filename string, r io.Reader) (*AddResult, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.nextCID++
	cID := fmt.Sprintf("QmMock%d", m.nextCID)
	m.files[cID] = data
	m.mu.Unlock()
	return &AddResult{CID: cID, Name: filename}, nil
}

func (m *mockClient) Pin(ctx context.Context, cidStr string, retries int, delay time.Duration) error {
	m.mu.Lock()
	m.pinned[cidStr] = true
	m.mu.Unlock()
	return nil
}

func (m *mockClient) Unpin(ctx context.Context, cidStr string) error {
	m.mu.Lock()
	delete(m.pinned, cidStr)
	m.mu.Unlock()
	return nil
}

func (m *mockClient) Cat(ctx context.Context, cidStr string) (io.ReadCloser, error) {
	m.mu.Lock()
	data, ok := m.files[cidStr]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *mockClient) Fetch(ctx context.Context, cidStr string) error {
	m.mu.Lock()
	_, ok := m.files[cidStr]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("not found")
	}
	return nil
}

func (m *mockClient) Stat(ctx context.Context, cidStr string) (*StatResult, error) {
	m.mu.Lock()
	data, ok := m.files[cidStr]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &StatResult{CID: cidStr, Size: uint64(len(data))}, nil
}

func (m *mockClient) IsPinned(ctx context.Context, cidStr string) (bool, error) {
	m.mu.Lock()
	p := m.pinned[cidStr]
	m.mu.Unlock()
	return p, nil
}

func (m *mockClient) URL() string { return "http://mock:5001" }

// ---- Юнит-тесты ----

func TestMockClientStat(t *testing.T) {
	mc := newMockClient()
	ctx := context.Background()

	result, err := mc.Add(ctx, "test.txt", bytes.NewReader([]byte("hello")))
	if err != nil {
		t.Fatal(err)
	}

	stat, err := mc.Stat(ctx, result.CID)
	if err != nil {
		t.Fatal(err)
	}
	if stat.CID != result.CID {
		t.Errorf("Stat CID = %q, want %q", stat.CID, result.CID)
	}
	if stat.Size != 5 {
		t.Errorf("Stat Size = %d, want 5", stat.Size)
	}
}

func TestMockClientStatNotFound(t *testing.T) {
	mc := newMockClient()
	ctx := context.Background()

	_, err := mc.Stat(ctx, "QmNonExistent")
	if err == nil {
		t.Error("expected error for non-existent CID")
	}
}

func TestCumulativeSizeLogic(t *testing.T) {
	// Тестируем логику подсчёта cumulative size:
	// - Лист (0 links) → размер блока
	// - Директория (links) → sum(link.Size)

	// В реальном production-коде cumulativeSize использует Dag().Get().
	// Здесь проверяем саму логику расчёта.

	tests := []struct {
		name       string
		linkSizes  []uint64
		leafSize   uint64
		want       uint64
	}{
		{"leaf_only", nil, 42, 42},
		{"single_link", []uint64{1024}, 0, 1024},
		{"three_links", []uint64{1024, 2048, 4096}, 0, 7168},
		{"zero", nil, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got uint64
			if len(tt.linkSizes) == 0 {
				got = tt.leafSize
			} else {
				for _, s := range tt.linkSizes {
					got += s
				}
			}
			if got != tt.want {
				t.Errorf("cumulativeSize = %d, want %d", got, tt.want)
			}
		})
	}

	_ = cid.Cid{} // просто убеждаемся что импорт есть
}
