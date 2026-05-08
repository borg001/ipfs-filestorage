package ipfs

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

type ClusterManager struct {
	nodes []*ClusterNode
}

type ClusterNode struct {
	*Client
	URL string
}

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

func (cm *ClusterManager) ClusterAdd(ctx context.Context, filename string, data io.Reader) (*AddResult, error) {
	if len(cm.nodes) == 0 {
		return nil, fmt.Errorf("no IPFS nodes in cluster")
	}
	return cm.nodes[0].Add(ctx, filename, data)
}

func (cm *ClusterManager) ClusterCat(ctx context.Context, cid string) (io.ReadCloser, error) {
	if len(cm.nodes) == 0 {
		return nil, fmt.Errorf("no IPFS nodes in cluster")
	}
	return cm.ClusterTryFetch(ctx, cid)
}

func (cm *ClusterManager) ClusterStat(ctx context.Context, cid string) (*StatResult, error) {
	if len(cm.nodes) == 0 {
		return nil, fmt.Errorf("no IPFS nodes in cluster")
	}
	return cm.nodes[0].Stat(ctx, cid)
}

func (cm *ClusterManager) ClusterPinAll(ctx context.Context, cid string, retries int, delay time.Duration) error {
	if len(cm.nodes) == 0 {
		return fmt.Errorf("no IPFS nodes in cluster")
	}
	var wg sync.WaitGroup
	errCh := make(chan error, len(cm.nodes))
	for _, node := range cm.nodes {
		wg.Add(1)
		go func(n *ClusterNode) {
			defer wg.Done()
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
		return fmt.Errorf("cluster pin errors (%d/%d): %v", len(errs), len(cm.nodes), errs)
	}
	return nil
}

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
		return fmt.Errorf("cluster pin errors (%d): %v", len(errs), errs)
	}
	return nil
}

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
		return fmt.Errorf("cluster unpin errors (%d): %v", len(errs), errs)
	}
	return nil
}

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
