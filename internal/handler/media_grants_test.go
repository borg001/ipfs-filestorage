package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/bundle"
	"github.com/borg001/ipfs-filestorage/internal/config"
	"github.com/borg001/ipfs-filestorage/internal/mediagrant"
)

type grantTestState struct {
	checks int
	err    error
}

func (s *grantTestState) Check(context.Context, mediagrant.Claims) error { s.checks++; return s.err }
func (s *grantTestState) Begin(context.Context, string) (func(context.Context) error, error) {
	return func(context.Context) error { return nil }, s.err
}
func (s *grantTestState) Revoke(context.Context, string) error { return s.err }

func grantTestHandler(t *testing.T) (*Handler, *mediagrant.Codec, mediagrant.Claims, *grantTestState) {
	t.Helper()
	h := setupTestHandler(&config.Config{})
	codec, err := mediagrant.New("k1", "stand", map[string]string{"k1": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))})
	if err != nil {
		t.Fatal(err)
	}
	state := &grantTestState{}
	h.mediaGrants = &mediaGrantVerifier{codec: codec, state: state}
	h.mediaAccess = nil
	claim := mediagrant.Claims{Version: 1, Session: mediagrant.Session("session"), LinkID: 55, AssetID: 3, SourceCID: testCID("GrantImage"), Mode: "blur", Purpose: "gallery", Operations: []string{"image"}, IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(5 * time.Minute).Unix(), Epoch: "test", AssetVersion: "0", FileVersion: "0"}
	manifest := bundle.Manifest{CID: claim.SourceCID, Type: "image", Original: bundle.Asset{BundlePath: bundle.OriginalFilename, ContentType: "image/jpeg"}, Variants: map[string]bundle.Asset{"blur": {BundlePath: "privacy/blur.jpg", ContentType: "image/jpeg"}}}
	encoded, _ := json.Marshal(manifest)
	h.cluster.(*mockCluster).dirs[claim.SourceCID] = map[string][]byte{bundle.ManifestFilename: encoded, bundle.OriginalFilename: []byte("secret-original"), "privacy/blur.jpg": []byte("safe-blur")}
	return h, codec, claim, state
}

func TestGrantDownloadsProtectedBytesWithoutPolicyResolver(t *testing.T) {
	h, codec, claim, state := grantTestHandler(t)
	grant, err := codec.Seal(claim, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "/320x320", "/original", "/blur_faces"} {
		request := httptest.NewRequest("GET", "/file/link/55"+suffix+"?token=session&grant="+url.QueryEscape(grant), nil)
		w := httptest.NewRecorder()
		h.HandleFileLink(w, request)
		if w.Code != 200 || w.Body.String() != "safe-blur" {
			t.Fatalf("%s: %d %s", suffix, w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("capability response was publicly cacheable")
		}
	}
	if state.checks != 4 {
		t.Fatal("downloads did not check online revoke state")
	}
}

func TestInvalidOrRevokedGrantNeverFallsBackToPolicy(t *testing.T) {
	h, codec, claim, state := grantTestHandler(t)
	grant, _ := codec.Seal(claim, time.Now())
	for _, tc := range []struct {
		path, token, grant string
		status             int
	}{
		{"/file/link/56", "session", grant, 403},
		{"/file/link/55/bundle", "session", grant, 403},
		{"/file/link/55", "other-session", grant, 403},
		{"/file/link/55", "session", "", 403},
		{"/file/link/55", "session", grant + "x", 403},
		{"/file/" + claim.SourceCID, "session", grant, 403},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", tc.path+"?token="+tc.token+"&grant="+tc.grant, nil)
		if strings.HasPrefix(tc.path, "/file/link/") {
			h.HandleFileLink(w, r)
		} else {
			h.HandleFile(w, r)
		}
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		err    error
		status int
	}{{mediagrant.ErrRevoked, 403}, {errors.New("Redis unavailable"), 503}} {
		state.err = tc.err
		w := httptest.NewRecorder()
		h.HandleFileLink(w, httptest.NewRequest("GET", "/file/link/55?token=session&grant="+grant, nil))
		if w.Code != tc.status {
			t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestExpiredGrantHasRefreshCodeAndVideoPosterDoesNotAuthorizeStream(t *testing.T) {
	h, codec, claim, _ := grantTestHandler(t)
	claim.IssuedAt = time.Now().Add(-10 * time.Minute).Unix()
	claim.ExpiresAt = claim.IssuedAt + 300
	expired, _ := codec.Seal(claim, time.Unix(claim.IssuedAt, 0))
	w := httptest.NewRecorder()
	h.HandleFileLink(w, httptest.NewRequest("GET", "/file/link/55?token=session&grant="+expired, nil))
	if w.Code != 401 || !strings.Contains(w.Body.String(), "MEDIA_GRANT_EXPIRED") {
		t.Fatalf("expired: %d %s", w.Code, w.Body.String())
	}
	claim.IssuedAt = time.Now().Unix()
	claim.ExpiresAt = claim.IssuedAt + 300
	claim.Video = true
	claim.Mode = "blur_faces"
	claim.Operations = []string{"poster"}
	grant, err := codec.Seal(claim, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"master.m3u8", "playlist/0.m3u8", "segment/0/0.ts"} {
		w := httptest.NewRecorder()
		h.HandleStreamLink(w, httptest.NewRequest("GET", "/stream/link/55/"+path+"?token=session&grant="+grant, nil))
		if w.Code != 403 {
			t.Fatalf("stream accepted poster grant: %s %d", path, w.Code)
		}
	}
	suffix := playlistAuthSuffix(url.Values{"token": {"session"}, "grant": {grant}})
	if !strings.Contains(suffix, "grant=") {
		t.Fatal("HLS children lost the grant")
	}
}
