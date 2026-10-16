package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/bundle"
	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/mediagrant"
	"github.com/borg001/ipfs-filestorage/internal/store"
)

func TestFileCacheStillChecksPolicyDeletionAndMaskReplacement(t *testing.T) {
	cid := testCID("DiskCachePolicy")
	var mode atomic.Value
	mode.Store("original")
	policy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode.Load().(string) == "deny" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"rows": []map[string]any{{"storage_uri": "ipfs://" + cid, "delivery_mode": mode.Load().(string)}}})
	}))
	defer policy.Close()
	h := setupTestHandler(&config.Config{})
	var err error
	h.files, err = newFileCache(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	h.unpinStore, err = store.NewUnpinStore(filepath.Join(t.TempDir(), "unpin.json"))
	if err != nil {
		t.Fatal(err)
	}
	h.variantOverrides, err = store.NewVariantOverrides(filepath.Join(t.TempDir(), "variants.json"))
	if err != nil {
		t.Fatal(err)
	}
	h.mediaAccess = newMediaAccessResolver(config.MediaAccessConfig{URL: policy.URL, TimeoutMs: 1000})
	m := bundle.Manifest{CID: cid, Type: "image", Original: bundle.Asset{BundlePath: bundle.OriginalFilename, ContentType: "image/jpeg"}, Variants: map[string]bundle.Asset{"blur": {BundlePath: "blur.jpg", ContentType: "image/jpeg"}}}
	b, _ := json.Marshal(m)
	cluster := h.cluster.(*mockCluster)
	cluster.dirs[cid] = map[string][]byte{bundle.ManifestFilename: b, bundle.OriginalFilename: []byte("original"), "blur.jpg": []byte("mask")}
	get := func(want int, body string) {
		t.Helper()
		w := httptest.NewRecorder()
		h.HandleFile(w, httptest.NewRequest("GET", "/file/"+cid+"?token=viewer-token", nil))
		if w.Code != want || (want == 200 && w.Body.String() != body) {
			t.Fatalf("response: %d %q", w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("protected cache headers changed")
		}
	}
	get(200, "original")
	delete(cluster.dirs[cid], bundle.OriginalFilename)
	get(200, "original") // proven warm cache hit
	mode.Store("deny")
	get(403, "")
	mode.Store("blur")
	get(200, "mask")
	get(200, "mask")
	newCID := testCID("NewMask")
	cluster.files[newCID] = []byte("replacement-mask")
	if err := h.variantOverrides.Set(store.VariantOverrideKey(cid, "blur"), newCID); err != nil {
		t.Fatal(err)
	}
	get(200, "replacement-mask")
	h.unpinStore.Add(cid)
	get(404, "")
}

func TestFileCacheWarmGrantStillChecksRevocationAndSession(t *testing.T) {
	h, codec, claim, state := grantTestHandler(t)
	var err error
	h.files, err = newFileCache(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := codec.Seal(claim, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	get := func(token string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		h.HandleFileLink(w, httptest.NewRequest("GET", "/file/link/55?token="+token+"&grant="+grant, nil))
		if w.Code != want {
			t.Fatalf("status: %d, want %d", w.Code, want)
		}
		if want == 200 && w.Body.String() != "safe-blur" {
			t.Fatal("wrong protected bytes")
		}
	}
	get("session", 200)
	delete(h.cluster.(*mockCluster).dirs[claim.SourceCID], "privacy/blur.jpg")
	get("session", 200)
	get("other-session", 403)
	state.err = mediagrant.ErrRevoked
	get("session", 403)
	state.err = errors.New("Redis unavailable")
	get("session", 503)
}
