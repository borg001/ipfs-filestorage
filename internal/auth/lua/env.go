package lua

import (
	"os"

	lua "github.com/yuin/gopher-lua"
)

// registerEnvLib exposes env.get("KEY") in the Lua VM.
// Only keys present in the whitelist map are readable; others return nil.
// Registered in package.loaded so require("env") works without filesystem access.
func registerEnvLib(L *lua.LState, whitelist map[string]string) {
	mod := L.NewTable()

	L.SetField(mod, "get", L.NewFunction(func(L *lua.LState) int {
		key := L.CheckString(1)
		if val, ok := whitelist[key]; ok {
			// If value was pre-populated, use it
			if val != "" {
				L.Push(lua.LString(val))
				return 1
			}
			// Otherwise check OS env (still restricted to whitelisted keys only)
			if v := os.Getenv(key); v != "" {
				L.Push(lua.LString(v))
				return 1
			}
		}
		L.Push(lua.LNil)
		return 1
	}))

	// Register in package.loaded so require("env") works
	L.GetField(lua.RegistryIndex, "_LOADED")
	L.SetField(L.Get(-1), "env", mod)
	L.Pop(1)

	// Also set as global
	L.SetGlobal("env", mod)
}
