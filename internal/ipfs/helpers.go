package ipfs

import (
	"fmt"
	"strings"

	"github.com/ipfs/boxo/path"
)

// httpURLToMultiaddr конвертирует http://host:port в /ip4/host/tcp/port/http
func httpURLToMultiaddr(rawURL string) string {
	rawURL = strings.TrimPrefix(rawURL, "http://")
	rawURL = strings.TrimPrefix(rawURL, "https://")
	parts := strings.SplitN(rawURL, ":", 2)
	host := parts[0]
	port := "5001"
	if len(parts) == 2 {
		port = strings.TrimSuffix(parts[1], "/")
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("/ip4/%s/tcp/%s/http", host, port)
}

// parsePath парсит CID строку в path.Path
func parsePath(cidStr string) (path.Path, error) {
	p, err := path.NewPath("/ipfs/" + cidStr)
	if err != nil {
		return nil, fmt.Errorf("invalid CID %q: %w", cidStr, err)
	}
	return p, nil
}
