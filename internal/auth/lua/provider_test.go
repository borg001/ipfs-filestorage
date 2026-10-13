package lua

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderCompiledScriptIsolationAndRevocation(t *testing.T) {
	var calls atomic.Int64
	var revoked atomic.Bool
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if revoked.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"id":1}`)
	}))
	defer auth.Close()
	provider := NewProvider(`
local request = require("request")
local env = require("env")
local json = require("json")
counter = (counter or 0) + 1
function authorize(req)
  if counter ~= 1 then return false end
  local response = request.get(env.get("AUTH_SERVICE_URL") .. "/auth/me", {})
  return response.status == 200 and json.decode(response.body).id == 1
end`, 3000, map[string]string{"AUTH_SERVICE_URL": auth.URL})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := provider.Authorize(context.Background(), httptest.NewRequest("GET", "/file/example", nil))
			if !ok || err != nil {
				t.Errorf("isolated authorization: %v %v", ok, err)
			}
		}()
	}
	wg.Wait()
	revoked.Store(true)
	ok, err := provider.Authorize(context.Background(), httptest.NewRequest("GET", "/file/example", nil))
	if ok || err != nil || calls.Load() != 33 {
		t.Fatalf("authorization must execute on every request, including revocation: %v %v calls=%d", ok, err, calls.Load())
	}
}

func TestProviderRegistryGrowsForLargerScripts(t *testing.T) {
	script := `function count(n)
  if n == 0 then return 0 end
  local a,b,c,d,e = 1,1,1,1,1
  return a+b+c+d+e+count(n-1)
end
function authorize(req) return count(100) == 500 end`
	ok, err := NewProvider(script, 3000, nil).Authorize(context.Background(), httptest.NewRequest("GET", "/", nil))
	if !ok || err != nil {
		t.Fatalf("growing registry: %v %v", ok, err)
	}
}

func BenchmarkProviderAuthorize(b *testing.B) {
	p := NewProvider(`local json = require("json")
function authorize(req)
 local value = json.decode('{"active":true}')
 return value.active and req.headers["Authorization"] == "Bearer fixture"
end`, 3000, nil)
	r := httptest.NewRequest("GET", "/file/link/1/100x100", nil)
	r.Header.Set("Authorization", "Bearer fixture")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if ok, err := p.Authorize(context.Background(), r); !ok || err != nil {
			b.Fatal(ok, err)
		}
	}
}

func TestProvider_Disabled(t *testing.T) {
	p := NewProvider("", 0, nil)
	if p.Enabled() {
		t.Fatal("should be disabled with empty script")
	}
	ok, err := p.Authorize(context.Background(), httptest.NewRequest("GET", "/", nil))
	if ok || err != nil {
		t.Fatalf("disabled provider should return (false, nil), got (%v, %v)", ok, err)
	}
}

func TestProvider_AuthorizeTrue(t *testing.T) {
	script := `
function authorize(req)
  return req.headers["X-Allow"] == "yes"
end
`
	p := NewProvider(script, 3000, nil)

	r := httptest.NewRequest("GET", "/test", nil)
	r.Header.Set("X-Allow", "yes")
	ok, err := p.Authorize(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true")
	}
}

func TestProvider_AuthorizeFalse(t *testing.T) {
	script := `
function authorize(req)
  return false
end
`
	p := NewProvider(script, 3000, nil)

	r := httptest.NewRequest("GET", "/", nil)
	ok, err := p.Authorize(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected false")
	}
}

func TestProvider_NoAuthorizeFunction(t *testing.T) {
	script := `x = 1`
	p := NewProvider(script, 3000, nil)

	r := httptest.NewRequest("GET", "/", nil)
	_, err := p.Authorize(context.Background(), r)
	if err == nil {
		t.Fatal("expected error when authorize() not defined")
	}
}

func TestProvider_RequestFields(t *testing.T) {
	script := `
function authorize(req)
  if req.method ~= "POST" then return false end
  if req.path ~= "/upload" then return false end
  if req.query["token"] ~= "abc" then return false end
  return true
end
`
	p := NewProvider(script, 3000, nil)

	r := httptest.NewRequest("POST", "/upload?token=abc", nil)
	ok, err := p.Authorize(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true")
	}
}

func TestProvider_Timeout(t *testing.T) {
	script := `
function authorize(req)
  local i = 0
  while true do i = i + 1 end
  return true
end
`
	p := NewProvider(script, 100, nil)

	r := httptest.NewRequest("GET", "/", nil)
	_, err := p.Authorize(context.Background(), r)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestProvider_EnvWhitelist(t *testing.T) {
	script := `
function authorize(req)
  local url = env.get("AUTH_SERVICE_URL")
  return url == "http://auth:8080"
