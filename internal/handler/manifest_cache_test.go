package handler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/bundle"
)

func TestManifestCacheIsBoundedAndReturnsIndependentMaps(t *testing.T) {
	sample := bundle.Manifest{CID: "a", Variants: map[string]bundle.Asset{"small": {BundlePath: "small.jpg"}}, Metadata: map[string]string{"name": "original"}}
	cache := newManifestCache(2 * manifestCost(sample))
	calls := 0
	fetch := func(context.Context) (bundle.Manifest, error) { calls++; return cloneManifest(sample), nil }
	first, err := cache.load(context.Background(), "a", fetch)
	if err != nil {
		t.Fatal(err)
	}
	first.Variants["small"] = bundle.Asset{BundlePath: "bad"}
	first.Metadata["name"] = "bad"
	second, _ := cache.load(context.Background(), "a", fetch)
	if calls != 1 || second.Variants["small"].BundlePath != "small.jpg" || second.Metadata["name"] != "original" {
		t.Fatal("cache value leaked mutable maps")
	}
	for _, cid := range []string{"b", "c"} {
		sample.CID = cid
		cache.load(context.Background(), cid, fetch)
	}
	if _, found := cache.get("a"); found {
		t.Fatal("LRU entry not evicted")
	}
	if cache.bytes > cache.limit || len(cache.entries) != 2 {
		t.Fatal("cache exceeded budget")
	}
	sample.Metadata["big"] = string(make([]byte, 256<<10))
	cache.load(context.Background(), "large", fetch)
	if _, found := cache.get("large"); found {
		t.Fatal("oversized entry cached")
	}
}

func TestManifestCacheRetriesErrorsAndCanBeDisabled(t *testing.T) {
	for _, cache := range []*manifestCache{nil, newManifestCache(8 << 20)} {
		calls := 0
		fetch := func(context.Context) (bundle.Manifest, error) {
			calls++
			if calls == 1 {
				return bundle.Manifest{}, errors.New("missing block")
			}
			return bundle.Manifest{CID: "a"}, nil
		}
		if _, err := cache.load(context.Background(), "a", fetch); err == nil {
			t.Fatal("lost upstream error")
		}
		if _, err := cache.load(context.Background(), "a", fetch); err != nil {
			t.Fatal("error was cached")
		}
		cache.load(context.Background(), "a", fetch)
		if cache == nil && calls != 3 || cache != nil && calls != 2 {
			t.Fatalf("unexpected fetch count: %d", calls)
		}
	}
}

func TestManifestCacheCoalescesConcurrentMisses(t *testing.T) {
	cache := newManifestCache(8 << 20)
	var calls atomic.Int64
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := cache.load(context.Background(), "a", func(context.Context) (bundle.Manifest, error) {
				calls.Add(1)
				entered <- struct{}{}
				<-release
				return bundle.Manifest{CID: "a"}, nil
			})
			if err != nil || m.CID != "a" {
				t.Errorf("load: %v", err)
			}
		}()
	}
	<-entered
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("duplicate fetches: %d", calls.Load())
	}
}

func TestManifestCacheCanceledLeaderDoesNotPoisonWaiter(t *testing.T) {
	cache := newManifestCache(8 << 20)
	leader, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := cache.load(leader, "a", func(ctx context.Context) (bundle.Manifest, error) {
			close(entered)
			<-ctx.Done()
			return bundle.Manifest{}, ctx.Err()
		})
		done <- err
	}()
	<-entered
	waiter, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	live := make(chan error, 1)
	go func() {
		m, err := cache.load(waiter, "a", func(context.Context) (bundle.Manifest, error) { return bundle.Manifest{CID: "a"}, nil })
		if err == nil && m.CID != "a" {
			err = fmt.Errorf("wrong CID")
		}
		live <- err
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader: %v", err)
	}
	if err := <-live; err != nil {
		t.Fatalf("live waiter: %v", err)
	}
}
