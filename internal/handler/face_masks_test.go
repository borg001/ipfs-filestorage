package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/bundle"
	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/store"
)

// A photo keeps its address when its face mask is drawn again: the mask
// redrawn after upload is served in place of the one in the bundle.
func TestHandleFileServesARedrawnFaceMask(t *testing.T) {
	cid := testCID("MaskedFace")
	policy := policyServer(t, cid, []map[string]interface{}{{"delivery_mode": "blur_faces"}})
	defer policy.Close()

	h := setupTestHandler(&config.Config{})
	h.mediaAccess = newMediaAccessResolver(config.MediaAccessConfig{URL: policy.URL, TimeoutMs: 1000})
	overrides, err := store.NewVariantOverrides(filepath.Join(t.TempDir(), "variant-overrides.json"))
	if err != nil {
		t.Fatal(err)
	}
	h.variantOverrides = overrides
	cluster := h.cluster.(*mockCluster)
	manifestBytes, err := json.Marshal(bundle.Manifest{
		CID:      cid,
		Type:     "image",
		Original: bundle.Asset{BundlePath: bundle.OriginalFilename, ContentType: "image/jpeg"},
		Variants: map[string]bundle.Asset{
			config.PrivacyFaceBlurVariantKey: {BundlePath: "privacy/blur_faces.jpg", ContentType: "image/jpeg"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cluster.dirs[cid] = map[string][]byte{
		bundle.ManifestFilename:  manifestBytes,
		bundle.OriginalFilename:  []byte("original-image"),
		"privacy/blur_faces.jpg": []byte("hard-edged-mask"),
	}

	serve := func() []byte {
		req := httptest.NewRequest(http.MethodGet, "/file/"+cid+"/320x320?token=viewer-token", nil)
		w := httptest.NewRecorder()
		h.HandleFile(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	if got := serve(); !bytes.Equal(got, []byte("hard-edged-mask")) {
		t.Fatalf("before the redraw = %q, want the bundled mask", got)
	}

	added, err := cluster.ClusterAdd(context.Background(), "blur_faces.jpg", strings.NewReader("soft-edged-mask"))
	if err != nil {
		t.Fatal(err)
	}
	if err := overrides.Set(store.VariantOverrideKey(cid, config.PrivacyFaceBlurVariantKey), added.CID); err != nil {
		t.Fatal(err)
	}
	if got := serve(); !bytes.Equal(got, []byte("soft-edged-mask")) {
		t.Fatalf("after the redraw = %q, want the redrawn mask", got)
	}
}

// Redrawing writes to the cluster, so only the platform may ask for it.
func TestRefreshFaceMasksNeedsThePlatformKey(t *testing.T) {
	h := setupTestHandler(&config.Config{})
	req := httptest.NewRequest(http.MethodPost, "/admin/face-masks/refresh", strings.NewReader(`{"bundles":["`+testCID("AnyPhoto")+`"]}`))
	w := httptest.NewRecorder()
	h.HandleRefreshFaceMasks(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}
