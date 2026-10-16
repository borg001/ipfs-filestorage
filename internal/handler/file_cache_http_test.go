package handler

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
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
			// Inspect the wire: http.Client would hide an unexpected HEAD body.
			address, _ := url.Parse(server.URL)
			connection, err := net.DialTimeout("tcp", address.Host, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			connection.SetDeadline(time.Now().Add(2 * time.Second))
			fmt.Fprintf(connection, "HEAD /file/link/55?token=session&grant=%s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", url.QueryEscape(grant), address.Host)
			wire := bufio.NewReader(connection)
			head, err := http.ReadResponse(wire, &http.Request{Method: http.MethodHead})
			if err != nil {
				t.Fatal(err)
			}
			if head.StatusCode != 200 || head.ContentLength != int64(len(body)) {
				t.Fatalf("HEAD headers: %d %d", head.StatusCode, head.ContentLength)
			}
			extra, err := io.ReadAll(wire)
			if err != nil || len(extra) != 0 {
				t.Fatalf("HEAD sent body: bytes=%d err=%v", len(extra), err)
			}
		})
	}
}
