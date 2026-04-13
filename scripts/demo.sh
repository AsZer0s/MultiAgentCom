#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"

echo "== create project =="
PROJECT_JSON="$(curl -sS -X POST "$BASE_URL/projects" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Todo Demo","description":"Sprint 1 walkthrough"}')"
echo "$PROJECT_JSON"
PROJECT_ID="$(printf '%s' "$PROJECT_JSON" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"

echo
echo "== add requirement =="
curl -sS -X POST "$BASE_URL/projects/$PROJECT_ID/requirements" \
  -H 'Content-Type: application/json' \
  -d '{"title":"实现 Todo 列表的增删改查","content":"实现 Todo 列表的增删改查，并生成最小交付包。","constraints":["后端使用 Go","前端后续补充"],"acceptanceHints":["可生成结构化 PRD","可导出 ZIP 交付包"]}' \
  | tee /dev/stderr >/dev/null

echo
echo "== generate plan =="
PLAN_JSON="$(curl -sS -X POST "$BASE_URL/projects/$PROJECT_ID/plan" -H 'Content-Type: application/json' -d '{}')"
echo "$PLAN_JSON"
TASK_ID="$(printf '%s' "$PLAN_JSON" | sed -n 's/.*"task":{"id":"\([^"]*\)".*/\1/p')"

echo
echo "== generate contract =="
CONTRACT_JSON="$(curl -sS -X POST "$BASE_URL/projects/$PROJECT_ID/contracts/generate" -H 'Content-Type: application/json' -d '{}')"
echo "$CONTRACT_JSON"
CONTRACT_ID="$(printf '%s' "$CONTRACT_JSON" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"

echo
echo "== validate conflicting contract candidate =="
curl -sS -X POST "$BASE_URL/projects/$PROJECT_ID/contracts/validate" \
  -H 'Content-Type: application/json' \
  -d "{\"contractId\":\"$CONTRACT_ID\",\"endpoints\":[{\"name\":\"ListTodo\",\"method\":\"GET\",\"path\":\"/api/todos\"}],\"schemas\":[{\"name\":\"Todo\",\"fields\":[{\"name\":\"id\",\"type\":\"string\",\"required\":true},{\"name\":\"title\",\"type\":\"number\",\"required\":true}]}]}" \
  | tee /dev/stderr >/dev/null

echo
echo "== dispatch frontend/backend tasks =="
DISPATCH_JSON="$(curl -sS -X POST "$BASE_URL/projects/$PROJECT_ID/tasks/dispatch" -H 'Content-Type: application/json' -d '{}')"
echo "$DISPATCH_JSON"
BACKEND_TASK_ID="$(printf '%s' "$DISPATCH_JSON" | sed -n 's/.*"tasks":\[{"id":"\([^"]*\)".*/\1/p')"
FRONTEND_TASK_ID="$(printf '%s' "$DISPATCH_JSON" | sed -n 's/.*"tasks":\[{"id":"[^"]*".*{"id":"\([^"]*\)".*/\1/p')"

echo
echo "== generate backend context =="
curl -sS -X POST "$BASE_URL/projects/$PROJECT_ID/tasks/$BACKEND_TASK_ID/context/generate" \
  -H 'Content-Type: application/json' \
  -d '{}' \
  | tee /dev/stderr >/dev/null

echo
echo "== generate frontend context =="
curl -sS -X POST "$BASE_URL/projects/$PROJECT_ID/tasks/$FRONTEND_TASK_ID/context/generate" \
  -H 'Content-Type: application/json' \
  -d '{}' \
  | tee /dev/stderr >/dev/null

echo
echo "== start parallel runs =="
PARALLEL_JSON="$(curl -sS -X POST "$BASE_URL/projects/$PROJECT_ID/runs/parallel" \
  -H 'Content-Type: application/json' \
  -d "{\"taskIds\":[\"$BACKEND_TASK_ID\",\"$FRONTEND_TASK_ID\"]}")"
echo "$PARALLEL_JSON"

for RUN_ID in $(printf '%s' "$PARALLEL_JSON" | grep -o 'run_[a-z0-9]*'); do
  echo
  echo "== poll run $RUN_ID =="
  for _ in $(seq 1 20); do
    STATUS_JSON="$(curl -sS "$BASE_URL/projects/$PROJECT_ID/runs/$RUN_ID/status")"
    echo "$STATUS_JSON"
    if printf '%s' "$STATUS_JSON" | grep -q '"status":"SUCCEEDED"'; then
      break
    fi
    sleep 0.2
  done
done

echo
echo "== status matrix =="
curl -sS "$BASE_URL/status/matrix?projectId=$PROJECT_ID"

echo
echo "== open status panel in browser manually if needed =="
echo "$BASE_URL/status/panel"

echo
echo "== optional sprint 1 delivery export (seed task) =="
RUN_JSON="$(curl -sS -X POST "$BASE_URL/projects/$PROJECT_ID/tasks/run" \
  -H 'Content-Type: application/json' \
  -d "{\"taskId\":\"$TASK_ID\"}")"
echo "$RUN_JSON"
SEED_RUN_ID="$(printf '%s' "$RUN_JSON" | sed -n 's/.*"run":{"id":"\([^"]*\)".*/\1/p')"
for _ in $(seq 1 20); do
  STATUS_JSON="$(curl -sS "$BASE_URL/projects/$PROJECT_ID/runs/$SEED_RUN_ID/status")"
  if printf '%s' "$STATUS_JSON" | grep -q '"status":"SUCCEEDED"'; then
    break
  fi
  sleep 0.2
done
curl -sS -X POST "$BASE_URL/projects/$PROJECT_ID/delivery/export" \
  -H 'Content-Type: application/json' \
  -d "{\"runId\":\"$SEED_RUN_ID\"}"
