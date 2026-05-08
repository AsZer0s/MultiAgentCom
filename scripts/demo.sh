#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
API_TOKEN="${API_TOKEN:-${MULTI_AGENT_API_TOKEN:-}}"
RUNS="${RUNS:-1}"
POLL_RETRIES="${POLL_RETRIES:-60}"
POLL_INTERVAL="${POLL_INTERVAL:-0.2}"
ARTIFACT_DIR="${ARTIFACT_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/multiagentcom-demo.XXXXXX")}"

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

download_file() {
  local url="$1"
  local output="$2"
  if [[ -n "$API_TOKEN" ]]; then
    curl -sS -H "Authorization: Bearer $API_TOKEN" "$url" -o "$output"
  else
    curl -sS "$url" -o "$output"
  fi
}

get_page() {
  local url="$1"
  if [[ -n "$API_TOKEN" ]]; then
    curl -sS -H "Authorization: Bearer $API_TOKEN" "$url"
  else
    curl -sS "$url"
  fi
}

wait_run_status() {
  local project_id="$1"
  local run_id="$2"
  local expected="$3"
  local status body
  for _ in $(seq 1 "$POLL_RETRIES"); do
    body="$(request_json GET "/projects/$project_id/runs/$run_id/status")"
    status="$(printf '%s' "$body" | json_query "run.status")"
    if [[ "$status" == "$expected" ]]; then
      printf '%s' "$body"
      return 0
    fi
    if [[ "$status" == "FAILED" ]]; then
      echo "Run $run_id failed unexpectedly" >&2
      printf '%s\n' "$body" >&2
      exit 1
    fi
    sleep "$POLL_INTERVAL"
  done
  echo "Run $run_id did not reach $expected in time" >&2
  exit 1
}

wait_preview_ready() {
  local project_id="$1"
  local preview_path="$2"
  local body status
  for _ in $(seq 1 "$POLL_RETRIES"); do
    body="$(request_json GET "$preview_path/status")"
    status="$(printf '%s' "$body" | json_query "status")"
    if [[ "$status" == "READY" ]]; then
      printf '%s' "$body"
      return 0
    fi
    sleep "$POLL_INTERVAL"
  done
  echo "Preview did not become READY in time" >&2
  exit 1
}

assert_zip_contains() {
  local zip_path="$1"
  shift
  local listing
  listing="$(unzip -Z1 "$zip_path")"
  for expected in "$@"; do
    assert_contains "$listing" "$expected" "zip missing $expected"
  done
}

assert_delivery_gate_contract() {
  local zip_path="$1"
  /usr/bin/python3 - "$zip_path" <<'PY'
import json
import sys
import zipfile

zip_path = sys.argv[1]
required_paths = {
    "README.md",
    "docker-compose.yml",
    "generated-app/go.mod",
    "generated-app/main.go",
    "generated-app/Dockerfile",
    "web-app/package.json",
    "web-app/server.js",
    "web-app/index.html",
    "web-app/Dockerfile",
    "metadata/prd.json",
    "metadata/task.json",
    "metadata/run.json",
    "metadata/release-gate.json",
}

with zipfile.ZipFile(zip_path) as archive:
    names = set(archive.namelist())
    missing = sorted((required_paths | {"metadata/manifest.json"}) - names)
    if missing:
        raise SystemExit("delivery bundle missing required paths: " + ", ".join(missing))
    manifest = json.loads(archive.read("metadata/manifest.json"))
    gate = json.loads(archive.read("metadata/release-gate.json"))

if manifest.get("schemaVersion") != "delivery.bundle.v1":
    raise SystemExit("manifest schemaVersion must be delivery.bundle.v1")
if manifest.get("kind") != "delivery_bundle":
    raise SystemExit("manifest kind must be delivery_bundle")
release_gate = manifest.get("releaseGate") or {}
if release_gate.get("path") != "metadata/release-gate.json" or release_gate.get("status") != "PASS":
    raise SystemExit("manifest releaseGate must point to PASS metadata/release-gate.json")
if gate.get("schemaVersion") != "delivery.release_gate.v1" or gate.get("status") != "PASS":
    raise SystemExit("release gate must be PASS delivery.release_gate.v1")
entrypoints = manifest.get("entrypoints") or {}
if entrypoints.get("frontend") != "http://127.0.0.1:3000":
    raise SystemExit("manifest frontend entrypoint mismatch")
if entrypoints.get("backendHealth") != "http://127.0.0.1:8081/health":
    raise SystemExit("manifest backend health entrypoint mismatch")
if entrypoints.get("composeFile") != "docker-compose.yml":
    raise SystemExit("manifest compose file entrypoint mismatch")
files = {item.get("path"): item for item in manifest.get("files") or []}
for path in sorted(required_paths):
    item = files.get(path)
    if not item:
        raise SystemExit(f"manifest missing descriptor for {path}")
    if item.get("required") is not True:
        raise SystemExit(f"manifest descriptor for {path} must be required")
    if not item.get("sha256"):
        raise SystemExit(f"manifest descriptor for {path} missing sha256")
    if int(item.get("sizeBytes") or 0) <= 0:
        raise SystemExit(f"manifest descriptor for {path} missing positive sizeBytes")
if "metadata/manifest.json" in files:
    raise SystemExit("manifest must not include self-referential metadata/manifest.json descriptor")
PY
}

