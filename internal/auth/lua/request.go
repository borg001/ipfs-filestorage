package lua

import (
	"fmt"
	"io"
	"net/http"

	lua "github.com/yuin/gopher-lua"
)

// registerRequestLib exposes `request.get(url, opts)` and `request.post(url, opts)`
// inside the Lua VM. opts is a table with optional `headers` and `body` fields.
func (p *Provider) registerRequestLib(L *lua.LState) {
	mod := L.NewTable()

	L.SetField(mod, "get", L.NewFunction(func(L *lua.LState) int {
		url := L.CheckString(1)
		opts := L.OptTable(2, L.NewTable())
		resp, err := p.doRequest("GET", url, opts)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(respToTable(L, resp))
		return 1
	}))

	L.SetField(mod, "post", L.NewFunction(func(L *lua.LState) int {
		url := L.CheckString(1)
		opts := L.OptTable(2, L.NewTable())
		resp, err := p.doRequest("POST", url, opts)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(respToTable(L, resp))
		return 1
	}))

	L.SetField(mod, "put", L.NewFunction(func(L *lua.LState) int {
		url := L.CheckString(1)
		opts := L.OptTable(2, L.NewTable())
		resp, err := p.doRequest("PUT", url, opts)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(respToTable(L, resp))
		return 1
	}))

	L.SetField(mod, "del", L.NewFunction(func(L *lua.LState) int {
		url := L.CheckString(1)
		opts := L.OptTable(2, L.NewTable())
		resp, err := p.doRequest("DELETE", url, opts)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(respToTable(L, resp))
		return 1
	}))

	L.SetGlobal("request", mod)
}

func (p *Provider) doRequest(method, url string, opts *lua.LTable) (*http.Response, error) {
	req, err := http.NewRequest(method, url, nil)
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
