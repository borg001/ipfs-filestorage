package ipfs

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/ipfs/boxo/files"
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

// Stat возвращает метаданные файла по CID
func (c *Client) Stat(ctx context.Context, cidStr string) (*StatResult, error) {
	p, err := parsePath(cidStr)
	if err != nil {
		return nil, err
	}
	stat, err := c.api.Object().Stat(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("ipfs stat failed for %s: %w", cidStr, err)
	}
	return &StatResult{
		CID:  cidStr,
		Size: uint64(stat.CumulativeSize),
	}, nil
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
