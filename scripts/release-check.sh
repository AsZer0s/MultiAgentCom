#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
LOG_FILE="${LOG_FILE:-${TMPDIR:-/tmp}/multiagentcom-release-check.log}"
DEMO_RUNS="${DEMO_RUNS:-3}"

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "== syntax checks =="
bash -n "$ROOT_DIR/scripts/demo.sh"
bash -n "$ROOT_DIR/scripts/release-check.sh"
bash -n "$ROOT_DIR/scripts/security-check.sh"

echo
echo "== security checks =="
bash "$ROOT_DIR/scripts/security-check.sh"

echo
echo "== regression tests =="
(cd "$ROOT_DIR" && go test ./...)

echo
echo "== start local server =="
(cd "$ROOT_DIR" && go run ./cmd/server >"$LOG_FILE" 2>&1) &
SERVER_PID="$!"

for _ in $(seq 1 60); do
  if curl -sS "$BASE_URL/health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done
curl -sS "$BASE_URL/health" >/dev/null

echo
echo "== end-to-end demo verification =="
RUNS="$DEMO_RUNS" BASE_URL="$BASE_URL" ARTIFACT_DIR="${TMPDIR:-/tmp}/multiagentcom-release-artifacts" bash "$ROOT_DIR/scripts/demo.sh"

echo
echo "== docs check =="
test -f "$ROOT_DIR/docs/Release-Checklist.md"
test -f "$ROOT_DIR/docs/Sprint-4-Acceptance-Report.md"

echo
echo "Release check passed."
