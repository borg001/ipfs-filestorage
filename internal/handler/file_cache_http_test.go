package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/borg001/ipfs-filestorage/internal/bundle"
)

func TestCachedProtectedFileHTTPFramingAndSniffing(t *testing.T) {
	for _, contentType := range []string{"image/jpeg", ""} {
		t.Run(contentType, func(t *testing.T) {
			h, codec, claim, _ := grantTestHandler(t)
			var err error
			h.files, err = newFileCache(t.TempDir(), 8<<20)
			if err != nil {
				t.Fatal(err)
			}
			files := h.cluster.(*mockCluster).dirs[claim.SourceCID]
			var manifest bundle.Manifest
			if err := json.Unmarshal(files[bundle.ManifestFilename], &manifest); err != nil {
				t.Fatal(err)
			}
			variant := manifest.Variants["blur"]
			variant.ContentType = contentType
			manifest.Variants["blur"] = variant
			files[bundle.ManifestFilename], err = json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			body := bytes.Repeat([]byte("protected-mask\n"), 8192)
			files["privacy/blur.jpg"] = body
			grant, err := codec.Seal(claim, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(h.HandleFileLink))
			defer server.Close()
			for attempt := 0; attempt < 2; attempt++ {
				response, err := server.Client().Get(server.URL + "/file/link/55?token=session&grant=" + url.QueryEscape(grant))
				if err != nil {
					t.Fatal(err)
				}
				received, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil || response.StatusCode != 200 || !bytes.Equal(received, body) {
					t.Fatalf("download attempt=%d status=%d bytes=%d err=%v", attempt, response.StatusCode, len(received), err)
				}
				if response.Header.Get("Cache-Control") != "private, no-store" {
					t.Fatal("protected cache policy changed")
				}
				if contentType == "" && response.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
					t.Fatal("content sniffing changed")
				}
				if attempt == 1 && (response.ContentLength != int64(len(body)) || len(response.TransferEncoding) != 0) {
					t.Fatalf("warm file uses incorrect HTTP framing: length=%d transfer=%v", response.ContentLength, response.TransferEncoding)
				}
			}
		})
	}
}
