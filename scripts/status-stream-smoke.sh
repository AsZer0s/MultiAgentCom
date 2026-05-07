#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
API_TOKEN="${API_TOKEN:-${MULTI_AGENT_API_TOKEN:-}}"
POLL_RETRIES="${POLL_RETRIES:-60}"
POLL_INTERVAL="${POLL_INTERVAL:-0.2}"
STREAM_TIMEOUT="${STREAM_TIMEOUT:-5}"
LOG_FILE="${LOG_FILE:-${TMPDIR:-/tmp}/multiagentcom-status-stream-smoke.log}"

cleanup() {
  rm -f "${HEADERS_FILE:-}" "${BODY_FILE:-}"
  if [[ -n "${SERVER_PID:-}" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

assert_contains() {
  local haystack="$1"
  local needle="$2"
  local message="$3"
  if [[ "$haystack" != *"$needle"* ]]; then
    echo "ASSERT FAILED: $message" >&2
    exit 1
  fi
}

wait_for_health() {
  for _ in $(seq 1 "$POLL_RETRIES"); do
    if curl -sS "$BASE_URL/health" >/dev/null 2>&1; then
      return 0
    fi
    sleep "$POLL_INTERVAL"
  done
  return 1
}

if curl -sS "$BASE_URL/health" >/dev/null 2>&1; then
  echo "Using existing server at $BASE_URL"
else
  echo "Starting local server for status stream smoke at $BASE_URL"
  (cd "$ROOT_DIR" && go run ./cmd/server >"$LOG_FILE" 2>&1) &
  SERVER_PID="$!"
  if ! wait_for_health; then
    echo "Server did not become healthy at $BASE_URL" >&2
    if [[ -f "$LOG_FILE" ]]; then
      tail -n 50 "$LOG_FILE" >&2
    fi
    exit 1
  fi
fi

HEADERS_FILE="$(mktemp)"
BODY_FILE="$(mktemp)"

stream_url="$BASE_URL/status/stream"
curl_args=(-s -N --max-time "$STREAM_TIMEOUT" -H "Accept: text/event-stream")
if [[ -n "$API_TOKEN" ]]; then
  curl_args+=(-H "Authorization: Bearer $API_TOKEN")
fi

set +e
curl "${curl_args[@]}" -D "$HEADERS_FILE" "$stream_url" >"$BODY_FILE"
curl_exit="$?"
set -e

if [[ "$curl_exit" -ne 0 && "$curl_exit" -ne 28 ]]; then
  echo "curl failed for $stream_url (exit=$curl_exit)" >&2
  cat "$HEADERS_FILE" >&2 || true
  cat "$BODY_FILE" >&2 || true
  exit 1
fi

status_code="$(awk '/^HTTP\/[0-9.]+/ {code=$2} END {print code}' "$HEADERS_FILE")"
if [[ "$status_code" != "200" ]]; then
  echo "Expected HTTP 200 for /status/stream, got ${status_code:-unknown}" >&2
  cat "$HEADERS_FILE" >&2
  cat "$BODY_FILE" >&2 || true
  exit 1
fi

content_type="$(tr -d '\r' <"$HEADERS_FILE" | awk 'BEGIN{IGNORECASE=1} /^Content-Type:/ {sub(/^[^:]+:[[:space:]]*/, "", $0); print; exit}')"
assert_contains "$content_type" "event-stream" "status stream should return event-stream content type"

stream_body="$(cat "$BODY_FILE")"
assert_contains "$stream_body" "event: status" "status stream should include event: status"
assert_contains "$stream_body" "data:" "status stream should include data field"

echo "Status stream smoke passed for $stream_url"
