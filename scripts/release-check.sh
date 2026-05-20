#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
RELEASE_CHECK_PORT="${RELEASE_CHECK_PORT:-$((24000 + RANDOM % 1000))}"
BASE_URL="${BASE_URL:-http://127.0.0.1:$RELEASE_CHECK_PORT}"
LOG_FILE="${LOG_FILE:-${TMPDIR:-/tmp}/multiagentcom-release-check.log}"
DEMO_RUNS="${DEMO_RUNS:-3}"
ALERT_WEBHOOK_PORT="${ALERT_WEBHOOK_PORT:-18081}"
ALERT_WEBHOOK_LOG="${ALERT_WEBHOOK_LOG:-${TMPDIR:-/tmp}/multiagentcom-alert-webhook.log}"
AUTH_SMOKE_PORT="${AUTH_SMOKE_PORT:-18082}"
AUTH_SMOKE_TOKEN="${AUTH_SMOKE_TOKEN:-release-check-token}"
RUNTIME_SMOKE_PORT="${RUNTIME_SMOKE_PORT:-$((25000 + RANDOM % 1000))}"
RUNTIME_PROVIDER_PORT="${RUNTIME_PROVIDER_PORT:-$((26000 + RANDOM % 1000))}"
RUNTIME_PROVIDER_TOKEN="${RUNTIME_PROVIDER_TOKEN:-runtime-release-token}"
RUNTIME_PROVIDER_LOG="${RUNTIME_PROVIDER_LOG:-${TMPDIR:-/tmp}/multiagentcom-runtime-provider.log}"

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  if [[ -n "${AUTH_SERVER_PID:-}" ]]; then
    kill "$AUTH_SERVER_PID" >/dev/null 2>&1 || true
    wait "$AUTH_SERVER_PID" 2>/dev/null || true
  fi
  if [[ -n "${RUNTIME_SERVER_PID:-}" ]]; then
    kill "$RUNTIME_SERVER_PID" >/dev/null 2>&1 || true
    wait "$RUNTIME_SERVER_PID" 2>/dev/null || true
  fi
  if [[ -n "${RUNTIME_PROVIDER_PID:-}" ]]; then
    kill "$RUNTIME_PROVIDER_PID" >/dev/null 2>&1 || true
    wait "$RUNTIME_PROVIDER_PID" 2>/dev/null || true
  fi
  if [[ -n "${WEBHOOK_PID:-}" ]]; then
    kill "$WEBHOOK_PID" >/dev/null 2>&1 || true
    wait "$WEBHOOK_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

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

request_json() {
  local base_url="$1"
  local method="$2"
  local path="$3"
  local payload="${4:-}"
  local tmp code body
  tmp="$(mktemp)"
  if [[ -n "$payload" ]]; then
    code="$(curl -sS -o "$tmp" -w "%{http_code}" -X "$method" "$base_url$path" -H 'Content-Type: application/json' -d "$payload")"
  else
    code="$(curl -sS -o "$tmp" -w "%{http_code}" -X "$method" "$base_url$path")"
  fi
  body="$(<"$tmp")"
  rm -f "$tmp"
  if [[ "$code" -lt 200 || "$code" -ge 300 ]]; then
    echo "HTTP $code for $method $path" >&2
    printf '%s\n' "$body" >&2
    exit 1
  fi
  printf '%s' "$body"
}

wait_health() {
  local base_url="$1"
  local expected_service="${2:-}"
  local body
  for _ in $(seq 1 60); do
    if body="$(curl -sS "$base_url/health" 2>/dev/null)"; then
      if [[ -z "$expected_service" || "$body" == *"\"service\":\"$expected_service\""* ]]; then
        return 0
      fi
    fi
    sleep 0.2
  done
  body="$(curl -sS "$base_url/health")"
  if [[ -n "$expected_service" && "$body" != *"\"service\":\"$expected_service\""* ]]; then
    echo "Unexpected service at $base_url/health" >&2
    printf '%s\n' "$body" >&2
    exit 1
  fi
}

wait_run_status() {
  local base_url="$1"
  local project_id="$2"
  local run_id="$3"
  local expected="$4"
  local status body
  for _ in $(seq 1 80); do
    body="$(request_json "$base_url" GET "/projects/$project_id/runs/$run_id/status")"
    status="$(printf '%s' "$body" | json_query "run.status")"
    if [[ "$status" == "$expected" ]]; then
      printf '%s' "$body"
      return 0
    fi
    if [[ "$status" == "FAILED" && "$expected" != "FAILED" ]]; then
      echo "Run $run_id failed unexpectedly" >&2
      printf '%s\n' "$body" >&2
      exit 1
    fi
    sleep 0.2
  done
  echo "Run $run_id did not reach $expected in time" >&2
  exit 1
}

