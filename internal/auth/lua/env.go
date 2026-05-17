package lua

import (
	"os"

	lua "github.com/yuin/gopher-lua"
)

// registerEnvLib exposes env.get("KEY") in the Lua VM.
// Only keys present in the whitelist map are readable; others return nil.
func registerEnvLib(L *lua.LState, whitelist map[string]string) {
	mod := L.NewTable()

	L.SetField(mod, "get", L.NewFunction(func(L *lua.LState) int {
		key := L.CheckString(1)
		if val, ok := whitelist[key]; ok {
			L.Push(lua.LString(val))
			return 1
		}
		// Fallback: check OS env (still restricted to whitelist keys)
		if v := os.Getenv(key); v != "" {
			// Only allow if the key was explicitly whitelisted
			if _, ok := whitelist[key]; ok {
				L.Push(lua.LString(v))
				return 1
			}
		}
		L.Push(lua.LNil)
		return 1
	}))

	L.SetGlobal("env", mod)
}
