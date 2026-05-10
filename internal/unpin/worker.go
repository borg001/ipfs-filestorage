package unpin

import (
	"context"
	"log"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/ipfs"
	"github.com/borg001/ipfs-filestorage/internal/store"
)

// Worker фоновый сборщик мусора для unpin-списка
type Worker struct {
	cluster  *ipfs.ClusterManager
	store    *store.UnpinStore
	ttl      time.Duration
	interval time.Duration
	stopChan chan struct{}
	running  bool
}

// NewWorker создаёт новый worker
func NewWorker(cluster *ipfs.ClusterManager, store *store.UnpinStore, ttl time.Duration, interval time.Duration) *Worker {
	return &Worker{
		cluster:  cluster,
		store:    store,
		ttl:      ttl,
		interval: interval,
		stopChan: make(chan struct{}),
	}
}

// Start запускает фоновый сборщик
func (w *Worker) Start() {
	if w.running {
		return
	}
	w.running = true
	go w.loop()
	log.Printf("[unpin-worker] started with TTL=%v, interval=%v", w.ttl, w.interval)
}

// Stop останавливает worker
func (w *Worker) Stop() {
	if !w.running {
		return
	}
	close(w.stopChan)
	w.running = false
	log.Println("[unpin-worker] stopped")
}

func (w *Worker) loop() {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.gc()
		case <-w.stopChan:
			return
		}
	}
}

func (w *Worker) gc() {
	cutoff := time.Now().UTC().Add(-w.ttl)
	expired := w.store.Expired(cutoff)

	if len(expired) == 0 {
		return
	}

	log.Printf("[unpin-worker] found %d expired entries", len(expired))

	ctx := context.Background()
	for _, cid := range expired {
		if err := w.cluster.ClusterUnpinAll(ctx, cid); err != nil {
			log.Printf("[unpin-worker] failed to unpin %s: %v", cid, err)
			continue
		}
		w.store.Remove(cid)
		log.Printf("[unpin-worker] unpin %s done", cid)
	}
}
