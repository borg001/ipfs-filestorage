package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestFileCacheCompleteReadsOnly(t *testing.T) {
	c, err := newFileCache(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	fetch := func() (io.ReadCloser, error) { calls.Add(1); return io.NopCloser(strings.NewReader("immutable")), nil }
	ctx := context.Background()
	r, _ := c.open(ctx, "cid/path", fetch)
	r.Read(make([]byte, 2))
	r.Close() // interrupted download must not be cached
	for i := 0; i < 2; i++ {
		r, err = c.open(ctx, "cid/path", fetch)
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil || string(b) != "immutable" {
			t.Fatalf("read: %q %v", b, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("fetches: %d", calls.Load())
	}
	// Same directory CID but a different variant cannot reuse original bytes.
	r, _ = c.open(ctx, "cid/mask", fetch)
	io.ReadAll(r)
	r.Close()
	if calls.Load() != 3 {
		t.Fatal("variants shared a cache entry")
	}
	if c.active != 0 {
		t.Fatal("writer reservation leaked")
	}
}

type failingFile struct{ done bool }

func (f *failingFile) Read(p []byte) (int, error) {
	if !f.done {
		f.done = true
		return copy(p, "partial"), errors.New("upstream failed")
	}
	return 0, io.EOF
}
func (*failingFile) Close() error { return nil }

func TestFileCacheFailureOversizeCancellationAndRestart(t *testing.T) {
	dir := t.TempDir()
	c, _ := newFileCache(dir, 64)
	for _, kind := range []string{"error", "large", "cancel"} {
		ctx, cancel := context.WithCancel(context.Background())
		fetch := func() (io.ReadCloser, error) {
			if kind == "error" {
				return &failingFile{}, nil
			}
			return io.NopCloser(strings.NewReader(strings.Repeat("x", 65))), nil
		}
		r, err := c.open(ctx, kind, fetch)
		if err != nil {
			t.Fatal(err)
		}
		if kind == "cancel" {
			cancel()
		}
		b, readErr := io.ReadAll(r)
		r.Close()
		cancel()
		if kind == "error" && readErr == nil {
			t.Fatal("upstream error lost")
		}
		if kind == "large" && len(b) != 65 {
			t.Fatal("large download truncated")
		}
		if len(c.entries) != 0 || c.active != 0 {
			t.Fatal("failed/incomplete read cached")
		}
		files, _ := os.ReadDir(dir)
		if len(files) != 0 {
			t.Fatal("partial file leaked")
		}
	}
	r, _ := c.open(context.Background(), "complete", func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("ok")), nil })
	io.ReadAll(r)
	r.Close()
	if len(c.entries) != 1 {
		t.Fatal("completed read not cached")
	}
	if _, err := newFileCache(dir, 64); err != nil {
		t.Fatal(err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatal("startup retained old files")
	}
}

func TestFileCacheConcurrentReadsAndDiskBudget(t *testing.T) {
	dir := t.TempDir()
	c, _ := newFileCache(dir, 256)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			want := strings.Repeat(fmt.Sprint(i%8), 64)
			for j := 0; j < 4; j++ {
				r, err := c.open(context.Background(), fmt.Sprint(i%8), func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(want)), nil })
				if err != nil {
					t.Error(err)
					return
				}
				b, err := io.ReadAll(r)
				r.Close()
				if err != nil || string(b) != want {
					t.Errorf("wrong cached body: %v", err)
				}
			}
		}(i)
	}
	wg.Wait()
	files, _ := os.ReadDir(dir)
	var bytes int64
	for _, f := range files {
		info, _ := f.Info()
		bytes += info.Size()
	}
	if bytes > c.limit || bytes != c.bytes || c.active != 0 {
		t.Fatalf("budget: disk=%d indexed=%d active=%d", bytes, c.bytes, c.active)
	}
}

func TestFileCacheRetainsBudgetForSlowReader(t *testing.T) {
	c, _ := newFileCache(t.TempDir(), 64)
	fetch := func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("small")), nil }
	r, _ := c.open(context.Background(), "a", fetch)
	io.ReadAll(r)
	r.Close()
	held, _ := c.open(context.Background(), "a", fetch)
	r, _ = c.open(context.Background(), "b", fetch)
	io.ReadAll(r)
	r.Close()
	if len(c.entries) != 1 {
		t.Fatal("evicted a file with an active reader")
	}
	b, err := io.ReadAll(held)
	held.Close()
	held.Close()
	if err != nil || string(b) != "small" {
		t.Fatalf("held file: %q %v", b, err)
	}
	r, _ = c.open(context.Background(), "b", fetch)
	io.ReadAll(r)
	r.Close()
	if len(c.entries) != 1 || c.active != 0 {
		t.Fatal("eviction/reservation broken after closing reader")
	}
}
