package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/borg001/ipfs-filestorage/internal/config"
)

// A viewer's own public link is not a key to another file: the decision taken
// for the link stays with the link's file, and a CID it does not name is
// judged by its own policy.
func TestResolveSetsAsideAMediaLinkForAnotherFile(t *testing.T) {
	private := testCID("PrivatePhoto")
	own := testCID("OwnPublicPhoto")
	policy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/view/id/8916" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"item": map[string]interface{}{
				"delivery_mode": "original", "storage_uri": "ipfs://" + own,
			}})
			return
		}
		if got := r.URL.Query().Get("search"); got != private {
			t.Errorf("policy search = %q, want %q", got, private)
		}
		if r.URL.Query().Get("media_link") != "" {
			t.Errorf("the foreign media_link reached the CID policy")
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"rows": []map[string]interface{}{{
			"delivery_mode": "blur", "storage_uri": "ipfs://" + private,
		}}})
	}))
	defer policy.Close()

	resolver := newMediaAccessResolver(config.MediaAccessConfig{URL: policy.URL, TimeoutMs: 1000})
	request := httptest.NewRequest(http.MethodGet, "/file/"+private+"?media_link=8916&token=viewer-token", nil)
	decision, err := resolver.Resolve(request.Context(), request, private)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Mode != mediaDeliveryBlur {
		t.Fatalf("mode = %q, want blur: the own link must not open another file", decision.Mode)
	}

	// The link's own file is still served by the link's decision.
	request = httptest.NewRequest(http.MethodGet, "/file/"+own+"?media_link=8916&token=viewer-token", nil)
	decision, err = resolver.Resolve(request.Context(), request, own)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Mode != mediaDeliveryOriginal || decision.SourceCID != own {
		t.Fatalf("decision = %+v, want the link's original", decision)
	}
}

// A file placed somewhere and open to the viewer through none of its places
// is refused with 403, not served as an unmanaged original.
func TestHandleFileRefusesADeniedFile(t *testing.T) {
	cid := testCID("VerificationVideo")
	policy := policyServer(t, cid, []map[string]interface{}{{"delivery_mode": "deny"}})
	defer policy.Close()

	resolver := newMediaAccessResolver(config.MediaAccessConfig{URL: policy.URL, TimeoutMs: 1000})
	request := httptest.NewRequest(http.MethodGet, "/file/"+cid+"?token=viewer-token", nil)
	if _, err := resolver.Resolve(request.Context(), request, cid); !errors.Is(err, errMediaForbidden) {
		t.Fatalf("err = %v, want errMediaForbidden", err)
	}

	h := setupTestHandler(&config.Config{})
	h.mediaAccess = resolver
	w := httptest.NewRecorder()
	h.HandleFile(w, httptest.NewRequest(http.MethodGet, "/file/"+cid+"?token=viewer-token", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
}
