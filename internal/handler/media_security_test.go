package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

func TestMediaLinkCannotAuthorizeAnotherResource(t *testing.T) {
	for _, mode := range []string{"original", "blur", "blur_faces"} {
		t.Run(mode, func(t *testing.T) {
			policy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"item": map[string]interface{}{
					"delivery_mode": mode, "storage_uri": "video://" + testMasterCID, "poster_uri": "ipfs://" + testPosterCID,
					// Even a forged child list cannot enlarge the actual playlist tree.
					"metadata": map[string]interface{}{"stream_cids": []string{testMissingCID}},
				}})
			}))
			defer policy.Close()
			h := setupVideoTestHandler(t, &config.Config{})
			h.mediaAccess = newMediaAccessResolver(config.MediaAccessConfig{URL: policy.URL})
			cluster := h.cluster.(*mockCluster)
			cluster.files[testMasterCID] = []byte("#EXTM3U\n" + testLowCID + ".m3u8\n")
			cluster.files[testLowCID] = []byte("#EXTM3U\n" + testSegmentCID + ".m4s\n")
			cluster.files[testMissingCID] = []byte("secret foreign bytes")
			for _, tc := range []struct {
				path    string
				handler http.HandlerFunc
			}{
				{"/file/" + testMissingCID, h.HandleFile},
				{"/stream/" + testMissingCID + "/master.m3u8", h.HandleStreamMaster},
				{"/stream/segment/" + testMissingCID + ".m4s", h.HandleStreamSegment},
				{"/stream/segment/" + testMissingCID + ".jpg", h.HandleStreamSegment},
			} {
				w := httptest.NewRecorder()
				tc.handler(w, httptest.NewRequest("GET", tc.path+"?media_link=55", nil))
				if w.Code != 403 || strings.Contains(w.Body.String(), "secret foreign bytes") {
					t.Fatalf("%s: status %d body %q", tc.path, w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestMediaPolicyEmptyAndFailedAreDifferent(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   int
	}{
		{200, `{"rows":[]}`, 403}, {200, `{"item":null}`, 403},
		{400, `{"message":"Record not found"}`, 403}, {400, `{"message":"syntax error"}`, 503}, {403, `{}`, 403}, {404, `{}`, 403}, {500, `{}`, 503}, {200, `broken`, 503},
	} {
		policy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		h := setupVideoTestHandler(t, &config.Config{})
		h.mediaAccess = newMediaAccessResolver(config.MediaAccessConfig{URL: policy.URL})
		for _, link := range []bool{false, true} {
			w := httptest.NewRecorder()
			path := "/file/" + testMasterCID
			handler := h.HandleFile
			if link {
				path = "/file/link/55"
				handler = h.HandleFileLink
			}
			handler(w, httptest.NewRequest("GET", path, nil))
			if w.Code != tc.want {
				t.Errorf("%s policy=%d %s: got %d want %d", path, tc.status, tc.body, w.Code, tc.want)
			}
			if w.Header().Get("Cache-Control") != "private, no-store" {
				t.Error("denial must not be publicly cached")
			}
		}
		policy.Close()
	}
}

func TestFaceHiddenVideoDeniesAllStreamPaths(t *testing.T) {
	for _, mode := range []string{"blur", "blur_faces", "original"} {
		t.Run(mode, func(t *testing.T) {
			replacement := testCID("SafePoster")
			policy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				row := map[string]interface{}{"delivery_mode": mode, "storage_uri": "video://" + testMasterCID, "poster_uri": "ipfs://" + testPosterCID,
					"metadata": map[string]interface{}{"poster_aliases": map[string]interface{}{testPosterCID: map[string]interface{}{"blur": testPosterCID, "blur_faces": testPosterCID}}}}
				if strings.Contains(r.URL.Path, "/view/id/") {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"item": row})
				} else {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"rows": []interface{}{row}})
				}
			}))
			defer policy.Close()
			h := setupVideoTestHandler(t, &config.Config{})
			h.mediaAccess = newMediaAccessResolver(config.MediaAccessConfig{URL: policy.URL})
			cluster := h.cluster.(*mockCluster)
			cluster.files[testMasterCID] = []byte(testVideoMasterPosters(testPosterCID, replacement))
			cluster.files[testLowCID] = []byte("#EXTM3U\n" + testSegmentCID + ".m4s\n")
			cluster.files[testSegmentCID] = []byte("original-video")
			cluster.files[testPosterCID] = []byte("original-poster")
			cluster.files[replacement] = []byte("protected-poster")
			for _, path := range []string{"master.m3u8", "playlist/0.m3u8", "segment/0/0.m4s", "poster.jpg"} {
				w := httptest.NewRecorder()
				h.HandleStreamLink(w, httptest.NewRequest("GET", "/stream/link/55/"+path, nil))
				want := 403
				if mode == "original" || path == "poster.jpg" {
					want = 200
				}
				if w.Code != want {
					t.Fatalf("%s: got %d want %d: %s", path, w.Code, want, w.Body.String())
				}
				if path == "poster.jpg" && mode != "original" && w.Body.String() != "protected-poster" {
					t.Fatal("original poster leaked")
				}
			}
			for _, suffix := range []string{"", "?media_link=55"} {
				for _, ext := range []string{".m4s", ".jpg"} {
					w := httptest.NewRecorder()
					h.HandleStreamSegment(w, httptest.NewRequest("GET", "/stream/segment/"+testSegmentCID+ext+suffix, nil))
					want := 403
					if mode == "original" && ext != ".jpg" {
						want = 200
					}
					if w.Code != want {
						t.Fatalf("direct segment %s%s: got %d want %d", ext, suffix, w.Code, want)
					}
				}
			}
		})
	}
}

