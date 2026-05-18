package lua

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProvider_Disabled(t *testing.T) {
	p := NewProvider("", 0, 0, nil)
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
	p := NewProvider(script, 3000, 0, nil)

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
	p := NewProvider(script, 3000, 0, nil)

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
	p := NewProvider(script, 3000, 0, nil)

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
	p := NewProvider(script, 3000, 0, nil)

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
	p := NewProvider(script, 100, 0, nil)

	r := httptest.NewRequest("GET", "/", nil)
	_, err := p.Authorize(context.Background(), r)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestProvider_MemoryLimit(t *testing.T) {
	script := `
function authorize(req)
  local t = {}
  for i = 1, 10000000 do
    t[i] = string.rep("x", 100)
  end
  return true
end
`
	p := NewProvider(script, 5000, 1, nil) // 1MB limit

	r := httptest.NewRequest("GET", "/", nil)
	_, err := p.Authorize(context.Background(), r)
	if err == nil {
		t.Fatal("expected memory limit error")
	}
}

func TestProvider_EnvWhitelist(t *testing.T) {
	script := `
function authorize(req)
  local url = env.get("AUTH_SERVICE_URL")
  return url == "http://auth:8080"
end
`
	p := NewProvider(script, 3000, 0, map[string]string{
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
	p := NewProvider(script, 3000, 0, map[string]string{})

	r := httptest.NewRequest("GET", "/", nil)
	ok, err := p.Authorize(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("env.PATH should be blocked, expected false")
	}
}

func TestProvider_DofileBlocked(t *testing.T) {
	script := `
function authorize(req)
  dofile("/etc/passwd")
  return true
end
`
	p := NewProvider(script, 3000, 0, nil)

	r := httptest.NewRequest("GET", "/", nil)
	_, err := p.Authorize(context.Background(), r)
	if err == nil {
		t.Fatal("expected error when dofile is called")
	}
}

func TestProvider_LoadfileBlocked(t *testing.T) {
	script := `
function authorize(req)
  loadfile("/etc/passwd")
  return true
end
`
	p := NewProvider(script, 3000, 0, nil)

	r := httptest.NewRequest("GET", "/", nil)
	_, err := p.Authorize(context.Background(), r)
	if err == nil {
		t.Fatal("expected error when loadfile is called")
	}
}

func TestProvider_SSRFProtection(t *testing.T) {
	script := `
function authorize(req)
  local resp = request.get("http://127.0.0.1:5001/api/v0/id")
  return false
end
`
	p := NewProvider(script, 3000, 0, nil)

	r := httptest.NewRequest("GET", "/", nil)
	_, err := p.Authorize(context.Background(), r)
	if err == nil {
		t.Fatal("expected SSRF error for private IP")
	}
}

func TestProvider_RequestHTTP(t *testing.T) {
	// Mock auth server
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer valid-token" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"active": true}`))
		} else {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"active": false}`))
		}
	}))
	defer authServer.Close()

	// Extract host and port from test server URL for the whitelist
	script := `
function authorize(req)
  local token = req.headers["Authorization"]
  if not token then return false end

  local resp = request.get(env.get("AUTH_URL") .. "/auth/me", {
    headers = { Authorization = token }
  })

  if not resp then return false end
  local data = json.decode(resp.body)
  return data.active == true
end
`
	p := NewProvider(script, 5000, 0, map[string]string{
		"AUTH_URL": authServer.URL,
	})

	// Valid token
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer valid-token")
	ok, err := p.Authorize(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true for valid token")
	}

	// Invalid token
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.Header.Set("Authorization", "Bearer bad-token")
	ok2, err := p.Authorize(context.Background(), r2)
	if err != nil {
		t.Fatal(err)
	}
	if ok2 {
		t.Fatal("expected false for invalid token")
	}
}

func TestProvider_JSON(t *testing.T) {
	script := `
function authorize(req)
  local data = json.decode('{"active": true, "role": "admin"}')
  return data.active == true and data.role == "admin"
end
`
	p := NewProvider(script, 3000, 0, nil)

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
	p := NewProvider(script, 3000, 0, nil)

	r := httptest.NewRequest("GET", "/", nil)
	_, err := p.Authorize(context.Background(), r)
	if err == nil {
		t.Fatal("expected error for syntax error")
	}
}
