package ipfs

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ipfs/boxo/files"
	"github.com/ipfs/boxo/path"
	"github.com/ipfs/go-cid"
	rpc "github.com/ipfs/kubo/client/rpc"
	"github.com/ipfs/kubo/core/coreiface/options"
	"github.com/multiformats/go-multiaddr"
)

// Client — обёртка над kubo RPC клиентом одной IPFS-ноды
type Client struct {
	api *rpc.HttpApi
	url string
}

// AddResult — результат добавления файла
type AddResult struct {
	CID  string
	Name string
}

// StatResult — метаданные файла
type StatResult struct {
	CID  string
	Size uint64
}

// New создаёт новый Client по HTTP URL ноды (например http://localhost:5001)
func New(url string) (*Client, error) {
	ma, err := multiaddr.NewMultiaddr(httpURLToMultiaddr(url))
	if err != nil {
		return nil, fmt.Errorf("invalid ipfs url %q: %w", url, err)
	}
	api, err := rpc.NewApi(ma)
	if err != nil {
		return nil, fmt.Errorf("failed to create ipfs rpc client: %w", err)
	}
	return &Client{api: api, url: url}, nil
}

// Add загружает файл в IPFS и возвращает CID
func (c *Client) Add(ctx context.Context, filename string, r io.Reader) (*AddResult, error) {
	f := files.NewReaderFile(r)
	resolved, err := c.api.Unixfs().Add(ctx, f, options.Unixfs.Pin(false))
	if err != nil {
		return nil, fmt.Errorf("ipfs add failed: %w", err)
	}
	return &AddResult{
		CID:  resolved.RootCid().String(),
		Name: filename,
	}, nil
}

// AddDir загружает UnixFS directory в IPFS и возвращает root CID.
func (c *Client) AddDir(ctx context.Context, entries map[string][]byte) (*AddResult, error) {
	nodes := make(map[string]files.Node, len(entries))
	for name, data := range entries {
		nodes[name] = files.NewBytesFile(data)
	}
	dir := files.NewMapDirectory(nodes)
	resolved, err := c.api.Unixfs().Add(ctx, dir, options.Unixfs.Pin(false))
	if err != nil {
		return nil, fmt.Errorf("ipfs add dir failed: %w", err)
	}
	return &AddResult{CID: resolved.RootCid().String(), Name: ""}, nil
}

// Pin пиннит CID с retry-логикой, идемпотентен
func (c *Client) Pin(ctx context.Context, cidStr string, retries int, delay time.Duration) error {
	p, err := parsePath(cidStr)
	if err != nil {
		return err
	}

	// Проверяем — может уже запиннен
	_, isPinned, err := c.api.Pin().IsPinned(ctx, p)
	if err == nil && isPinned {
		return nil
	}

	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		if err := c.api.Pin().Add(ctx, p); err != nil {
			lastErr = err
			if attempt < retries {
				time.Sleep(delay * time.Duration(attempt))
			}
			continue
		}
		return nil
	}
	return fmt.Errorf("pin failed after %d attempts: %w", retries, lastErr)
}

// Unpin анпиннит CID
func (c *Client) Unpin(ctx context.Context, cidStr string) error {
	p, err := parsePath(cidStr)
	if err != nil {
		return err
	}
	if err := c.api.Pin().Rm(ctx, p); err != nil {
		return fmt.Errorf("unpin failed for %s: %w", cidStr, err)
	}
	return nil
}

// Cat читает содержимое файла по CID
func (c *Client) Cat(ctx context.Context, cidStr string) (io.ReadCloser, error) {
	p, err := parsePath(cidStr)
	if err != nil {
		return nil, err
	}
	node, err := c.api.Unixfs().Get(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("ipfs get failed for %s: %w", cidStr, err)
	}
	f, ok := node.(files.File)
	if !ok {
		return nil, fmt.Errorf("cid %s is not a file", cidStr)
	}
	return f, nil
}

