#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
API_TOKEN="${API_TOKEN:-${MULTI_AGENT_API_TOKEN:-}}"
POLL_RETRIES="${POLL_RETRIES:-60}"
POLL_INTERVAL="${POLL_INTERVAL:-0.2}"
ALERT_WEBHOOK_LOG="${ALERT_WEBHOOK_LOG:-}"

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

request_json() {
  local method="$1"
  local path="$2"
  local payload="${3:-}"
  local tmp code body
  tmp="$(mktemp)"
  if [[ -n "$payload" ]]; then
    if [[ -n "$API_TOKEN" ]]; then
      code="$(curl -sS -o "$tmp" -w "%{http_code}" -X "$method" "$BASE_URL$path" -H "Authorization: Bearer $API_TOKEN" -H 'Content-Type: application/json' -d "$payload")"
    else
      code="$(curl -sS -o "$tmp" -w "%{http_code}" -X "$method" "$BASE_URL$path" -H 'Content-Type: application/json' -d "$payload")"
    fi
  else
    if [[ -n "$API_TOKEN" ]]; then
      code="$(curl -sS -o "$tmp" -w "%{http_code}" -X "$method" "$BASE_URL$path" -H "Authorization: Bearer $API_TOKEN")"
    else
      code="$(curl -sS -o "$tmp" -w "%{http_code}" -X "$method" "$BASE_URL$path")"
    fi
  fi
  body="$(cat "$tmp")"
  rm -f "$tmp"
  if [[ "$code" -lt 200 || "$code" -ge 300 ]]; then
    echo "HTTP $code for $method $path" >&2
    printf '%s\n' "$body" >&2
    exit 1
  fi
  printf '%s' "$body"
}

wait_run_failed() {
  local project_id="$1"
  local run_id="$2"
  local body status
  for _ in $(seq 1 "$POLL_RETRIES"); do
    body="$(request_json GET "/projects/$project_id/runs/$run_id/status")"
    status="$(printf '%s' "$body" | json_query "run.status")"
    if [[ "$status" == "FAILED" ]]; then
      printf '%s' "$body"
      return 0
    fi
    sleep "$POLL_INTERVAL"
  done
  echo "Run $run_id did not fail in time" >&2
  exit 1
}

wait_webhook_log() {
  local needle="$1"
  if [[ -z "$ALERT_WEBHOOK_LOG" ]]; then
    return 0
  fi
  for _ in $(seq 1 "$POLL_RETRIES"); do
    if [[ -f "$ALERT_WEBHOOK_LOG" ]] && grep -q "$needle" "$ALERT_WEBHOOK_LOG"; then
      return 0
    fi
    sleep "$POLL_INTERVAL"
  done
  echo "Webhook log did not contain $needle" >&2
  if [[ -f "$ALERT_WEBHOOK_LOG" ]]; then
    cat "$ALERT_WEBHOOK_LOG" >&2
  fi
  exit 1
}

suffix="$(date +%s)"
project_json="$(request_json POST "/projects" "{\"name\":\"Alert Smoke $suffix\",\"description\":\"Alert webhook smoke test\"}")"
project_id="$(printf '%s' "$project_json" | json_query "id")"
[[ -n "$project_id" ]] || { echo "missing project id" >&2; exit 1; }

request_json POST "/projects/$project_id/requirements" '{"title":"实现 Todo 列表的增删改查","content":"实现 Todo 列表的增删改查，并模拟私有沙盒失败。"}' >/dev/null
plan_json="$(request_json POST "/projects/$project_id/plan" '{}')"
task_id="$(printf '%s' "$plan_json" | json_query "task.id")"
[[ -n "$task_id" ]] || { echo "missing task id" >&2; exit 1; }

request_json POST "/projects/$project_id/tasks/$task_id/sandbox/fail" '{"reason":"alert smoke failure"}' >/dev/null
run_json="$(request_json POST "/projects/$project_id/tasks/run" "{\"taskId\":\"$task_id\"}")"
run_id="$(printf '%s' "$run_json" | json_query "run.id")"
[[ -n "$run_id" ]] || { echo "missing run id" >&2; exit 1; }

status_json="$(wait_run_failed "$project_id" "$run_id")"
assert_contains "$status_json" '"status":"FAILED"' "run status should be FAILED"

alerts_json="$(request_json GET "/projects/$project_id/alerts")"
assert_contains "$alerts_json" '"type":"RUN_FAILURE"' "alerts should include RUN_FAILURE"
assert_contains "$alerts_json" "\"resourceId\":\"$run_id\"" "alerts should reference failed run"

wait_webhook_log 'RUN_FAILURE'

echo "Alert smoke passed for project $project_id and run $run_id"
