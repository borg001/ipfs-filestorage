package lua

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	lua "github.com/yuin/gopher-lua"
)

const defaultMaxMemoryMB = 32

// Provider executes a sandboxed Lua script to validate requests.
// If no script is configured, Authorize always returns (false, nil).
type Provider struct {
	script         string
	timeoutMs      int
	maxMemoryBytes uint64
	envWhitelist   map[string]string
	httpClient     *http.Client
}

// NewProvider creates a new Lua auth provider.
// script is the Lua source code; empty string means disabled.
// timeoutMs is the max execution time in milliseconds (0 → 3000).
// maxMemoryMB is the max VM memory in megabytes (0 → 32).
// envWhitelist maps allowed env var names to their values.
func NewProvider(script string, timeoutMs int, maxMemoryMB int, envWhitelist map[string]string) *Provider {
	if timeoutMs <= 0 {
		timeoutMs = 3000
	}
	if maxMemoryMB <= 0 {
		maxMemoryMB = defaultMaxMemoryMB
	}
	return &Provider{
		script:         script,
		timeoutMs:      timeoutMs,
		maxMemoryBytes: uint64(maxMemoryMB) * 1024 * 1024,
		envWhitelist:   envWhitelist,
		httpClient: &http.Client{
			Timeout: time.Duration(timeoutMs) * time.Millisecond,
			// SSRF protection: custom transport with DNS resolve + private IP check
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					host, port, err := net.SplitHostPort(addr)
					if err != nil {
						return nil, fmt.Errorf("invalid address: %w", err)
					}
					// Resolve hostname
					ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
					if err != nil {
						return nil, fmt.Errorf("DNS lookup failed for %s: %w", host, err)
					}
					for _, ip := range ips {
						if isPrivateIP(ip.IP) {
							return nil, fmt.Errorf("access to private IP %s is blocked (SSRF protection)", ip.IP)
						}
					}
					if len(ips) == 0 {
						return nil, fmt.Errorf("no IP resolved for %s", host)
					}
					resolvedAddr := net.JoinHostPort(ips[0].IP.String(), port)
					return net.DialContext(ctx, network, resolvedAddr)
				},
			},
		},
	}
}

// Enabled returns true when a Lua script is configured.
func (p *Provider) Enabled() bool {
	return p.script != ""
}

// Authorize runs the Lua script with the request data.
// Returns (true, nil) when the script returns true,
// (false, nil) when it returns false,
// (false, err) on timeout or execution error.
func (p *Provider) Authorize(ctx context.Context, r *http.Request) (bool, error) {
	if !p.Enabled() {
		return false, nil
	}

	timeout := time.Duration(p.timeoutMs) * time.Millisecond
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	L := lua.NewState(lua.Options{
		SkipOpenLibs:   true,
		RegistrySize:   1024 * 20,
		CallStackSize: 256,
	})
	defer L.Close()

	// Memory limit
	L.SetMxMemory(p.maxMemoryBytes)

	// VM will check context on every loop iteration and abort on cancel
	L.SetContext(ctx)

	// Open only safe standard libraries
	for _, pair := range []struct {
		name string
		open lua.LGFunction
	}{
		{"base", lua.OpenBase},
		{"math", lua.OpenMath},
		{"string", lua.OpenString},
		{"table", lua.OpenTable},
	} {
		L.Push(L.NewFunction(pair.open))
		L.Push(lua.LString(pair.name))
		L.Call(1, 0)
	}

	// Disable dangerous base functions: dofile, loadfile
	for _, name := range []string{"dofile", "loadfile"} {
		L.Push(L.NewFunction(func(L *lua.LState) int {
			L.RaiseError("%s is disabled in sandbox", name)
			return 0
		}))
		L.SetGlobal(name, L.Get(-1))
		L.Pop(1)
	}

	// Register sandboxed libraries in package.loaded so require() works
	p.registerRequestLib(L)
	registerJSONLib(L)
	registerEnvLib(L, p.envWhitelist)

	// Build req table and set as global
	reqTable := buildReqTable(L, r)
	L.SetGlobal("req", reqTable)

	// Execute the script (defines authorize function)
	if err := L.DoString(p.script); err != nil {
		return false, fmt.Errorf("lua script error: %w", err)
	}

	// Call authorize(req)
	authFn := L.GetGlobal("authorize")
	if authFn.Type() != lua.LTFunction {
		return false, fmt.Errorf("lua script must define authorize(req) function")
	}

	L.Push(authFn)
	L.Push(reqTable)
	if err := L.PCall(1, 1, nil); err != nil {
		return false, fmt.Errorf("lua authorize() error: %w", err)
	}

	result := L.Get(-1)
	L.Pop(1)

	if result.Type() == lua.LTBool {
		return bool(result.(lua.LBool)), nil
	}
	return false, fmt.Errorf("lua authorize() must return boolean, got %s", result.Type())
}

func buildReqTable(L *lua.LState, r *http.Request) *lua.LTable {
	req := L.NewTable()

	L.SetField(req, "method", lua.LString(r.Method))
	L.SetField(req, "path", lua.LString(r.URL.Path))

	// Headers — last value wins (consistent with r.Header.Get)
	headers := L.NewTable()
	for k, vs := range r.Header {
		if len(vs) > 0 {
			L.SetField(headers, k, lua.LString(vs[len(vs)-1]))
		}
	}
	L.SetField(req, "headers", headers)

	// Query params — last value wins
	query := L.NewTable()
	for k, vs := range r.URL.Query() {
		if len(vs) > 0 {
			L.SetField(query, k, lua.LString(vs[len(vs)-1]))
		}
	}
	L.SetField(req, "query", query)

	L.SetField(req, "remote_addr", lua.LString(r.RemoteAddr))

	return req
}

// isPrivateIP checks if an IP address is in a private/reserved range (SSRF protection)
func isPrivateIP(ip net.IP) bool {
	privateCIDRs := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"127.0.0.0/8",
		"0.0.0.0/8",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}

	for _, cidr := range privateCIDRs {
		_, pr, _ := net.ParseCIDR(cidr)
		if pr.Contains(ip) {
			return true
		}
	}

	return false
}
