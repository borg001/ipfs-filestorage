-- Example: integrate with darkrain/auth-service
--
-- The `request` library makes HTTP calls. `json` decodes responses.
-- `env.get()` reads only whitelisted env vars (see AUTH_LUA_ENV_WHITELIST).

local request = require("request")
local json    = require("json")
local env     = require("env")

local function cookie_value(cookie_header, name)
  if not cookie_header or cookie_header == "" then return nil end
  for pair in string.gmatch(cookie_header, "([^;]+)") do
    local key, value = string.match(pair, "^%s*([^=]+)=?(.*)$")
    if key == name then return value end
  end
  return nil
end

function authorize(req)
  -- Extract Bearer token from Authorization header
  local auth_header = req.headers["Authorization"]

  -- Also accept X-API-Key as a token
  local token = auth_header
  if not token or token == "" then
    token = req.headers["X-API-Key"]
  end
  if not token or token == "" then
    local cookie_token = cookie_value(req.headers["Cookie"] or req.headers["cookie"], "iamfree_auth_token")
    if cookie_token and cookie_token ~= "" then
      token = "Bearer " .. cookie_token
    end
  end
  if not token or token == "" then
    token = req.query["token"] or req.query["access_token"]
    if token and token ~= "" then
      token = "Bearer " .. token
    end
  end
  if not token or token == "" then return false end

  -- Call auth-service /auth/me endpoint
  local url = env.get("AUTH_SERVICE_URL")
  if not url then return false end

  local resp = request.get(url .. "/auth/me", {
    headers = { Authorization = token }
  })

  if not resp or resp.status ~= 200 then return false end

  local data = json.decode(resp.body)
  -- darkrain/auth-service returns user data only for a valid active session.
  return data.id ~= nil
end