// CatPath читает файл внутри UnixFS directory CID.
func (c *Client) CatPath(ctx context.Context, cidStr, filePath string) (io.ReadCloser, error) {
	p, err := path.NewPath("/ipfs/" + cidStr + "/" + filePath)
	if err != nil {
		return nil, fmt.Errorf("invalid IPFS path %q/%q: %w", cidStr, filePath, err)
	}
	node, err := c.api.Unixfs().Get(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("ipfs get failed for %s/%s: %w", cidStr, filePath, err)
	}
	f, ok := node.(files.File)
	if !ok {
		return nil, fmt.Errorf("path %s/%s is not a file", cidStr, filePath)
	}
	return f, nil
}

// Fetch подтягивает все блоки CID с других нод через bitswap.
// Вызывает Cat() и полностью вычитывает reader, чтобы гарантировать
// передачу всех блоков DAG. После Fetch блоки локальны, и Pin сработает.
func (c *Client) Fetch(ctx context.Context, cidStr string) error {
	p, err := parsePath(cidStr)
	if err != nil {
		return err
	}
	node, err := c.api.Unixfs().Get(ctx, p)
	if err != nil {
		return fmt.Errorf("ipfs get failed for %s: %w", cidStr, err)
	}
	if err := drainUnixFS(node); err != nil {
		return fmt.Errorf("fetch drain failed for %s: %w", cidStr, err)
	}
	return nil
}

func drainUnixFS(node files.Node) error {
	switch n := node.(type) {
	case files.File:
		defer n.Close()
		_, err := io.Copy(io.Discard, n)
		return err
	case files.Directory:
		entries := n.Entries()
		for entries.Next() {
			if err := drainUnixFS(entries.Node()); err != nil {
				return err
			}
		}
		if err := entries.Err(); err != nil && !strings.Contains(err.Error(), "EOF") {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unsupported UnixFS node %T", node)
	}
}

// Stat возвращает метаданные файла по CID.
// Использует Dag API вместо удалённого в Kubo v0.27 Object API.
// Для UnixFS файлов считает cumulative size через links.
func (c *Client) Stat(ctx context.Context, cidStr string) (*StatResult, error) {
	cID, err := cid.Parse(cidStr)
	if err != nil {
		return nil, fmt.Errorf("invalid CID %q: %w", cidStr, err)
	}

	size, err := c.cumulativeSize(ctx, cID)
	if err != nil {
		return nil, fmt.Errorf("ipfs stat failed for %s: %w", cidStr, err)
	}

	return &StatResult{
		CID:  cidStr,
		Size: size,
	}, nil
}

// cumulativeSize считает размер файла в IPFS, обходя DAG.
// Для листовых нод — размер самого блока.
// Для нод со ссылками — сумма Size всех links (каждая link.Size уже
// содержит cumulative size поддерева для UnixFS).
func (c *Client) cumulativeSize(ctx context.Context, cID cid.Cid) (uint64, error) {
	node, err := c.api.Dag().Get(ctx, cID)
	if err != nil {
		return 0, err
	}

	links := node.Links()
	if len(links) == 0 {
		// Листовая нода — размер = сериализованный размер блока
		return node.Size()
	}

	// Для нод со ссылками: каждая link.Size уже содержит
	// cumulative size поддерева (поведение UnixFS)
	var total uint64
	for _, link := range links {
		total += link.Size
	}
	return total, nil
}

// IsPinned проверяет запиннен ли CID
func (c *Client) IsPinned(ctx context.Context, cidStr string) (bool, error) {
	p, err := parsePath(cidStr)
	if err != nil {
		return false, err
	}
	_, pinned, err := c.api.Pin().IsPinned(ctx, p)
	if err != nil {
		return false, fmt.Errorf("isPinned failed for %s: %w", cidStr, err)
	}
	return pinned, nil
}

// URL возвращает адрес ноды
func (c *Client) URL() string {
	return c.url
}

// parsePath парсит CID строку в path.Path (для Pin/Unpin/IsPinned)
func parsePath(cidStr string) (path.Path, error) {
	p, err := path.NewPath("/ipfs/" + cidStr)
	if err != nil {
		return nil, fmt.Errorf("invalid CID %q: %w", cidStr, err)
	}
	return p, nil
}
