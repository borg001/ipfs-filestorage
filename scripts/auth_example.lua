-- Example: integrate with darkrain/auth-service
--
-- The `request` library makes HTTP calls. `json` decodes responses.
-- `env.get()` reads only whitelisted env vars (see AUTH_LUA_ENV_WHITELIST).

local request = require("request")
local json    = require("json")
local env     = require("env")

function authorize(req)
  -- Extract Bearer token from Authorization header
  local auth_header = req.headers["Authorization"]
  if not auth_header then return false end

  -- Also accept X-API-Key as a token
  local token = auth_header
  if not token or token == "" then
    token = req.headers["X-API-Key"]
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
