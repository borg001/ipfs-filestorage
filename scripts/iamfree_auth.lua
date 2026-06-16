local request = require("request")
local env = require("env")

function authorize(req)
  local token = req.headers["Authorization"]
  if not token or token == "" then
    token = req.headers["X-API-Key"]
  end
  if not token or token == "" then
    return false
  end

  local url = env.get("AUTH_SERVICE_URL")
  if not url or url == "" then
    return false
  end

  local resp = request.get(url .. "/auth/me", {
    headers = { Authorization = token }
  })

  return resp ~= nil and resp.status == 200
end
