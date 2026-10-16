package handler

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
)

// Immutable bytes only, after the handler has checked access, deletion and the
// current variant override. Incomplete reads, errors and oversized files never
// become cache entries. Cache failures do not fail the underlying download.
type fileCache struct {
	mu                        sync.Mutex
	dir                       string
	limit, bytes, objectLimit int64
	active                    int
	entries                   map[string]*list.Element
	lru                       list.List
}

type fileEntry struct {
	key     string
	size    int64
	readers int
}

func newFileCache(dir string, limit int64) (*fileCache, error) {
	if limit <= 0 {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	// Each instance owns its directory. Clear this disposable cache at startup,
	// including crash leftovers; never trust an unverified file after a crash.
	paths, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	owned := regexp.MustCompile(`^(?:[a-f0-9]{64}\.blob|partial-[0-9]+)$`)
	for _, path := range paths {
		if !path.IsDir() && owned.MatchString(path.Name()) {
			if err := os.Remove(filepath.Join(dir, path.Name())); err != nil {
				return nil, err
			}
		}
	}
	return &fileCache{dir: dir, limit: limit, objectLimit: min(limit, 2<<20), entries: make(map[string]*list.Element)}, nil
}

func (c *fileCache) remove(e *list.Element) bool {
	v := e.Value.(*fileEntry)
	if err := os.Remove(filepath.Join(c.dir, v.key+".blob")); err != nil && !os.IsNotExist(err) {
		return false
	}
	delete(c.entries, v.key)
	c.lru.Remove(e)
	c.bytes -= v.size
	return true
}

func (c *fileCache) evict() bool {
	for e := c.lru.Back(); e != nil; e = e.Prev() {
		if e.Value.(*fileEntry).readers == 0 {
			if c.remove(e) {
				return true
			}
		}
	}
	return false
}

type cacheHit struct {
	*os.File
	cache *fileCache
	entry *fileEntry
	once  sync.Once
	err   error
}

func (r *cacheHit) Close() error {
	r.once.Do(func() {
		r.err = r.File.Close()
		r.cache.mu.Lock()
		r.entry.readers--
		r.cache.mu.Unlock()
	})
	return r.err
}

func (c *fileCache) open(ctx context.Context, key string, fetch func() (io.ReadCloser, error)) (io.ReadCloser, error) {
	if c == nil {
		return fetch()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(key))
	key = hex.EncodeToString(digest[:])
	c.mu.Lock()
	if e := c.entries[key]; e != nil {
		f, err := os.Open(filepath.Join(c.dir, key+".blob"))
		if err == nil {
			c.lru.MoveToFront(e)
			v := e.Value.(*fileEntry)
			v.readers++
			c.mu.Unlock()
			return &cacheHit{File: f, cache: c, entry: v}, nil
		}
		if e.Value.(*fileEntry).readers == 0 {
			c.remove(e)
		}
	}
	c.mu.Unlock()
	r, err := fetch()
	if err != nil {
		return r, err
	}
	c.mu.Lock()
	// Reserve worst-case space for each writer; downloads beyond the bound
	// simply stream from IPFS. Active readers are never evicted, so their
	// disk space remains counted until Close, even with very slow clients.
	if c.active >= 8 || int64(c.active+1)*c.objectLimit > c.limit {
		c.mu.Unlock()
		return r, nil
	}
	for c.bytes+int64(c.active+1)*c.objectLimit > c.limit || c.lru.Len()+c.active >= 4096 {
		if !c.evict() {
			c.mu.Unlock()
			return r, nil
		}
	}
	f, err := os.CreateTemp(c.dir, "partial-")
	if err != nil {
		c.mu.Unlock()
		return r, nil
	}
	c.active++
	c.mu.Unlock()
	return &cacheFill{source: r, file: f, cache: c, ctx: ctx, key: key}, nil
}

type cacheFill struct {
	source io.ReadCloser
	file   *os.File
	cache  *fileCache
	ctx    context.Context
	key    string
	size   int64
}

func (r *cacheFill) finish(commit bool) {
	if r.file == nil {
		return
	}
	f := r.file
	r.file = nil
	if f.Close() != nil {
		commit = false
	}
	c := r.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active--
	if commit && r.ctx.Err() == nil && c.entries[r.key] == nil {
		// Entry slots and file space were reserved before creating the writer.
		if err := os.Rename(f.Name(), filepath.Join(c.dir, r.key+".blob")); err == nil {
			c.entries[r.key] = c.lru.PushFront(&fileEntry{key: r.key, size: r.size})
			c.bytes += r.size
			return
		}
	}
	_ = os.Remove(f.Name())
}

func (r *cacheFill) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	if r.file != nil {
		r.size += int64(n)
		if r.size > r.cache.objectLimit {
			r.finish(false)
		} else if n > 0 {
			written, writeErr := r.file.Write(p[:n])
			if writeErr != nil || written != n {
				r.finish(false)
			}
		}
		if err != nil {
			r.finish(err == io.EOF)
		}
	}
	return n, err
}

func (r *cacheFill) Close() error {
	r.finish(false)
	return r.source.Close()
}

// Complete cache files are immutable and their length was measured on fill.
// Supplying it avoids chunked encoding and lets net/http use the file's
// WriteTo/sendfile path. Authorization still runs before opening the file.
func setCachedFileLength(w http.ResponseWriter, reader io.Reader) {
	if hit, ok := reader.(*cacheHit); ok {
		w.Header().Set("Content-Length", strconv.FormatInt(hit.entry.size, 10))
	}
}

// Keep the concrete *os.File visible through a LimitedReader. Go 1.23's
// File.WriteTo fallback wraps it in a private type that net.sendFile cannot
// recognize. CopyN lets net/http.ReaderFrom pass the real file to TCPConn.
func copyCachedFile(w io.Writer, reader io.Reader) (int64, error) {
	if hit, ok := reader.(*cacheHit); ok {
		return io.CopyN(w, hit.File, hit.entry.size)
	}
	return io.Copy(w, reader)
}
