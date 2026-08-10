#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/resonance-evidence-scan.XXXXXX")
cleanup() {
  rm -rf "$test_dir"
}
trap cleanup EXIT

printf '%s\n' '{"Status":"running","Running":true,"Health":"healthy","RestartCount":0,"first_token_ms":12,"model_tokens":15}' \
  >"$test_dir/safe.json"
deploy/scripts/check-evidence-secrets.sh "$test_dir/safe.json" >/dev/null

printf '%s\n' '{"SERVICE_SIGNING_SECRET":"synthetic-test-value"}' \
  >"$test_dir/unsafe.json"
printf '%s\n' '{"access_token":"synthetic-test-value"}' \
  >"$test_dir/unsafe-token.json"
set +e
deploy/scripts/check-evidence-secrets.sh "$test_dir" >/dev/null 2>&1
match_rc=$?
set -e
if [[ "$match_rc" -ne 1 ]]; then
  echo "sensitive-field scanner returned $match_rc for a secret-bearing fixture, expected 1" >&2
  exit 1
fi

error_bin="$test_dir/error-bin"
missing_bin="$test_dir/missing-bin"
mkdir -p "$error_bin" "$missing_bin"
printf '%s\n' '#!/bin/sh' 'exit 2' >"$error_bin/rg"
chmod +x "$error_bin/rg"

set +e
PATH="$error_bin:/usr/bin:/bin" /bin/bash \
  deploy/scripts/check-evidence-secrets.sh "$test_dir/safe.json" >/dev/null 2>&1
error_rc=$?
set -e
if [[ "$error_rc" -ne 2 ]]; then
  echo "sensitive-field scanner returned $error_rc for rg failure, expected 2" >&2
  exit 1
fi

set +e
PATH="$missing_bin" /bin/bash \
  deploy/scripts/check-evidence-secrets.sh "$test_dir/safe.json" >/dev/null 2>&1
missing_rc=$?
set -e
if [[ "$missing_rc" -ne 2 ]]; then
  echo "sensitive-field scanner returned $missing_rc for missing rg, expected 2" >&2
  exit 1
fi

echo "evidence sensitive-field scanner self-test: PASS"
