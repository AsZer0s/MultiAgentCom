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
echo "== start run =="
RUN_JSON="$(curl -sS -X POST "$BASE_URL/projects/$PROJECT_ID/tasks/run" \
  -H 'Content-Type: application/json' \
  -d "{\"taskId\":\"$TASK_ID\"}")"
echo "$RUN_JSON"
RUN_ID="$(printf '%s' "$RUN_JSON" | sed -n 's/.*"run":{"id":"\([^"]*\)".*/\1/p')"

echo
echo "== poll status =="
for _ in $(seq 1 20); do
  STATUS_JSON="$(curl -sS "$BASE_URL/projects/$PROJECT_ID/runs/$RUN_ID/status")"
  echo "$STATUS_JSON"
  if printf '%s' "$STATUS_JSON" | grep -q '"status":"SUCCEEDED"'; then
    break
  fi
  sleep 0.2
done

echo
echo "== export delivery =="
curl -sS -X POST "$BASE_URL/projects/$PROJECT_ID/delivery/export" \
  -H 'Content-Type: application/json' \
  -d "{\"runId\":\"$RUN_ID\"}"
