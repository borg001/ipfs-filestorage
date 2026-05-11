package ipfs

import (
	"context"
	"io"
	"time"
)

// Clusterer — интерфейс для работы с IPFS-кластером.
// Позволяет подменять реализацию в тестах (mock).
type Clusterer interface {
	ClusterAdd(ctx context.Context, filename string, r io.Reader) (*AddResult, error)
	ClusterReplicate(ctx context.Context, cid string, retries int, delay time.Duration) error
	ClusterPinAllExcept(ctx context.Context, cid, skipURL string, retries int, delay time.Duration) error
	ClusterCat(ctx context.Context, cid string) (io.ReadCloser, error)
	ClusterStat(ctx context.Context, cid string) (*StatResult, error)
	ClusterUnpinAll(ctx context.Context, cid string) error
	ClusterIsPinnedAll(ctx context.Context, cid string) (bool, error)
	ClusterTryFetch(ctx context.Context, cid string) (io.ReadCloser, error)
	NodeURLs() []string
}
