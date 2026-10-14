package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

func TestPolicyTransportReusesConnectionsWithoutCachingDecisions(t *testing.T) {
	var connections, calls atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		switch call {
		case 2:
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{}`)
		case 3:
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{}`)
		default:
			fmt.Fprintf(w, `{"item":{"delivery_mode":"original","storage_uri":"ipfs://%s"}}`, testMasterCID)
		}
		w.(http.Flusher).Flush()
		fmt.Fprint(w, strings.Repeat(" ", 8192))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	resolver := newMediaAccessResolver(config.MediaAccessConfig{URL: server.URL})
	defer resolver.client.CloseIdleConnections()
	for i := 0; i < 4; i++ {
		_, err := resolver.ResolveLink(context.Background(), httptest.NewRequest("GET", "/file/link/55", nil), "55")
		switch i {
		case 0, 3:
			if err != nil {
				t.Fatal(err)
			}
		case 1:
			if !errors.Is(err, errMediaAccessDenied) {
				t.Fatalf("denial lost: %v", err)
			}
		case 2:
			if err == nil || errors.Is(err, errMediaAccessDenied) {
				t.Fatalf("upstream failure lost: %v", err)
			}
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("authorization calls=%d", calls.Load())
	}
	if connections.Load() != 1 {
		t.Fatalf("connections=%d, want one reused TCP connection", connections.Load())
	}
}

func TestMediaUnavailableLogDoesNotExposePolicyURL(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	w := httptest.NewRecorder()
	writeMediaAccessError(w, fmt.Errorf("https://private/policy?token=secret: %w", context.DeadlineExceeded))
	if w.Code != 503 || !strings.Contains(output.String(), "category=timeout") {
		t.Fatalf("status=%d log=%q", w.Code, output.String())
	}
	if strings.Contains(output.String(), "secret") || strings.Contains(output.String(), "private") {
		t.Fatal("sensitive URL in diagnostic log")
	}
}
