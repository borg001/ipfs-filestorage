package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/bundle"
	"github.com/borg001/ipfs-filestorage/internal/config"
)

func policyServer(t *testing.T, expectedCID string, rows []map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("search"); got != expectedCID {
			t.Errorf("policy search = %q, want %q", got, expectedCID)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer viewer-token" {
			t.Errorf("policy Authorization = %q, want forwarded bearer token", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"rows": rows})
	}))
}

func TestHandleFileUsesProtectedImageVariantFromPolicy(t *testing.T) {
	cid := testCID("PrivateImage")
	policy := policyServer(t, cid, []map[string]interface{}{{"delivery_mode": "blur"}})
	defer policy.Close()

	h := setupTestHandler(&config.Config{})
	h.mediaAccess = newMediaAccessResolver(config.MediaAccessConfig{URL: policy.URL, TimeoutMs: 1000})
	cluster := h.cluster.(*mockCluster)
	manifest := bundle.Manifest{
		CID:      cid,
		Type:     "image",
		Original: bundle.Asset{BundlePath: bundle.OriginalFilename, ContentType: "image/jpeg"},
		Variants: map[string]bundle.Asset{
			config.PrivacyBlurVariantKey: {BundlePath: "privacy/blur.jpg", ContentType: "image/jpeg"},
		},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	cluster.dirs[cid] = map[string][]byte{
		bundle.ManifestFilename: manifestBytes,
		bundle.OriginalFilename: []byte("original-image"),
		"privacy/blur.jpg":      []byte("blurred-image"),
	}

	req := httptest.NewRequest(http.MethodGet, "/file/"+cid+"/320x320?token=viewer-token", nil)
	w := httptest.NewRecorder()
	h.HandleFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), []byte("blurred-image")) {
		t.Fatalf("protected response = %q, want blurred rendition", w.Body.Bytes())
	}
	if got := w.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q, want private no-store", got)
	}
}

func TestMediaPolicyForwardsGalleryLinkContext(t *testing.T) {
	policy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("media_link"); got != "55" {
			t.Fatalf("media_link = %q, want 55", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"rows": []map[string]interface{}{}})
	}))
	defer policy.Close()

	resolver := newMediaAccessResolver(config.MediaAccessConfig{URL: policy.URL, TimeoutMs: 1000})
	request := httptest.NewRequest(http.MethodGet, "/file/"+testCID("LinkContext")+"/320x320?media_link=55", nil)
	decision, err := resolver.Resolve(request.Context(), request, testCID("LinkContext"))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Managed {
		t.Fatal("empty policy response must keep ordinary media unmanaged")
	}
}

func TestMediaPolicyResolvesOpaqueLinkWithoutCIDInBrowserRequest(t *testing.T) {
	sourceCID := testCID("OpaqueSource")
	posterCID := testCID("OpaquePoster")
	policy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("search"); got != "" {
			t.Fatalf("opaque link policy must not receive search CID, got %q", got)
		}
		if got := r.URL.Query().Get("media_link"); got != "77" {
			t.Fatalf("media_link = %q, want 77", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer viewer-token" {
			t.Fatalf("policy Authorization = %q, want forwarded bearer token", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"rows": []map[string]interface{}{{
			"delivery_mode": "blur_faces",
			"storage_uri":   "ipfs://" + sourceCID,
			"poster_uri":    "ipfs://" + posterCID,
		}}})
	}))
	defer policy.Close()

	resolver := newMediaAccessResolver(config.MediaAccessConfig{URL: policy.URL, TimeoutMs: 1000})
	request := httptest.NewRequest(http.MethodGet, "/file/link/77/320x320?token=viewer-token", nil)
	decision, err := resolver.ResolveLink(request.Context(), request, "77")
	if err != nil {
		t.Fatal(err)
	}
	if decision.SourceCID != sourceCID || decision.PosterCID != posterCID {
		t.Fatalf("opaque policy source/poster = %q/%q, want %q/%q", decision.SourceCID, decision.PosterCID, sourceCID, posterCID)
	}
	if decision.Mode != mediaDeliveryBlurFaces || !decision.Managed {
		t.Fatalf("opaque policy decision = %#v, want managed face blur", decision)
	}
}

