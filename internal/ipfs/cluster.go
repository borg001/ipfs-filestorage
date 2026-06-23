package ipfs

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// ClusterManager управляет репликацией файлов по всем нодам IPFS-кластера.
type ClusterManager struct {
	nodes []*ClusterNode
}

// ClusterNode — одна нода в кластере
type ClusterNode struct {
	*Client
	URL string
}

// NewCluster создаёт ClusterManager из списка URL нод
func NewCluster(nodeURLs []string) *ClusterManager {
	m := &ClusterManager{}
	for _, url := range nodeURLs {
		client, err := New(url)
		if err != nil {
			continue
		}
		m.nodes = append(m.nodes, &ClusterNode{Client: client, URL: url})
	}
	return m
}

// ClusterAdd загружает файл на первую доступную ноду и возвращает CID.
func (cm *ClusterManager) ClusterAdd(ctx context.Context, filename string, data io.Reader) (*AddResult, error) {
	if len(cm.nodes) == 0 {
		return nil, fmt.Errorf("no IPFS nodes in cluster")
	}
	return cm.nodes[0].Add(ctx, filename, data)
}

// ClusterAddDir загружает директорию на первую доступную ноду и возвращает root CID.
func (cm *ClusterManager) ClusterAddDir(ctx context.Context, files map[string][]byte) (*AddResult, error) {
	if len(cm.nodes) == 0 {
		return nil, fmt.Errorf("no IPFS nodes in cluster")
	}
	return cm.nodes[0].AddDir(ctx, files)
}

// ClusterCat читает файл по CID, пытаясь последовательно все ноды.
func (cm *ClusterManager) ClusterCat(ctx context.Context, cid string) (io.ReadCloser, error) {
	if len(cm.nodes) == 0 {
		return nil, fmt.Errorf("no IPFS nodes in cluster")
	}
	return cm.ClusterTryFetch(ctx, cid)
}

// ClusterStat возвращает метаданные файла с первой ноды.
func (cm *ClusterManager) ClusterStat(ctx context.Context, cid string) (*StatResult, error) {
	if len(cm.nodes) == 0 {
		return nil, fmt.Errorf("no IPFS nodes in cluster")
	}
	return cm.nodes[0].Stat(ctx, cid)
}

// ClusterReplicate реплицирует CID на все ноды кластера.
// Для каждой ноды: Fetch (подтягивает блоки через bitswap) + Pin.
// Гарантирует, что данные физически присутствуют на каждой ноде.
func (cm *ClusterManager) ClusterReplicate(ctx context.Context, cid string, retries int, delay time.Duration) error {
	if len(cm.nodes) == 0 {
		return fmt.Errorf("no IPFS nodes in cluster")
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(cm.nodes))

	for _, node := range cm.nodes {
		wg.Add(1)
		go func(n *ClusterNode) {
			defer wg.Done()

			// Шаг 1: Fetch — Cat() + drain reader
			// Это заставляет bitswap подтянуть ВСЕ блоки DAG с ноды-источника.
			// Без этого Pin() создаст маркер без реальных данных.
			fetchErr := cm.fetchWithRetry(ctx, n, cid, retries, delay)
			if fetchErr != nil {
				errCh <- fmt.Errorf("fetch failed on %s: %w", n.URL, fetchErr)
				return
			}

			// Шаг 2: Pin — теперь блоки локальны, pin гарантирует сохранение
			if err := n.Pin(ctx, cid, retries, delay); err != nil {
				errCh <- fmt.Errorf("pin failed on %s: %w", n.URL, err)
			}
		}(node)
	}

	wg.Wait()
	close(errCh)

	var errs []error
	for e := range errCh {
		errs = append(errs, e)
	}
	if len(errs) > 0 {
		return fmt.Errorf("cluster replicate errors (%d/%d): %v", len(errs), len(cm.nodes), errs)
	}
	return nil
}

// fetchWithRetry пытается подтянуть данные с повторными попытками.
// После Add на исходной ноде может потребоваться время для DHT-пропагации.
func (cm *ClusterManager) fetchWithRetry(ctx context.Context, node *ClusterNode, cid string, retries int, delay time.Duration) error {
	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		if err := node.Fetch(ctx, cid); err != nil {
			lastErr = err
			if attempt < retries {
				time.Sleep(delay * time.Duration(attempt))
			}
			continue
		}
		return nil
	}
	return fmt.Errorf("fetch failed after %d attempts: %w", retries, lastErr)
}

