#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
LOG_FILE="${LOG_FILE:-${TMPDIR:-/tmp}/multiagentcom-release-check.log}"
DEMO_RUNS="${DEMO_RUNS:-3}"
ALERT_WEBHOOK_PORT="${ALERT_WEBHOOK_PORT:-18081}"
ALERT_WEBHOOK_LOG="${ALERT_WEBHOOK_LOG:-${TMPDIR:-/tmp}/multiagentcom-alert-webhook.log}"

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  if [[ -n "${WEBHOOK_PID:-}" ]]; then
    kill "$WEBHOOK_PID" >/dev/null 2>&1 || true
    wait "$WEBHOOK_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "== syntax checks =="
bash -n "$ROOT_DIR/scripts/demo.sh"
bash -n "$ROOT_DIR/scripts/alert-smoke.sh"
bash -n "$ROOT_DIR/scripts/release-check.sh"
bash -n "$ROOT_DIR/scripts/security-check.sh"

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
(cd "$ROOT_DIR" && MULTI_AGENT_ALERT_WEBHOOK_URL="http://127.0.0.1:$ALERT_WEBHOOK_PORT" go run ./cmd/server >"$LOG_FILE" 2>&1) &
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
echo "== alert webhook smoke verification =="
BASE_URL="$BASE_URL" ALERT_WEBHOOK_LOG="$ALERT_WEBHOOK_LOG" bash "$ROOT_DIR/scripts/alert-smoke.sh"

echo
echo "== docs check =="
test -f "$ROOT_DIR/docs/Release-Checklist.md"
test -f "$ROOT_DIR/docs/Sprint-4-Acceptance-Report.md"

echo
echo "Release check passed."
