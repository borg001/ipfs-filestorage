package lua

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Provider executes a sandboxed Lua script to validate requests.
// If no script is configured, Authorize always returns (false, nil).
type Provider struct {
	script       string
	timeoutMs    int
	envWhitelist map[string]string
	httpClient   *http.Client
}

// NewProvider creates a new Lua auth provider.
// script is the Lua source code; empty string means disabled.
// timeoutMs is the max execution time in milliseconds (0 → 3000).
// envWhitelist maps allowed env var names to their values.
func NewProvider(script string, timeoutMs int, envWhitelist map[string]string) *Provider {
	if timeoutMs <= 0 {
		timeoutMs = 3000
	}
	return &Provider{
		script:       script,
		timeoutMs:    timeoutMs,
		envWhitelist: envWhitelist,
		httpClient: &http.Client{
			Timeout: time.Duration(timeoutMs) * time.Millisecond,
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
		SkipOpenLibs: true,
	})
	defer L.Close()

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

	// Override dangerous base functions with no-ops
	L.SetGlobal("dofile", L.NewFunction(func(L *lua.LState) int { return 0 }))
	L.SetGlobal("loadfile", L.NewFunction(func(L *lua.LState) int { return 0 }))
	L.SetGlobal("load", L.NewFunction(func(L *lua.LState) int {
		L.RaiseError("load() is disabled in sandbox")
		return 0
	}))

	// Register sandboxed libraries
	p.registerRequestLib(L)
	registerJSONLib(L)
	registerEnvLib(L, p.envWhitelist)

	// Build req table and set as global
	reqTable := buildReqTable(L, r)
	L.SetGlobal("req", reqTable)

	// Register modules in package.loaded so require() works
	loaded := L.GetField(L.Get(lua.RegistryIndex), "_LOADED")
	L.SetField(loaded, "request", L.GetGlobal("request"))
	L.SetField(loaded, "json", L.GetGlobal("json"))
	L.SetField(loaded, "env", L.GetGlobal("env"))

	// Execute the script (defines authorize function)
	if err := L.DoString(p.script); err != nil {
		log.Printf("[AUTH-LUA] Script parse error: %v", err)
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
		log.Printf("[AUTH-LUA] authorize() execution error: %v", err)
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