// ClusterPinAllExcept реплицирует CID на все ноды КРОМЕ указанной.
func (cm *ClusterManager) ClusterPinAllExcept(ctx context.Context, cid, skipURL string, retries int, delay time.Duration) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(cm.nodes))
	for _, node := range cm.nodes {
		if node.URL == skipURL {
			continue
		}
		wg.Add(1)
		go func(n *ClusterNode) {
			defer wg.Done()
			if err := n.Fetch(ctx, cid); err != nil {
				errCh <- fmt.Errorf("fetch failed on %s: %w", n.URL, err)
				return
			}
			if err := n.Pin(ctx, cid, retries, delay); err != nil {
				errCh <- fmt.Errorf("pin failed on %s: %w", n.URL, err)
			}
		}(node)
	}
	wg.Wait()
	close(errCh)
	var errs []error
	for e := range errCh {
		errs = append(errs, e)
	}
	if len(errs) > 0 {
		return fmt.Errorf("cluster replicate errors (%d): %v", len(errs), errs)
	}
	return nil
}

// ClusterUnpinAll анпиннит CID на всех нодах кластера.
func (cm *ClusterManager) ClusterUnpinAll(ctx context.Context, cid string) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(cm.nodes))
	for _, node := range cm.nodes {
		wg.Add(1)
		go func(n *ClusterNode) {
			defer wg.Done()
			if err := n.Unpin(ctx, cid); err != nil {
				errCh <- fmt.Errorf("unpin failed on %s: %w", n.URL, err)
			}
		}(node)
	}
	wg.Wait()
	close(errCh)
	var errs []error
	for e := range errCh {
		errs = append(errs, e)
	}
	if len(errs) > 0 {
		return fmt.Errorf("cluster unpin errors (%d/%d): %v", len(errs), len(cm.nodes), errs)
	}
	return nil
}

// ClusterIsPinnedAll проверяет что CID запиннен на ВСЕХ нодах.
func (cm *ClusterManager) ClusterIsPinnedAll(ctx context.Context, cid string) bool {
	if len(cm.nodes) == 0 {
		return false
	}
	var wg sync.WaitGroup
	resultCh := make(chan bool, len(cm.nodes))
	for _, node := range cm.nodes {
		wg.Add(1)
		go func(n *ClusterNode) {
			defer wg.Done()
			pinned, _ := n.IsPinned(ctx, cid)
			resultCh <- pinned
		}(node)
	}
	wg.Wait()
	close(resultCh)
	for r := range resultCh {
		if !r {
			return false
		}
	}
	return true
}

// ClusterTryFetch пытается прочитать файл по CID, перебирая ноды.
func (cm *ClusterManager) ClusterTryFetch(ctx context.Context, cid string) (io.ReadCloser, error) {
	if len(cm.nodes) == 0 {
		return nil, fmt.Errorf("no IPFS nodes in cluster")
	}
	for _, node := range cm.nodes {
		r, err := node.Cat(ctx, cid)
		if err == nil {
			return r, nil
		}
	}
	return nil, fmt.Errorf("file %s not available on any node", cid)
}

// ClusterTryFetchPath пытается прочитать path внутри directory CID, перебирая ноды.
func (cm *ClusterManager) ClusterTryFetchPath(ctx context.Context, cid string, filePath string) (io.ReadCloser, error) {
	if len(cm.nodes) == 0 {
		return nil, fmt.Errorf("no IPFS nodes in cluster")
	}
	for _, node := range cm.nodes {
		r, err := node.CatPath(ctx, cid, filePath)
		if err == nil {
			return r, nil
		}
	}
	return nil, fmt.Errorf("file %s/%s not available on any node", cid, filePath)
}

// NodeURLs возвращает адреса всех нод кластера.
func (cm *ClusterManager) NodeURLs() []string {
	urls := make([]string, len(cm.nodes))
	for i, n := range cm.nodes {
		urls[i] = n.URL
	}
	return urls
}
