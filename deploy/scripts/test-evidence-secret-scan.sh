#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/resonance-evidence-scan.XXXXXX")
cleanup() {
  rm -rf "$test_dir"
}
trap cleanup EXIT

printf '%s\n' '{"Status":"running","Running":true,"Health":"healthy","RestartCount":0}' \
  >"$test_dir/safe.json"
deploy/scripts/check-evidence-secrets.sh "$test_dir/safe.json" >/dev/null

printf '%s\n' '{"SERVICE_SIGNING_SECRET":"synthetic-test-value"}' \
  >"$test_dir/unsafe.json"
if deploy/scripts/check-evidence-secrets.sh "$test_dir" >/dev/null 2>&1; then
  echo "sensitive-field scanner accepted a secret-bearing fixture" >&2
  exit 1
fi

echo "evidence sensitive-field scanner self-test: PASS"
