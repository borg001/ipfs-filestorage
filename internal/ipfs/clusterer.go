package ipfs

import (
	"context"
	"io"
	"time"
)

// Clusterer — интерфейс для работы с IPFS-кластером.
// Позволяет подменять реализацию в тестах (mockCluster).
type Clusterer interface {
	// ClusterAdd загружает файл на первую доступную ноду и возвращает CID.
	ClusterAdd(ctx context.Context, filename string, data io.Reader) (*AddResult, error)

	// ClusterAddDir загружает директорию файлов на первую доступную ноду и возвращает root CID.
	ClusterAddDir(ctx context.Context, files map[string][]byte) (*AddResult, error)

	// ClusterReplicate реплицирует CID на все ноды (Fetch + Pin).
	ClusterReplicate(ctx context.Context, cid string, retries int, delay time.Duration) error

	// ClusterStat возвращает метаданные файла.
	ClusterStat(ctx context.Context, cid string) (*StatResult, error)

	// ClusterTryFetch читает файл по CID, перебирая ноды.
	ClusterTryFetch(ctx context.Context, cid string) (io.ReadCloser, error)

	// ClusterTryFetchPath читает файл внутри UnixFS directory CID.
	ClusterTryFetchPath(ctx context.Context, cid string, filePath string) (io.ReadCloser, error)

	// ClusterUnpinAll анпиннит CID на всех нодах кластера.
	ClusterUnpinAll(ctx context.Context, cid string) error

	// ClusterPinAllExcept реплицирует CID на все ноды КРОМЕ указанной.
	ClusterPinAllExcept(ctx context.Context, cid, skipURL string, retries int, delay time.Duration) error

	// ClusterIsPinnedAll проверяет что CID запиннен на ВСЕХ нодах.
	ClusterIsPinnedAll(ctx context.Context, cid string) bool

	// NodeURLs возвращает адреса всех нод кластера.
	NodeURLs() []string
}
