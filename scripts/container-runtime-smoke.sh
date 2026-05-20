#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
RUN_CONTAINER_SMOKE="${RUN_CONTAINER_SMOKE:-0}"
CONTAINER_BINARY="${MULTI_AGENT_RUNTIME_CONTAINER_BINARY:-${CONTAINER_BINARY:-docker}}"
CONTAINER_IMAGE="${MULTI_AGENT_RUNTIME_CONTAINER_IMAGE:-${CONTAINER_SMOKE_IMAGE:-multiagentcom-runtime-smoke:local}}"
CONTAINER_SMOKE_PORT="${CONTAINER_SMOKE_PORT:-$((27000 + RANDOM % 1000))}"
BASE_URL="${BASE_URL:-http://127.0.0.1:$CONTAINER_SMOKE_PORT}"
LOG_FILE="${LOG_FILE:-${TMPDIR:-/tmp}/multiagentcom-container-runtime-smoke.log}"
BUILD_CONTEXT=""

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  if [[ -n "$BUILD_CONTEXT" ]]; then
    rm -rf "$BUILD_CONTEXT"
  fi
}
trap cleanup EXIT

if [[ "$RUN_CONTAINER_SMOKE" != "1" ]]; then
  echo "Skipping container runtime smoke; set RUN_CONTAINER_SMOKE=1 to enable."
  exit 0
fi

if ! command -v "$CONTAINER_BINARY" >/dev/null 2>&1; then
  echo "Container runtime binary not found: $CONTAINER_BINARY" >&2
  exit 1
fi

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
  local method="$1"
  local path="$2"
  local payload="${3:-}"
  local tmp code body
  tmp="$(mktemp)"
  if [[ -n "$payload" ]]; then
    code="$(curl -sS -o "$tmp" -w "%{http_code}" -X "$method" "$BASE_URL$path" -H 'Content-Type: application/json' -d "$payload")"
  else
    code="$(curl -sS -o "$tmp" -w "%{http_code}" -X "$method" "$BASE_URL$path")"
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
  for _ in $(seq 1 80); do
    if curl -sS "$BASE_URL/health" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done
  echo "Server did not become healthy at $BASE_URL" >&2
  if [[ -f "$LOG_FILE" ]]; then
    tail -n 80 "$LOG_FILE" >&2
  fi
  exit 1
}

wait_run_status() {
  local project_id="$1"
  local run_id="$2"
  local status body
  for _ in $(seq 1 120); do
    body="$(request_json GET "/projects/$project_id/runs/$run_id/status")"
    status="$(printf '%s' "$body" | json_query "run.status")"
    if [[ "$status" == "SUCCEEDED" ]]; then
      printf '%s' "$body"
      return 0
    fi
    if [[ "$status" == "FAILED" ]]; then
      echo "Container runtime run failed" >&2
      printf '%s\n' "$body" >&2
      exit 1
    fi
    sleep 0.25
  done
  echo "Run $run_id did not complete in time" >&2
  exit 1
}

if [[ -z "${CONTAINER_SMOKE_SKIP_BUILD:-}" ]]; then
  BUILD_CONTEXT="$(mktemp -d)"
  cat >"$BUILD_CONTEXT/Dockerfile" <<'DOCKERFILE'
FROM alpine:3.20
COPY runtime-smoke.sh /usr/local/bin/runtime-smoke
RUN chmod 755 /usr/local/bin/runtime-smoke
ENTRYPOINT ["/usr/local/bin/runtime-smoke"]
DOCKERFILE
  cat >"$BUILD_CONTEXT/runtime-smoke.sh" <<'RUNTIME'
#!/bin/sh
set -eu
payload="$(cat)"
printf '%s' "$payload" >/tmp/runtime-payload.json
if ! grep -q '"protocolVersion":"runtime.http.v1"' /tmp/runtime-payload.json; then
  echo "missing runtime.http.v1 payload" >&2
  exit 42
fi
if ! grep -q '"workspacePath"' /tmp/runtime-payload.json; then
  echo "missing workspace metadata" >&2
  exit 43
fi
printf 'container runtime smoke passed'
RUNTIME
  echo "Building container runtime smoke image $CONTAINER_IMAGE with $CONTAINER_BINARY"
  "$CONTAINER_BINARY" build -t "$CONTAINER_IMAGE" "$BUILD_CONTEXT"
else
  echo "Using existing container runtime smoke image $CONTAINER_IMAGE"
fi

echo "Starting server with container runtime at $BASE_URL"
(cd "$ROOT_DIR" && \
  MULTI_AGENT_ADDR=":$CONTAINER_SMOKE_PORT" \
  MULTI_AGENT_SERVICE_NAME="multiagentcom-container-runtime-smoke" \
  MULTI_AGENT_RUNTIME_PROVIDER="container" \
  MULTI_AGENT_RUNTIME_CONTAINER_BINARY="$CONTAINER_BINARY" \
  MULTI_AGENT_RUNTIME_CONTAINER_IMAGE="$CONTAINER_IMAGE" \
  MULTI_AGENT_RUNTIME_CONTAINER_NETWORK="none" \
  MULTI_AGENT_RUNTIME_CONTAINER_READONLY_ROOTFS="true" \
  MULTI_AGENT_RUNTIME_CONTAINER_WORKDIR="/workspace" \
  MULTI_AGENT_RUNTIME_CONTAINER_CPUS="1" \
  MULTI_AGENT_RUNTIME_CONTAINER_MEMORY="128m" \
  MULTI_AGENT_RUNTIME_CONTAINER_PIDS_LIMIT="64" \
  MULTI_AGENT_RUNTIME_CONTAINER_TMPFS="/tmp:rw,nosuid,nodev,noexec,size=64m;/run:rw,nosuid,nodev,noexec,size=16m" \
  go run ./cmd/server >"$LOG_FILE" 2>&1) &
SERVER_PID="$!"
wait_health

ready_body="$(request_json GET "/ready")"
if [[ "$ready_body" != *'"status":"ready"'* || "$ready_body" != *'"name":"runtime"'* ]]; then
  echo "Container runtime readiness did not report ready" >&2
  printf '%s\n' "$ready_body" >&2
  exit 1
fi

project_json="$(request_json POST "/projects" '{"name":"Container Runtime Smoke"}')"
project_id="$(printf '%s' "$project_json" | json_query "id")"
request_json POST "/projects/$project_id/requirements" '{"title":"Container smoke","content":"Verify container runtime provider."}' >/dev/null
plan_json="$(request_json POST "/projects/$project_id/plan" '{}')"
task_id="$(printf '%s' "$plan_json" | json_query "task.id")"
run_json="$(request_json POST "/projects/$project_id/tasks/run" "{\"taskId\":\"$task_id\"}")"
run_id="$(printf '%s' "$run_json" | json_query "run.id")"
status_json="$(wait_run_status "$project_id" "$run_id")"
model="$(printf '%s' "$status_json" | json_query "run.model")"
summary="$(printf '%s' "$status_json" | json_query "run.resultSummary")"
if [[ "$model" != "container-runtime" || "$summary" != *"container runtime smoke passed"* ]]; then
  echo "Container runtime smoke returned unexpected result" >&2
  printf '%s\n' "$status_json" >&2
  exit 1
fi

echo "Container runtime smoke passed for $CONTAINER_IMAGE"
