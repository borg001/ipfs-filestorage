package ipfs

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestClusterReadBalancesNodesReusesConnectionsAndFailsOver(t *testing.T) {
	var connections [2]atomic.Int64
	var reads [2]atomic.Int64
	var failed atomic.Bool
	urls := []string{}
	for i := 0; i < 2; i++ {
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if i == 0 && failed.Load() {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(500)
				fmt.Fprint(w, `{"Message":"unavailable","Code":0,"Type":"error"}`)
				return
			}
			switch r.URL.Path {
			case "/api/v0/files/stat":
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"Type":"file","Size":4}`)
			case "/api/v0/cat":
				reads[i].Add(1)
				fmt.Fprint(w, "data")
			default:
				t.Errorf("unexpected IPFS endpoint %s", r.URL.Path)
				w.WriteHeader(404)
			}
		}))
		server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
			if state == http.StateNew {
				connections[i].Add(1)
			}
		}
		server.Start()
		t.Cleanup(server.Close)
		urls = append(urls, server.URL)
	}
	cluster := NewCluster(urls)
	cid := "QmYwAPJzv5CZsnAzt8auVZRnGiVdLU1NAXq3oYtDCwC6tJ"
	read := func(index int) {
		t.Helper()
		var stream io.ReadCloser
		var err error
		if index%2 == 0 {
			stream, err = cluster.ClusterTryFetch(context.Background(), cid)
		} else {
			stream, err = cluster.ClusterTryFetchPath(context.Background(), cid, "original")
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(stream)
		stream.Close()
		if err != nil || string(body) != "data" {
			t.Fatalf("read: %q %v", body, err)
		}
	}
	for i := 0; i < 20; i++ {
		read(i)
	}
	if reads[0].Load() != 10 || reads[1].Load() != 10 {
		t.Fatalf("unbalanced reads: %d/%d", reads[0].Load(), reads[1].Load())
	}
	if connections[0].Load() > 2 || connections[1].Load() > 2 {
		t.Fatalf("connections not reused: %d/%d", connections[0].Load(), connections[1].Load())
	}
	failed.Store(true)
	for i := 0; i < 4; i++ {
		read(i)
	}
	if reads[1].Load() != 14 {
		t.Fatal("healthy node did not serve every read after failover")
	}
}
