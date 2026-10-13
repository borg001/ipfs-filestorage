package ipfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func TestCatStreamsAndCancels(t *testing.T) {
	for _, closeEarly := range []bool{false, true} {
		t.Run(fmt.Sprintf("close=%t", closeEarly), func(t *testing.T) {
			stopped := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v0/cat" {
					t.Errorf("unexpected RPC: %s", r.URL.Path)
				}
				if r.URL.Query().Get("arg") != "/ipfs/QmYwAPJzv5CZsnAzt8auVZRnGiVdLU1NAXq3oYtDCwC6tJ/original" {
					t.Error("wrong file path")
				}
				w.Header().Set("Content-Type", "application/octet-stream")
				fmt.Fprint(w, "first")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(stopped)
			}))
			defer server.Close()
			client, err := New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stream, err := client.CatPath(ctx, "QmYwAPJzv5CZsnAzt8auVZRnGiVdLU1NAXq3oYtDCwC6tJ", "original")
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			first := make([]byte, 5)
			if _, err := io.ReadFull(stream, first); err != nil || string(first) != "first" {
				t.Fatalf("first chunk: %q %v", first, err)
			}
			if closeEarly {
				stream.Close()
			} else {
				cancel()
				if _, err := io.ReadAll(stream); !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			}
			select {
			case <-stopped:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream request not canceled")
			}
		})
	}
}

func TestCatPropagatesRPCErrorsAndStreamTrailers(t *testing.T) {
	for _, rpcError := range []bool{true, false} {
		t.Run(fmt.Sprintf("rpcError=%t", rpcError), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v0/cat" {
					t.Errorf("unexpected RPC: %s", r.URL.Path)
				}
				if rpcError {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprint(w, `{"Message":"this dag node is a directory","Code":0,"Type":"error"}`)
					return
				}
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Trailer", "X-Stream-Error")
				fmt.Fprint(w, "partial")
				w.(http.Flusher).Flush()
				w.Header().Set("X-Stream-Error", "missing block")
			}))
			defer server.Close()
			client, err := New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			stream, err := client.Cat(context.Background(), "QmYwAPJzv5CZsnAzt8auVZRnGiVdLU1NAXq3oYtDCwC6tJ")
			if rpcError {
				if err == nil || !strings.Contains(err.Error(), "directory") || stream != nil {
					t.Fatalf("RPC error lost: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			data, err := io.ReadAll(stream)
			if string(data) != "partial" || err == nil || !strings.Contains(err.Error(), "missing block") {
				t.Fatalf("stream error lost: %q %v", data, err)
			}
		})
	}
}
