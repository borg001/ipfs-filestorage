package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/bundle"
	"github.com/borg001/ipfs-filestorage/internal/config"
)

func TestMediaAgainstRealPolicy(t *testing.T) {
	base := os.Getenv("MEDIA_E2E_POLICY_URL")
	if base == "" {
		t.Skip("launched by API TestMediaStorageEndToEnd")
	}
	h := setupVideoTestHandler(t, &config.Config{})
	h.mediaAccess = newMediaAccessResolver(config.MediaAccessConfig{URL: base + "/api/media_delivery", LinkURL: base + "/internal/api/media_delivery_link", TimeoutMs: 3000})
	cluster := h.cluster.(*mockCluster)
	for _, seed := range []string{"Private", "Public"} {
		cid := testCID(seed)
		manifest := bundle.Manifest{CID: cid, Type: "image", Original: bundle.Asset{BundlePath: bundle.OriginalFilename, ContentType: "image/jpeg"}, Variants: map[string]bundle.Asset{"blur": {BundlePath: "privacy/blur.jpg", ContentType: "image/jpeg"}}}
		encoded, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		cluster.dirs[cid] = map[string][]byte{bundle.ManifestFilename: encoded, bundle.OriginalFilename: []byte(seed + "-original"), "privacy/blur.jpg": []byte(seed + "-blur")}
	}
	safePoster := testCID("SafePoster")
	cluster.files[testMasterCID] = []byte(testVideoMasterPosters(testPosterCID, safePoster))
	cluster.files[testCID("Verify")] = []byte(testVideoMasterPosters(testPosterCID, safePoster))
	cluster.files[testLowCID] = []byte("#EXTM3U\n" + testSegmentCID + ".m4s\n")
	cluster.files[testSegmentCID] = []byte("video-original")
	cluster.files[testPosterCID] = []byte("poster-original")
	cluster.files[safePoster] = []byte("poster-protected")
	for _, tc := range []struct {
		name, path, token, body string
		status                  int
	}{
		{"private viewer", "/file/link/1", "viewer-token", "Private-blur", 200},
		{"private owner", "/file/link/1", "owner-token", "Private-original", 200},
		{"CID same rules", "/file/" + testCID("Private"), "viewer-token", "Private-blur", 200},
		{"foreign public link", "/file/" + testCID("Private") + "?media_link=2", "viewer-token", "", 403},
		{"foreign bundle", "/file/" + testCID("Private") + "/bundle?media_link=2", "viewer-token", "", 403},
		{"verification link viewer", "/stream/link/3/master.m3u8", "viewer-token", "", 403},
		{"verification CID viewer", "/stream/" + testCID("Verify") + "/master.m3u8", "viewer-token", "", 403},
		{"verification agency", "/stream/link/3/master.m3u8", "agency-token", "", 403},
		{"verification owner", "/stream/link/3/master.m3u8", "owner-token", "#EXTM3U", 200},
		{"verification staff", "/stream/link/3/master.m3u8", "staff-token", "#EXTM3U", 200},
		{"hidden face master", "/stream/link/4/master.m3u8", "viewer-token", "", 403},
		{"hidden face segment", "/stream/link/4/segment/0/0.m4s", "viewer-token", "", 403},
		{"hidden face poster", "/stream/link/4/poster.jpg", "viewer-token", "poster-protected", 200},
		{"video owner", "/stream/link/4/segment/0/0.m4s", "owner-token", "video-original", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", tc.path, nil)
			request.Header.Set("Authorization", "Bearer "+tc.token)
			request.Header.Set("Range", "bytes=0-")
			response := httptest.NewRecorder()
			switch {
			case strings.HasPrefix(tc.path, "/file/link/"):
				h.HandleFileLink(response, request)
			case strings.HasPrefix(tc.path, "/file/"):
				h.HandleFile(response, request)
			case strings.HasPrefix(tc.path, "/stream/link/"):
				h.HandleStreamLink(response, request)
			default:
				h.HandleStreamMaster(response, request)
			}
			if response.Code != tc.status {
				t.Fatalf("got %d want %d: %s", response.Code, tc.status, response.Body.String())
			}
			if tc.status == http.StatusOK {
				if tc.body == "#EXTM3U" {
					if !strings.HasPrefix(response.Body.String(), tc.body) {
						t.Fatal("not a playlist")
					}
				} else if response.Body.String() != tc.body {
					t.Fatalf("wrong bytes: %q", response.Body.String())
				}
				if response.Header().Get("Cache-Control") != "private, no-store" {
					t.Fatal("protected bytes publicly cacheable")
				}
			} else if strings.Contains(response.Body.String(), "-original") {
				t.Fatal("denial leaked original bytes")
			}
		})
	}
}
