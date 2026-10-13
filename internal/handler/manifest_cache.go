package handler

import (
	"container/list"
	"context"
	"errors"
	"maps"
	"sync"

	"github.com/borg001/ipfs-filestorage/internal/bundle"
	"golang.org/x/sync/singleflight"
)

// Only immutable bundle metadata is shared. Permissions, grants, unpin state
// and variant overrides are checked by the handlers on every request.
// The byte budget accounts for strings and conservative map/entry overhead;
// it is not a limit on the Go process's total heap.
type manifestCache struct {
	mu           sync.Mutex
	entries      map[string]*list.Element
	lru          list.List
	bytes, limit int
	flights      singleflight.Group
}

type cachedManifest struct {
	cid   string
	value bundle.Manifest
	bytes int
}

func newManifestCache(limit int) *manifestCache {
	if limit <= 0 {
		return nil
	}
	return &manifestCache{entries: make(map[string]*list.Element), limit: limit}
}

func cloneManifest(m bundle.Manifest) bundle.Manifest {
	m.Variants = maps.Clone(m.Variants)
	m.Metadata = maps.Clone(m.Metadata)
	return m
}

func manifestCost(m bundle.Manifest) int {
	assetCost := func(a bundle.Asset) int {
		return 256 + len(a.CID) + len(a.Path) + len(a.BundlePath) + len(a.Format) + len(a.ContentType)
	}
	n := 512 + 2*len(m.CID) + len(m.Name) + len(m.Type) + assetCost(m.Original)
	for k, a := range m.Variants {
		n += len(k) + assetCost(a)
	}
	for k, v := range m.Metadata {
		n += 128 + len(k) + len(v)
	}
	return n
}

func (c *manifestCache) get(cid string) (bundle.Manifest, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[cid]
	if !ok {
		return bundle.Manifest{}, false
	}
	c.lru.MoveToFront(element)
	return element.Value.(cachedManifest).value, true
}

func (c *manifestCache) put(cid string, value bundle.Manifest) {
	cost := manifestCost(value)
	// Large manifests are still served, but cannot consume the shared cache.
	if cost > c.limit || cost > 256<<10 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[cid]; exists {
		return
	}
	for c.bytes+cost > c.limit || len(c.entries) >= 1024 {
		last := c.lru.Back()
		old := last.Value.(cachedManifest)
		delete(c.entries, old.cid)
		c.bytes -= old.bytes
		c.lru.Remove(last)
	}
	c.entries[cid] = c.lru.PushFront(cachedManifest{cid, value, cost})
	c.bytes += cost
}

func (c *manifestCache) load(ctx context.Context, cid string, fetch func(context.Context) (bundle.Manifest, error)) (bundle.Manifest, error) {
	if c == nil {
		return fetch(ctx)
	}
	for {
		if err := ctx.Err(); err != nil {
			return bundle.Manifest{}, err
		}
		if value, ok := c.get(cid); ok {
			return cloneManifest(value), nil
		}
		pending := c.flights.DoChan(cid, func() (any, error) {
			if value, ok := c.get(cid); ok {
				return value, nil
			}
			value, err := fetch(ctx)
			// Cluster failover may wrap an exhausted read as a generic error.
			if err != nil && ctx.Err() != nil {
				err = ctx.Err()
			}
			if err == nil {
				c.put(cid, value)
			}
			return value, err
		})
		select {
		case <-ctx.Done():
			return bundle.Manifest{}, ctx.Err()
		case result := <-pending:
			// Canceling the first caller must not fail the other live callers. A new
			// flight uses one of their contexts; no detached background fetch survives.
			if result.Shared && ctx.Err() == nil && (errors.Is(result.Err, context.Canceled) || errors.Is(result.Err, context.DeadlineExceeded)) {
				continue
			}
			if result.Err != nil {
				return bundle.Manifest{}, result.Err
			}
			return cloneManifest(result.Val.(bundle.Manifest)), nil
		}
	}
}