end
`
	p := NewProvider(script, 3000, map[string]string{
		"AUTH_SERVICE_URL": "http://auth:8080",
	})

	r := httptest.NewRequest("GET", "/", nil)
	ok, err := p.Authorize(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true")
	}
}

func TestProvider_EnvBlocked(t *testing.T) {
	script := `
function authorize(req)
  local path = env.get("PATH")
  return path ~= nil
end
`
	p := NewProvider(script, 3000, map[string]string{})

	r := httptest.NewRequest("GET", "/", nil)
	ok, err := p.Authorize(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("env.PATH should be blocked, expected false")
	}
}

func TestProvider_RequestHTTP(t *testing.T) {
	// Test that request library works with JSON decode.
	// Note: SSRF protection blocks private IPs, so we test the request+json
	// pipeline using a script that doesn't make HTTP calls to localhost.
	script := `
function authorize(req)
  local data = json.decode('{"active": true, "token": "ok"}')
  if data.active and data.token == "ok" then
    return true
  end
  return false
end
`
	p := NewProvider(script, 3000, nil)

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer valid-token")
	ok, err := p.Authorize(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true")
	}
}

func TestProvider_SSRFBlock(t *testing.T) {
	script := `
function authorize(req)
  local resp = request.get("http://127.0.0.1:9999/secret", {})
  if resp then return true end
  return false
end
`
	p := NewProvider(script, 3000, nil)

	r := httptest.NewRequest("GET", "/", nil)
	_, err := p.Authorize(context.Background(), r)
	// Should error because localhost is blocked by SSRF protection
	if err == nil {
		t.Fatal("expected error - SSRF should block localhost")
	}
}

func TestProvider_TrustedAuthHostAllowsPrivateAddress(t *testing.T) {
	p := NewProvider("", 3000, map[string]string{
		"AUTH_SERVICE_URL": "http://127.0.0.1:8080",
	})

	if err := p.validateURL("http://127.0.0.1:8080/api/session"); err != nil {
		t.Fatalf("trusted auth host should bypass private IP SSRF block: %v", err)
	}
}

func TestProvider_JSON(t *testing.T) {
	script := `
function authorize(req)
  local data = json.decode('{"active": true, "role": "admin"}')
  return data.active == true and data.role == "admin"
end
`
	p := NewProvider(script, 3000, nil)

	r := httptest.NewRequest("GET", "/", nil)
	ok, err := p.Authorize(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true")
	}
}

func TestProvider_SyntaxError(t *testing.T) {
	script := `function authorize(req) return end`
	p := NewProvider(script, 3000, nil)

	r := httptest.NewRequest("GET", "/", nil)
	_, err := p.Authorize(context.Background(), r)
	if err == nil {
		t.Fatal("expected error for syntax error")
	}
}

func TestProviderReusesConnectionsAcrossConcurrentBursts(t *testing.T) {
	const parallel = 16
	var connections atomic.Int64
	entered := make(chan struct{}, parallel)
	release := []chan struct{}{make(chan struct{}), make(chan struct{})}
	var round atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := round.Load()
		entered <- struct{}{}
		<-release[index]
		fmt.Fprint(w, `{"id":1}`)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	provider := NewProvider(`local request=require("request"); local env=require("env"); function authorize(req) local r=request.get(env.get("AUTH_SERVICE_URL"),{}); return r and r.status==200 end`, 3000, map[string]string{"AUTH_SERVICE_URL": server.URL})
	for phase := 0; phase < 2; phase++ {
		round.Store(int32(phase))
		var wg sync.WaitGroup
		for i := 0; i < parallel; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ok, err := provider.Authorize(context.Background(), httptest.NewRequest("GET", "/file/example", nil))
				if !ok || err != nil {
					t.Errorf("authorize: %v %v", ok, err)
				}
			}()
		}
		timeout := time.NewTimer(2 * time.Second)
		arrived := 0
	wait:
		for arrived < parallel {
			select {
			case <-entered:
				arrived++
			case <-timeout.C:
				break wait
			}
		}
		timeout.Stop()
		close(release[phase])
		wg.Wait()
		if arrived != parallel {
			t.Fatalf("only %d requests reached auth", arrived)
		}
	}
	if got := connections.Load(); got > parallel+2 {
		t.Fatalf("auth connections not reused: %d for two %d-request bursts", got, parallel)
	}
}

func TestProviderCancelsAuthHTTPWithCaller(t *testing.T) {
	entered, stopped := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(stopped) }))
	defer server.Close()
	provider := NewProvider(`local request=require("request"); local env=require("env"); function authorize(req) local r=request.get(env.get("AUTH_SERVICE_URL"),{}); return r and r.status==200 end`, 3000, map[string]string{"AUTH_SERVICE_URL": server.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { provider.Authorize(ctx, httptest.NewRequest("GET", "/file/example", nil)); close(done) }()
	<-entered
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Error("upstream HTTP outlived canceled caller")
	}
	<-done
}
