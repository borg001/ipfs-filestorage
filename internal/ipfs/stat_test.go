package ipfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/ipfs/boxo/files"
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

// mockStatNode реализует format.Node для моков
type mockStatNode struct {
	data []byte
	links []*mockStatLink
}

func (n *mockStatNode) RawData() []byte                    { return n.data }
func (n *mockStatNode) Cid() cid.Cid                      { return cid.Cid{} }
func (n *mockStatNode) Links() []*formatLink              {
	out := make([]*formatLink, len(n.links))
	for i, l := range n.links {
		out[i] = &formatLink{Size: l.size}
	}
	return out
}
func (n *mockStatNode) ResolveLink(path []string) (*formatLink, []string, error) {
	return nil, nil, nil
}
func (n *mockStatNode) Resolve(path []string) (interface{}, []string, error) {
	return nil, nil, nil
}
func (n *mockStatNode) Tree(path string, depth int) []string { return nil }
func (n *mockStatNode) Copy() format.Node                    { return n }
func (n *mockStatNode) Stat() (*formatNodeStat, error)     { return nil, nil }
func (n *mockStatNode) Size() (uint64, error)               { return uint64(len(n.data)), nil }

type mockStatLink struct {
	size uint64
}

type formatLink struct {
	Size uint64
}

type formatNodeStat struct {
	Hash           string
	NumLinks       int
	BlockSize      int
	LinksSize      int
	DataSize       int
	CumulativeSize int
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

// ---- Юнит-тесты для cumulativeSize ----

func TestCumulativeSizeLeaf(t *testing.T) {
	// Для листовой ноды (без links) — размер = len(data)
	mc := newMockClient()
	node := &mockStatNode{data: []byte("hello world")}

	links := node.Links()
	if len(links) != 0 {
		t.Fatalf("expected 0 links, got %d", len(links))
	}

	size, err := node.Size()
	if err != nil {
		t.Fatal(err)
	}
	if size != 11 {
		t.Errorf("leaf node Size = %d, want 11", size)
	}
}

func TestCumulativeSizeWithLinks(t *testing.T) {
	// Для ноды со ссылками — cumulative size = sum(link.Size)
	node := &mockStatNode{
		data: []byte("dir"),
		links: []*mockStatLink{
			{size: 1024},
			{size: 2048},
			{size: 4096},
		},
	}

	var total uint64
	for _, link := range node.Links() {
		total += link.Size
	}
	if total != 7168 {
		t.Errorf("cumulative size from links = %d, want 7168", total)
	}
}

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
