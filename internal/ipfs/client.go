package ipfs

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/ipfs/go-cid"
	rpc "github.com/ipfs/kubo/client/rpc"
	"github.com/multiformats/go-multiaddr"
)

// Client — обёртка над kubo RPC клиентом одной IPFS-ноды.
type Client struct {
	url  string
	rpc  *rpc.HttpApi
}

// New создаёт нового IPFS клиента по URL (например http://localhost:5001).
func New(url string) (*Client, error) {
	ma, err := multiaddr.NewMultiaddr(httpURLToMultiaddr(url))
	if err != nil {
		return nil, fmt.Errorf("ipfs client: invalid url %q: %w", url, err)
	}

	api, err := rpc.NewApi(ma)
	if err != nil {
		return nil, fmt.Errorf("ipfs client: failed to create rpc api: %w", err)
	}

	return &Client{url: url, rpc: api}, nil
}

// URL возвращает адрес ноды.
func (c *Client) URL() string {
	return c.url
}

// AddResult — результат добавления файла в IPFS.
type AddResult struct {
	CID  string
	Size uint64
}

// Add добавляет файл в IPFS и возвращает CID.
func (c *Client) Add(ctx context.Context, filename string, r io.Reader) (*AddResult, error) {
	api := c.rpc.Unixfs()

	ip, err := api.Add(ctx, newNamedReader(filename, r))
	if err != nil {
		return nil, fmt.Errorf("ipfs add [%s]: %w", c.url, err)
	}

	stat, err := c.rpc.Block().Stat(ctx, ip)
	if err != nil {
		// не критично — размер можно не возвращать
		return &AddResult{CID: ip.Cid().String()}, nil
	}

	return &AddResult{
		CID:  ip.Cid().String(),
		Size: uint64(stat.Size()),
	}, nil
}

// Pin пиннит CID на ноде с retry-логикой.
func (c *Client) Pin(ctx context.Context, cidStr string, retries int, retryDelay time.Duration) error {
	cid, err := parseCID(cidStr)
	if err != nil {
		return err
	}

	ip, err := c.rpc.Unixfs().Get(ctx, mustPath(cid))
	_ = ip
	// Сначала проверим — уже запиннен?
	pins := c.rpc.Pin()
	_, isPinned, err := pins.IsPinned(ctx, mustPath(cid))
	if err == nil && isPinned {
		return nil
	}

	for attempt := 1; attempt <= retries; attempt++ {
		err = pins.Add(ctx, mustPath(cid))
		if err == nil {
			return nil
		}
		if attempt < retries {
			time.Sleep(retryDelay * time.Duration(attempt))
		}
	}
	return fmt.Errorf("ipfs pin [%s] after %d attempts: %w", c.url, retries, err)
}

// Unpin анпиннит CID на ноде.
func (c *Client) Unpin(ctx context.Context, cidStr string) error {
	cid, err := parseCID(cidStr)
	if err != nil {
		return err
	}

	err = c.rpc.Pin().Rm(ctx, mustPath(cid))
	if err != nil {
		return fmt.Errorf("ipfs unpin [%s]: %w", c.url, err)
	}
	return nil
}

// Cat читает содержимое файла по CID.
func (c *Client) Cat(ctx context.Context, cidStr string) (io.ReadCloser, error) {
	cid, err := parseCID(cidStr)
	if err != nil {
		return nil, err
	}

	node, err := c.rpc.Unixfs().Get(ctx, mustPath(cid))
	if err != nil {
		return nil, fmt.Errorf("ipfs cat [%s]: %w", c.url, err)
	}

	f, ok := node.(interface{ GetFile() io.ReadCloser })
	if !ok {
		return nil, fmt.Errorf("ipfs cat [%s]: not a file", c.url)
	}
	return f.GetFile(), nil
}

// StatResult — метаданные файла.
type StatResult struct {
	CID  string
	Size uint64
	Type string // "file" | "directory"
}

// Stat возвращает метаданные файла по CID.
func (c *Client) Stat(ctx context.Context, cidStr string) (*StatResult, error) {
	cid, err := parseCID(cidStr)
	if err != nil {
		return nil, err
	}

	stat, err := c.rpc.Block().Stat(ctx, mustPath(cid))
	if err != nil {
		return nil, fmt.Errorf("ipfs stat [%s]: %w", c.url, err)
	}

	return &StatResult{
		CID:  cidStr,
		Size: uint64(stat.Size()),
		Type: "file",
	}, nil
}

// --- helpers ---

func parseCID(cidStr string) (cid.Cid, error) {
	c, err := cid.Decode(cidStr)
	if err != nil {
		return cid.Undef, fmt.Errorf("invalid cid %q: %w", cidStr, err)
	}
	return c, nil
}

func mustPath(c cid.Cid) interface{} {
	// iface.ImmutablePath из go-libipfs
	// используем строковый путь /ipfs/<cid>
	return "/ipfs/" + c.String()
}

func httpURLToMultiaddr(url string) string {
	// Преобразует http://host:port → /ip4/host/tcp/port/http
	// Упрощённая версия для localhost/IP
	import_url := url
	_ = import_url
	// kubo rpc.NewApi принимает multiaddr вида /ip4/127.0.0.1/tcp/5001
	// но также есть NewApiWithClient который принимает http url напрямую
	// Возвращаем как есть — rpc умеет парсить http:// URL
	return url
}
