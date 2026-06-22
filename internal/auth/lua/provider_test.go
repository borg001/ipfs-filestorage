package lua

import (
	"context"
	"net/http/httptest"
	"testing"
)

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
