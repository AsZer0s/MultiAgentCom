#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

echo "== hardcoded secret scan =="

SCAN_OUTPUT="$(mktemp "${TMPDIR:-/tmp}/multiagentcom-secret-scan.XXXXXX")"
cleanup() {
  rm -f "$SCAN_OUTPUT"
}
trap cleanup EXIT

PATTERNS=(
  'BEGIN (RSA|EC|OPENSSH|DSA|PGP) PRIVATE KEY'
  'AKIA[0-9A-Z]{16}'
  'sk-[A-Za-z0-9]{20,}'
  'ghp_[A-Za-z0-9]{20,}'
  'AIza[0-9A-Za-z\-_]{20,}'
)

SCAN_TARGETS=(
  cmd
  internal
  scripts
  .github
  go.mod
  README.md
)

for pattern in "${PATTERNS[@]}"; do
  if rg -n --hidden --glob '!runtime/**' --glob '!docs/**' -e "$pattern" "${SCAN_TARGETS[@]}" >"$SCAN_OUTPUT" 2>/dev/null; then
    echo "Potential secret detected for pattern: $pattern" >&2
    cat "$SCAN_OUTPUT" >&2
    exit 1
  fi
done

echo "Security check passed."
