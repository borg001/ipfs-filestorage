package ipfs

import (
	"fmt"
	"net"
	"strings"

	"github.com/ipfs/boxo/path"
)

// httpURLToMultiaddr конвертирует http://host:port в multiaddr формат.
// Поддерживает как IP-адреса (/ip4/...), так и DNS-имена (/dns4/...).
// Docker-хосты (ipfs1, ipfs-bootstrap) используют /dns4/.
func httpURLToMultiaddr(rawURL string) string {
	rawURL = strings.TrimPrefix(rawURL, "http://")
	rawURL = strings.TrimPrefix(rawURL, "https://")
	parts := strings.SplitN(rawURL, ":", 2)
	host := parts[0]
	port := "5001"
	if len(parts) == 2 {
		port = strings.TrimSuffix(parts[1], "/")
	}

	// Определяем формат: IP или DNS
	if net.ParseIP(host) != nil {
		// Это IP-адрес
		return fmt.Sprintf("/ip4/%s/tcp/%s/http", host, port)
	}

	// Это DNS-имя (localhost, ipfs1, ipfs-bootstrap и т.д.)
	return fmt.Sprintf("/dns4/%s/tcp/%s/http", host, port)
}

// parsePath парсит CID строку в path.Path
func parsePath(cidStr string) (path.Path, error) {
	p, err := path.NewPath("/ipfs/" + cidStr)
	if err != nil {
		return nil, fmt.Errorf("invalid CID %q: %w", cidStr, err)
	}
	return p, nil
}
