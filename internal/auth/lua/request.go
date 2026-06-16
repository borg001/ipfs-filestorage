package lua

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	lua "github.com/yuin/gopher-lua"
)

// registerRequestLib exposes request.get/post/put/del inside the Lua VM.
// All HTTP requests are validated for SSRF protection — private IPs and
// cloud metadata endpoints are blocked.
func (p *Provider) registerRequestLib(L *lua.LState) {
	mod := L.NewTable()

	methods := []struct {
		name   string
		method string
	}{
		{"get", "GET"},
		{"post", "POST"},
		{"put", "PUT"},
		{"del", "DELETE"},
	}

	for _, m := range methods {
		name := m.name
		method := m.method
		L.SetField(mod, name, L.NewFunction(func(L *lua.LState) int {
			rawURL := L.CheckString(1)
			opts := L.OptTable(2, L.NewTable())

			if err := p.validateURL(rawURL); err != nil {
				// SSRF attempt — raise error so it propagates to Authorize()
				L.RaiseError("url blocked: %s", err.Error())
				return 0
			}

			resp, err := p.doRequest(method, rawURL, opts)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(respToTable(L, resp))
			return 1
		}))
	}

	L.SetGlobal("request", mod)
}

// validateURL blocks SSRF attempts: private IPs, link-local, loopback, cloud metadata.
func (p *Provider) validateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty host")
	}

	// Block cloud metadata endpoint
	if host == "169.254.169.254" {
		return fmt.Errorf("cloud metadata access blocked")
	}
	_, allowPrivate := p.allowedPrivateHosts[host]

	// Resolve hostname to IP
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("dns resolution failed for %s", host)
	}

	for _, ip := range ips {
		if isPrivateIP(ip) && !allowPrivate {
			return fmt.Errorf("private ip access blocked: %s", ip)
		}
	}

	return nil
}

// isPrivateIP checks if an IP is in a private/reserved range.
func isPrivateIP(ip net.IP) bool {
	privateRanges := []struct {
		cidr string
	}{
		{"10.0.0.0/8"},
		{"172.16.0.0/12"},
		{"192.168.0.0/16"},
		{"127.0.0.0/8"},
		{"169.254.0.0/16"},
		{"::1/128"},
		{"fc00::/7"},
		{"fe80::/10"},
	}

	for _, r := range privateRanges {
		_, network, err := net.ParseCIDR(r.cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func (p *Provider) doRequest(method, rawURL string, opts *lua.LTable) (*http.Response, error) {
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}

	// Read headers from opts.headers
	if ht, ok := opts.RawGetString("headers").(*lua.LTable); ok {
		ht.ForEach(func(k, v lua.LValue) {
			req.Header.Set(lua.LVAsString(k), lua.LVAsString(v))
		})
	}

	// Read body from opts.body (string)
	if body := lua.LVAsString(opts.RawGetString("body")); body != "" && body != "nil" {
		req.Body = io.NopCloser(newLuaStringReader(body))
		req.ContentLength = int64(len(body))
	}

	return p.httpClient.Do(req)
}

func respToTable(L *lua.LState, resp *http.Response) *lua.LTable {
	t := L.NewTable()

	L.SetField(t, "status", lua.LNumber(resp.StatusCode))

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	L.SetField(t, "body", lua.LString(string(body)))

	headers := L.NewTable()
	for k, vs := range resp.Header {
		for _, v := range vs {
			L.RawSet(headers, lua.LString(k), lua.LString(v))
		}
	}
	L.SetField(t, "headers", headers)

	return t
}

// luaStringReader wraps a string as an io.ReadCloser
type luaStringReader struct {
	data []byte
	pos  int
}

func newLuaStringReader(s string) *luaStringReader {
	return &luaStringReader{data: []byte(s)}
}

func (r *luaStringReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func (r *luaStringReader) Close() error {
	return nil
}
