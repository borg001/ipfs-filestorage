package handler

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/ipfs"
)

func TestManifestReadsReleaseChunkedIPFSConnectionsForReuse(t *testing.T) {
	var connections atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v0/files/stat":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"Type":"file","Size":131102}`)
		case "/api/v0/cat":
			fmt.Fprint(w, `{"name":"photo","type":"image"}`)
			w.(http.Flusher).Flush()
			// Legal JSON whitespace in a separate chunk: Decode alone does not
			// consume the response to EOF and Close then discards the TCP socket.
			fmt.Fprint(w, strings.Repeat(" ", 128<<10))
		default:
			t.Errorf("unexpected IPFS request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	h := &Handler{cluster: ipfs.NewCluster([]string{server.URL})}
	const cid = "QmYwAPJzv5CZsnAzt8auVZRnGiVdLU1NAXq3oYtDCwC6tJ"
	for i := 0; i < 10; i++ {
		manifest, err := h.readManifest(context.Background(), cid)
		if err != nil || manifest.Name != "photo" || manifest.CID != cid {
			t.Fatalf("manifest changed: name=%q cid=%q err=%v", manifest.Name, manifest.CID, err)
		}
	}
	if n := connections.Load(); n > 2 {
		t.Fatalf("10 manifest reads opened %d connections; response bodies were not drained", n)
	}
}