echo "== syntax checks =="
bash -n "$ROOT_DIR/scripts/demo.sh"
bash -n "$ROOT_DIR/scripts/alert-smoke.sh"
bash -n "$ROOT_DIR/scripts/auth-smoke.sh"
bash -n "$ROOT_DIR/scripts/release-check.sh"
bash -n "$ROOT_DIR/scripts/security-check.sh"
bash -n "$ROOT_DIR/scripts/status-stream-smoke.sh"
bash -n "$ROOT_DIR/scripts/container-runtime-smoke.sh"

echo
echo "== security checks =="
bash "$ROOT_DIR/scripts/security-check.sh"

echo
echo "== regression tests =="
(cd "$ROOT_DIR" && go test ./...)

echo
echo "== start local alert webhook sink =="
rm -f "$ALERT_WEBHOOK_LOG"
/usr/bin/python3 -c '
import http.server
import sys

port = int(sys.argv[1])
log_path = sys.argv[2]

class Handler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        payload = self.rfile.read(length).decode("utf-8")
        with open(log_path, "a", encoding="utf-8") as fh:
            fh.write(payload + "\n")
        self.send_response(202)
        self.end_headers()

    def log_message(self, format, *args):
        return

http.server.HTTPServer(("127.0.0.1", port), Handler).serve_forever()
' "$ALERT_WEBHOOK_PORT" "$ALERT_WEBHOOK_LOG" &
WEBHOOK_PID="$!"

echo
echo "== start local server =="
(cd "$ROOT_DIR" && MULTI_AGENT_ADDR=":$RELEASE_CHECK_PORT" MULTI_AGENT_ALERT_WEBHOOK_URL="http://127.0.0.1:$ALERT_WEBHOOK_PORT" go run ./cmd/server >"$LOG_FILE" 2>&1) &
SERVER_PID="$!"

wait_health "$BASE_URL" "multiagentcom-api"

echo
echo "== status stream smoke verification =="
BASE_URL="$BASE_URL" bash "$ROOT_DIR/scripts/status-stream-smoke.sh"

echo
echo "== end-to-end demo and delivery gate verification =="
RUNS="$DEMO_RUNS" BASE_URL="$BASE_URL" ARTIFACT_DIR="${TMPDIR:-/tmp}/multiagentcom-release-artifacts" bash "$ROOT_DIR/scripts/demo.sh"

echo
echo "== alert webhook smoke verification =="
BASE_URL="$BASE_URL" ALERT_WEBHOOK_LOG="$ALERT_WEBHOOK_LOG" bash "$ROOT_DIR/scripts/alert-smoke.sh"

echo
echo "== authenticated api smoke verification =="
(cd "$ROOT_DIR" && MULTI_AGENT_ADDR=":$AUTH_SMOKE_PORT" MULTI_AGENT_API_TOKEN="$AUTH_SMOKE_TOKEN" MULTI_AGENT_ALERT_WEBHOOK_URL="http://127.0.0.1:$ALERT_WEBHOOK_PORT" go run ./cmd/server >"${LOG_FILE}.auth" 2>&1) &
AUTH_SERVER_PID="$!"

wait_health "http://127.0.0.1:$AUTH_SMOKE_PORT" "multiagentcom-api"
BASE_URL="http://127.0.0.1:$AUTH_SMOKE_PORT" API_TOKEN="$AUTH_SMOKE_TOKEN" bash "$ROOT_DIR/scripts/auth-smoke.sh"
kill "$AUTH_SERVER_PID" >/dev/null 2>&1 || true
wait "$AUTH_SERVER_PID" 2>/dev/null || true
unset AUTH_SERVER_PID

echo
echo "== runtime provider smoke verification =="
rm -f "$RUNTIME_PROVIDER_LOG"
/usr/bin/python3 -c '
import http.server
import json
import sys

port = int(sys.argv[1])
token = sys.argv[2]
log_path = sys.argv[3]

class Handler(http.server.BaseHTTPRequestHandler):
    attempts = 0

    def do_GET(self):
        if self.path == "/health":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")
            return
        self.send_response(404)
        self.end_headers()

    def do_POST(self):
        auth = self.headers.get("Authorization", "")
        length = int(self.headers.get("Content-Length", "0"))
        payload = self.rfile.read(length).decode("utf-8")
        Handler.attempts += 1
        with open(log_path, "a", encoding="utf-8") as fh:
            fh.write(json.dumps({"attempt": Handler.attempts, "auth": auth, "payload": payload}) + "\n")
        if auth != "Bearer " + token:
            self.send_response(401)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({"protocolVersion":"runtime.http.v1","error":{"code":"UNAUTHORIZED","message":"missing bearer token"}}).encode("utf-8"))
            return
        if Handler.attempts == 1:
            self.send_response(503)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({"protocolVersion":"runtime.http.v1","error":{"code":"UPSTREAM_UNAVAILABLE","message":"temporary outage","retryable":True,"providerStatus":503,"requestId":"release-smoke-1"}}).encode("utf-8"))
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({"protocolVersion":"runtime.http.v1","model":"release-smoke-runtime","output":"runtime provider smoke passed","usage":{"promptTokens":13,"completionTokens":17,"totalTokens":30}}).encode("utf-8"))

    def log_message(self, format, *args):
        return

