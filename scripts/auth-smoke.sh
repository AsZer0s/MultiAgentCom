#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:18082}"
API_TOKEN="${API_TOKEN:-${MULTI_AGENT_API_TOKEN:-}}"

if [[ -z "$API_TOKEN" ]]; then
  echo "API_TOKEN or MULTI_AGENT_API_TOKEN is required" >&2
  exit 1
fi

assert_contains() {
  local haystack="$1"
  local needle="$2"
  local message="$3"
  if [[ "$haystack" != *"$needle"* ]]; then
    echo "ASSERT FAILED: $message" >&2
    exit 1
  fi
}

json_query() {
  local path="$1"
  /usr/bin/python3 -c '
import json
import sys

path = sys.argv[1].split(".")
data = json.load(sys.stdin)
value = data
for part in path:
    if part.isdigit():
        value = value[int(part)]
    else:
        value = value[part]
if isinstance(value, (dict, list)):
    print(json.dumps(value, ensure_ascii=False, separators=(",", ":")))
elif value is None:
    print("")
else:
    print(value)
' "$path"
}

request_expect_status() {
  local method="$1"
  local path="$2"
  local expected_status="$3"
  local payload="${4:-}"
  local auth_header="${5:-}"
  local tmp code body
  tmp="$(mktemp)"
  if [[ -n "$payload" ]]; then
    if [[ -n "$auth_header" ]]; then
      code="$(curl -sS -o "$tmp" -w "%{http_code}" -X "$method" "$BASE_URL$path" -H "$auth_header" -H 'Content-Type: application/json' -d "$payload")"
    else
      code="$(curl -sS -o "$tmp" -w "%{http_code}" -X "$method" "$BASE_URL$path" -H 'Content-Type: application/json' -d "$payload")"
    fi
  else
    if [[ -n "$auth_header" ]]; then
      code="$(curl -sS -o "$tmp" -w "%{http_code}" -X "$method" "$BASE_URL$path" -H "$auth_header")"
    else
      code="$(curl -sS -o "$tmp" -w "%{http_code}" -X "$method" "$BASE_URL$path")"
    fi
  fi
  body="$(cat "$tmp")"
  rm -f "$tmp"
  if [[ "$code" != "$expected_status" ]]; then
    echo "HTTP $code for $method $path, expected $expected_status" >&2
    printf '%s\n' "$body" >&2
    exit 1
  fi
  printf '%s' "$body"
}

health_body="$(request_expect_status GET "/health" 200)"
assert_contains "$health_body" '"status":"ok"' "health endpoint should stay public"

unauthorized_body="$(request_expect_status POST "/projects" 401 '{"name":"Unauthorized Demo"}')"
assert_contains "$unauthorized_body" '"code":"UNAUTHORIZED"' "unauthorized response should contain code"

auth_header="Authorization: Bearer $API_TOKEN"
actor_header="X-Actor: auth-smoke"

project_body="$(curl -sS -X POST "$BASE_URL/projects" \
  -H "$auth_header" \
  -H "$actor_header" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Authorized Demo","description":"Auth smoke test"}')"
project_id="$(printf '%s' "$project_body" | json_query "id")"
[[ -n "$project_id" ]] || { echo "missing project id" >&2; exit 1; }

audit_body="$(curl -sS "$BASE_URL/projects/$project_id/audit-logs" -H "$auth_header" -H "$actor_header")"
assert_contains "$audit_body" '"action":"PROJECT_CREATE"' "audit log should include project create"
assert_contains "$audit_body" '"actor":"auth-smoke"' "audit log should record actor"

panel_body="$(curl -sS "$BASE_URL/status/panel?token=$API_TOKEN")"
assert_contains "$panel_body" 'Status Matrix' "status panel with token query should render"
assert_contains "$panel_body" 'Audit Trail' "status panel should include audit trail"

echo "Auth smoke passed for project $project_id"