func TestMissingPolicyRequiresExplicitPublicMode(t *testing.T) {
	for _, cfg := range []config.MediaAccessConfig{
		{}, {URL: "not-a-url"}, {URL: "not-a-url", AllowUnmanaged: true},
	} {
		h := setupVideoTestHandler(t, &config.Config{})
		h.cfg.MediaAccess = cfg
		h.mediaAccess = newMediaAccessResolver(cfg)
		w := httptest.NewRecorder()
		h.HandleStreamMaster(w, httptest.NewRequest("GET", "/stream/"+testMasterCID+"/master.m3u8", nil))
		if w.Code != 503 {
			t.Fatalf("missing/invalid policy allowed delivery: %d", w.Code)
		}
	}
}

func TestLegacyHLSRootAuthorizesOnlyItsActualChildren(t *testing.T) {
	policy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("search") != testMasterCID {
			t.Error("policy must resolve the root, not an unregistered segment")
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"rows": []interface{}{map[string]interface{}{
			"storage_uri": "video://" + testMasterCID, "delivery_mode": "original",
		}}})
	}))
	defer policy.Close()
	h := setupVideoTestHandler(t, &config.Config{})
	h.mediaAccess = newMediaAccessResolver(config.MediaAccessConfig{URL: policy.URL})
	cluster := h.cluster.(*mockCluster)
	cluster.files[testMasterCID] = []byte("#EXTM3U\n" + testLowCID + ".m3u8\n")
	cluster.files[testLowCID] = []byte("#EXTM3U\n" + testSegmentCID + ".m4s\n")
	cluster.files[testSegmentCID] = []byte("authorized-segment")
	cluster.files[testMissingCID] = []byte("foreign-segment")
	w := httptest.NewRecorder()
	h.HandleStreamMaster(w, httptest.NewRequest("GET", "/stream/"+testMasterCID+"/master.m3u8?token=viewer-token", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "media_root="+testMasterCID) {
		t.Fatalf("root not propagated: %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		cid  string
		want int
	}{{testSegmentCID, 200}, {testMissingCID, 403}} {
		w := httptest.NewRecorder()
		h.HandleStreamSegment(w, httptest.NewRequest("GET", "/stream/segment/"+tc.cid+".m4s?media_root="+testMasterCID, nil))
		if w.Code != tc.want {
			t.Fatalf("segment %s got %d", tc.cid, w.Code)
		}
		if tc.want == 200 && w.Body.String() != "authorized-segment" {
			t.Fatal("wrong segment")
		}
		if tc.want == 403 && strings.Contains(w.Body.String(), "foreign-segment") {
			t.Fatal("foreign segment leaked")
		}
	}
}

func testVideoMasterPosters(original, protected string) string {
	return "#EXTM3U\n#EXT-X-IAMFREE-POSTER:SIZE=180x320,URI=\"../segment/" + original + ".jpg\"\n" +
		"#EXT-X-IAMFREE-POSTER:VARIANT=blur,SIZE=180x320,URI=\"../segment/" + protected + ".jpg\"\n" +
		"#EXT-X-IAMFREE-POSTER:VARIANT=blur_faces,SIZE=180x320,URI=\"../segment/" + protected + ".jpg\"\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=500000\n" + testLowCID + ".m3u8\n"
}