http.server.HTTPServer(("127.0.0.1", port), Handler).serve_forever()
' "$RUNTIME_PROVIDER_PORT" "$RUNTIME_PROVIDER_TOKEN" "$RUNTIME_PROVIDER_LOG" &
RUNTIME_PROVIDER_PID="$!"
for _ in $(seq 1 60); do
  if curl -sS "http://127.0.0.1:$RUNTIME_PROVIDER_PORT/health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done
curl -sS "http://127.0.0.1:$RUNTIME_PROVIDER_PORT/health" >/dev/null

(cd "$ROOT_DIR" && \
  MULTI_AGENT_ADDR=":$RUNTIME_SMOKE_PORT" \
  MULTI_AGENT_SERVICE_NAME="multiagentcom-runtime-smoke" \
  MULTI_AGENT_ALERT_WEBHOOK_URL="http://127.0.0.1:$ALERT_WEBHOOK_PORT" \
  MULTI_AGENT_RUNTIME_PROVIDER="http" \
  MULTI_AGENT_RUNTIME_HTTP_ENDPOINT="http://127.0.0.1:$RUNTIME_PROVIDER_PORT" \
  MULTI_AGENT_RUNTIME_HTTP_BEARER_TOKEN="$RUNTIME_PROVIDER_TOKEN" \
  MULTI_AGENT_RUNTIME_HTTP_MAX_ATTEMPTS="2" \
  MULTI_AGENT_RUNTIME_HTTP_RETRY_BASE_DELAY="10ms" \
  go run ./cmd/server >"${LOG_FILE}.runtime" 2>&1) &
RUNTIME_SERVER_PID="$!"

RUNTIME_BASE_URL="http://127.0.0.1:$RUNTIME_SMOKE_PORT"
wait_health "$RUNTIME_BASE_URL" "multiagentcom-runtime-smoke"

runtime_project_json="$(request_json "$RUNTIME_BASE_URL" POST "/projects" '{"name":"Runtime Provider Smoke"}')"
runtime_project_id="$(printf '%s' "$runtime_project_json" | json_query "id")"
request_json "$RUNTIME_BASE_URL" POST "/projects/$runtime_project_id/requirements" '{"title":"Runtime smoke","content":"Verify authenticated retrying runtime provider."}' >/dev/null
runtime_plan_json="$(request_json "$RUNTIME_BASE_URL" POST "/projects/$runtime_project_id/plan" '{}')"
runtime_task_id="$(printf '%s' "$runtime_plan_json" | json_query "task.id")"
runtime_run_json="$(request_json "$RUNTIME_BASE_URL" POST "/projects/$runtime_project_id/tasks/run" "{\"taskId\":\"$runtime_task_id\"}")"
runtime_run_id="$(printf '%s' "$runtime_run_json" | json_query "run.id")"
runtime_status_json="$(wait_run_status "$RUNTIME_BASE_URL" "$runtime_project_id" "$runtime_run_id" "SUCCEEDED")"
runtime_model="$(printf '%s' "$runtime_status_json" | json_query "run.model")"
runtime_tokens="$(printf '%s' "$runtime_status_json" | json_query "run.totalTokens")"
if [[ "$runtime_model" != "release-smoke-runtime" || "$runtime_tokens" != "30" ]]; then
  echo "Runtime provider smoke returned unexpected model/tokens" >&2
  printf '%s\n' "$runtime_status_json" >&2
  exit 1
fi
if ! grep -q "Bearer $RUNTIME_PROVIDER_TOKEN" "$RUNTIME_PROVIDER_LOG"; then
  echo "Runtime provider did not receive bearer token" >&2
  exit 1
fi
if [[ "$(wc -l < "$RUNTIME_PROVIDER_LOG" | tr -d ' ')" -lt 2 ]]; then
  echo "Runtime provider retry was not observed" >&2
  exit 1
fi
kill "$RUNTIME_SERVER_PID" >/dev/null 2>&1 || true
wait "$RUNTIME_SERVER_PID" 2>/dev/null || true
unset RUNTIME_SERVER_PID
kill "$RUNTIME_PROVIDER_PID" >/dev/null 2>&1 || true
wait "$RUNTIME_PROVIDER_PID" 2>/dev/null || true
unset RUNTIME_PROVIDER_PID

if [[ "${RUN_CONTAINER_SMOKE:-0}" == "1" ]]; then
  echo
  echo "== container runtime smoke verification =="
  RUN_CONTAINER_SMOKE=1 bash "$ROOT_DIR/scripts/container-runtime-smoke.sh"
else
  echo
  echo "== container runtime smoke verification skipped =="
  echo "Set RUN_CONTAINER_SMOKE=1 to run the real Docker/Podman-backed container smoke."
fi

echo
echo "== docs check =="
test -f "$ROOT_DIR/docs/Release-Checklist.md"
test -f "$ROOT_DIR/docs/Sprint-4-Acceptance-Report.md"

echo
echo "Release check passed."