run_demo_once() {
  local index="$1"
  local suffix
  suffix="$(date +%s)-$index"

  echo
  echo "========== DEMO RUN $index/$RUNS =========="

  local project_json project_id
  project_json="$(request_json POST "/projects" "{\"name\":\"Todo Demo $suffix\",\"description\":\"Sprint 4 walkthrough $suffix\"}")"
  project_id="$(printf '%s' "$project_json" | json_query "id")"
  [[ -n "$project_id" ]] || { echo "missing project id" >&2; exit 1; }
  echo "project: $project_id"

  request_json POST "/projects/$project_id/requirements" '{"title":"实现 Todo 全栈功能","content":"实现 Todo 全栈功能，覆盖契约、通信日志、预览和标准交付。","constraints":["后端使用 Go","前端使用原生 HTML"],"acceptanceHints":["可查看通信日志","可查看 Token 成本趋势","可启动预览","可导出标准交付包"]}' >/dev/null

  local plan_json seed_task_id
  plan_json="$(request_json POST "/projects/$project_id/plan" '{}')"
  seed_task_id="$(printf '%s' "$plan_json" | json_query "task.id")"
  [[ -n "$seed_task_id" ]] || { echo "missing seed task id" >&2; exit 1; }

  local contract_json contract_id
  contract_json="$(request_json POST "/projects/$project_id/contracts/generate" '{}')"
  contract_id="$(printf '%s' "$contract_json" | json_query "id")"
  [[ -n "$contract_id" ]] || { echo "missing contract id" >&2; exit 1; }

  local dispatch_json backend_task_id frontend_task_id
  dispatch_json="$(request_json POST "/projects/$project_id/tasks/dispatch" '{}')"
  backend_task_id="$(printf '%s' "$dispatch_json" | json_query "tasks.0.id")"
  frontend_task_id="$(printf '%s' "$dispatch_json" | json_query "tasks.1.id")"
  [[ -n "$backend_task_id" && -n "$frontend_task_id" ]] || { echo "missing dispatched task ids" >&2; exit 1; }

  request_json POST "/projects/$project_id/tasks/$backend_task_id/context/generate" '{}' >/dev/null
  request_json POST "/projects/$project_id/tasks/$frontend_task_id/context/generate" '{}' >/dev/null

  local parallel_json
  parallel_json="$(request_json POST "/projects/$project_id/runs/parallel" "{\"taskIds\":[\"$backend_task_id\",\"$frontend_task_id\"]}")"
  local run_ids
  run_ids="$(printf '%s' "$parallel_json" | grep -o 'run_[a-z0-9]*' | awk '!seen[$0]++')"
  [[ -n "$run_ids" ]] || { echo "missing run ids" >&2; exit 1; }
  for run_id in $run_ids; do
    wait_run_status "$project_id" "$run_id" "SUCCEEDED" >/dev/null
  done

  local comm_json
  comm_json="$(request_json GET "/projects/$project_id/communications?taskId=$backend_task_id")"
  assert_contains "$comm_json" '"taskId":"'"$backend_task_id"'"' "communications filter should include backend task"
  assert_contains "$comm_json" '"checksum":"' "communications should include checksum"

  local cost_json
  cost_json="$(request_json GET "/projects/$project_id/token-costs?taskId=$backend_task_id")"
  assert_contains "$cost_json" '"taskId":"'"$backend_task_id"'"' "token cost filter should include backend task"
  assert_contains "$cost_json" '"totalTokens":' "token cost response should include totals"

  request_json POST "/projects/$project_id/shared-sandbox/merge" "{\"taskIds\":[\"$backend_task_id\",\"$frontend_task_id\"],\"contractId\":\"$contract_id\",\"endpoints\":$(printf '%s' "$contract_json" | json_query "endpoints"),\"schemas\":$(printf '%s' "$contract_json" | json_query "schemas")}" >/dev/null

  local preview_json preview_url preview_status_html preview_status_json
  preview_json="$(request_json POST "/projects/$project_id/preview/start" '{}')"
  preview_url="$(printf '%s' "$preview_json" | json_query "preview.url")"
  [[ -n "$preview_url" ]] || { echo "missing preview url" >&2; exit 1; }
  preview_status_json="$(wait_preview_ready "$project_id" "$preview_url")"
  assert_contains "$preview_status_json" '"revision":"' "preview status should include revision"
  preview_status_html="$(get_page "$BASE_URL$preview_url")"
  assert_contains "$preview_status_html" 'Todo Preview Workspace' "preview page should render"
  assert_contains "$preview_status_html" 'Hot reload watching' "preview page should expose hot reload text"

  local seed_run_json seed_run_id
  seed_run_json="$(request_json POST "/projects/$project_id/tasks/run" "{\"taskId\":\"$seed_task_id\"}")"
  seed_run_id="$(printf '%s' "$seed_run_json" | json_query "run.id")"
  [[ -n "$seed_run_id" ]] || { echo "missing seed run id" >&2; exit 1; }
  wait_run_status "$project_id" "$seed_run_id" "SUCCEEDED" >/dev/null

  local export_json download_path artifact_zip
  export_json="$(request_json POST "/projects/$project_id/delivery/export" "{\"runId\":\"$seed_run_id\"}")"
  download_path="$(printf '%s' "$export_json" | json_query "downloadPath")"
  [[ -n "$download_path" ]] || { echo "missing download path" >&2; exit 1; }
  artifact_zip="$ARTIFACT_DIR/$project_id.zip"
  download_file "$BASE_URL$download_path" "$artifact_zip"
  assert_zip_contains "$artifact_zip" \
    "README.md" \
    "docker-compose.yml" \
    "generated-app/go.mod" \
    "generated-app/main.go" \
    "generated-app/Dockerfile" \
    "web-app/package.json" \
    "web-app/server.js" \
    "web-app/index.html" \
    "web-app/Dockerfile" \
    "metadata/manifest.json" \
    "metadata/release-gate.json"
  assert_delivery_gate_contract "$artifact_zip"

  local panel_html
  panel_html="$(get_page "$BASE_URL/status/panel")"
  assert_contains "$panel_html" 'Agent Message Log' "status panel should include communications section"
  assert_contains "$panel_html" 'Audit Trail' "status panel should include audit section"
  assert_contains "$panel_html" 'Token Cost Trend' "status panel should include token cost section"

  echo "demo run $index passed"
  echo "artifact: $artifact_zip"
  echo "status panel: $BASE_URL/status/panel"
}

echo "Using BASE_URL=$BASE_URL"
if [[ -n "$API_TOKEN" ]]; then
  echo "Using API token authentication"
fi
mkdir -p "$ARTIFACT_DIR"
echo "Artifacts will be downloaded to $ARTIFACT_DIR"

for run_index in $(seq 1 "$RUNS"); do
  run_demo_once "$run_index"
done

echo
echo "All demo runs completed successfully."