func TestHandleFileLinkServesProtectedRenditionWithoutCIDInRequest(t *testing.T) {
	sourceCID := testCID("OpaqueFileSource")
	policy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("search"); got != "" {
			t.Fatalf("opaque file policy unexpectedly received CID %q", got)
		}
		if got := r.URL.Query().Get("media_link"); got != "91" {
			t.Fatalf("media_link = %q, want 91", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"rows": []map[string]interface{}{{
			"delivery_mode": "blur",
			"storage_uri":   "ipfs://" + sourceCID,
		}}})
	}))
	defer policy.Close()

	h := setupTestHandler(&config.Config{})
	h.mediaAccess = newMediaAccessResolver(config.MediaAccessConfig{URL: policy.URL, TimeoutMs: 1000})
	manifest := bundle.Manifest{
		CID:      sourceCID,
		Type:     "image",
		Original: bundle.Asset{BundlePath: bundle.OriginalFilename, ContentType: "image/jpeg"},
		Variants: map[string]bundle.Asset{config.PrivacyBlurVariantKey: {BundlePath: "privacy/blur.jpg", ContentType: "image/jpeg"}},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	cluster := h.cluster.(*mockCluster)
	cluster.dirs[sourceCID] = map[string][]byte{
		bundle.ManifestFilename: manifestBytes,
		bundle.OriginalFilename: []byte("original"),
		"privacy/blur.jpg":      []byte("blurred"),
	}

	request := httptest.NewRequest(http.MethodGet, "/file/link/91/320x320?token=viewer-token", nil)
	response := httptest.NewRecorder()
	h.HandleFileLink(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("opaque file status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if !bytes.Equal(response.Body.Bytes(), []byte("blurred")) {
		t.Fatalf("opaque file response = %q, want protected rendition", response.Body.Bytes())
	}
}

func TestVideoPolicyProtectsMasterSegmentsAndPosters(t *testing.T) {
	cfg := &config.Config{Video: config.VideoConfig{TempDir: t.TempDir()}}

	t.Run("private master and direct segment are denied", func(t *testing.T) {
		h := setupVideoTestHandler(t, cfg)
		cluster := h.cluster.(*mockCluster)
		cluster.files[testMasterCID] = []byte("#EXTM3U\n")
		cluster.files[testSegmentCID] = []byte("original-segment")

		masterPolicy := policyServer(t, testMasterCID, []map[string]interface{}{{"delivery_mode": "blur"}})
		defer masterPolicy.Close()
		h.mediaAccess = newMediaAccessResolver(config.MediaAccessConfig{URL: masterPolicy.URL, TimeoutMs: 1000})
		masterReq := httptest.NewRequest(http.MethodGet, "/stream/"+testMasterCID+"/master.m3u8?token=viewer-token", nil)
		masterW := httptest.NewRecorder()
		h.HandleStreamMaster(masterW, masterReq)
		if masterW.Code != http.StatusForbidden {
			t.Fatalf("master status = %d, want 403", masterW.Code)
		}

		segmentPolicy := policyServer(t, testSegmentCID, []map[string]interface{}{{"delivery_mode": "blur"}})
		defer segmentPolicy.Close()
		h.mediaAccess = newMediaAccessResolver(config.MediaAccessConfig{URL: segmentPolicy.URL, TimeoutMs: 1000})
		segmentReq := httptest.NewRequest(http.MethodGet, "/stream/segment/"+testSegmentCID+".m4s?token=viewer-token", nil)
		segmentW := httptest.NewRecorder()
		h.HandleStreamSegment(segmentW, segmentReq)
		if segmentW.Code != http.StatusForbidden {
			t.Fatalf("segment status = %d, want 403", segmentW.Code)
		}
	})

	t.Run("protected poster is replaced using API metadata", func(t *testing.T) {
		h := setupVideoTestHandler(t, cfg)
		cluster := h.cluster.(*mockCluster)
		blurPosterCID := testCID("BlurPoster")
		cluster.files[testPosterCID] = []byte("original-poster")
		cluster.files[blurPosterCID] = []byte("blurred-poster")
		policy := policyServer(t, testPosterCID, []map[string]interface{}{{
			"delivery_mode": "blur",
			"metadata": map[string]interface{}{
				"poster_aliases": map[string]interface{}{
					testPosterCID: map[string]interface{}{"blur": blurPosterCID},
				},
			},
		}})
		defer policy.Close()
		h.mediaAccess = newMediaAccessResolver(config.MediaAccessConfig{URL: policy.URL, TimeoutMs: 1000})

		req := httptest.NewRequest(http.MethodGet, "/stream/segment/"+testPosterCID+".jpg?token=viewer-token", nil)
		w := httptest.NewRecorder()
		h.HandleStreamSegment(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("poster status = %d, want 200: %s", w.Code, w.Body.String())
		}
		if !bytes.Equal(w.Body.Bytes(), []byte("blurred-poster")) {
			t.Fatalf("poster response = %q, want blurred poster", w.Body.Bytes())
		}
	})
}
