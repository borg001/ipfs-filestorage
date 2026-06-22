package lua

import (
	"encoding/json"

	lua "github.com/yuin/gopher-lua"
)

// registerJSONLib exposes json.encode(table) and json.decode(string) in the Lua VM.
// Registered in package.loaded so require("json") works without filesystem access.
func registerJSONLib(L *lua.LState) {
	mod := L.NewTable()

	L.SetField(mod, "encode", L.NewFunction(func(L *lua.LState) int {
		table := L.CheckTable(1)
		goValue := tableToGo(L, table)
		data, err := json.Marshal(goValue)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LString(string(data)))
		return 1
	}))

	L.SetField(mod, "decode", L.NewFunction(func(L *lua.LState) int {
		str := L.CheckString(1)
		var goValue interface{}
		if err := json.Unmarshal([]byte(str), &goValue); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(goToLua(L, goValue))
		return 1
	}))

	// Register in package.loaded so require("json") works
	loaded := L.GetField(L.Get(lua.RegistryIndex), "_LOADED")
	L.SetField(loaded, "json", mod)

	// Also set as global
	L.SetGlobal("json", mod)
}

// tableToGo converts a Lua table to a Go value (map or slice).
func tableToGo(L *lua.LState, t *lua.LTable) interface{} {
	// Check if array-like (keys 1..n)
	maxIdx := 0
	isArray := true
	t.ForEach(func(k, _ lua.LValue) {
		if n, ok := k.(lua.LNumber); ok && n == lua.LNumber(int(n)) && int(n) >= 1 {
			if int(n) > maxIdx {
				maxIdx = int(n)
			}
		} else {
			isArray = false
		}
	})

	if isArray && maxIdx > 0 {
		result := make([]interface{}, maxIdx)
		t.ForEach(func(k, v lua.LValue) {
			idx := int(k.(lua.LNumber)) - 1
			result[idx] = luaToGo(L, v)
		})
		return result
	}

	result := make(map[string]interface{})
	t.ForEach(func(k, v lua.LValue) {
		result[lua.LVAsString(k)] = luaToGo(L, v)
	})
	return result
}

func luaToGo(L *lua.LState, v lua.LValue) interface{} {
	switch v.Type() {
	case lua.LTNil:
		return nil
	case lua.LTBool:
		return bool(v.(lua.LBool))
	case lua.LTNumber:
		return float64(v.(lua.LNumber))
	case lua.LTString:
		return string(v.(lua.LString))
	case lua.LTTable:
		return tableToGo(L, v.(*lua.LTable))
	default:
		return nil
	}
}

// goToLua converts a Go value to a Lua value.
func goToLua(L *lua.LState, v interface{}) lua.LValue {
	switch val := v.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(val)
	case float64:
		return lua.LNumber(val)
	case string:
		return lua.LString(val)
	case []interface{}:
		t := L.NewTable()
		for i, item := range val {
			L.RawSetInt(t, i+1, goToLua(L, item))
		}
		return t
	case map[string]interface{}:
		t := L.NewTable()
		for k, item := range val {
			L.SetField(t, k, goToLua(L, item))
		}
		return t
	default:
		return lua.LNil
	}
}
