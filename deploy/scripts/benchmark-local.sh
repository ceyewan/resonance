#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
OUT_DIR=${BENCHMARK_OUT_DIR:-artifacts/local-v1/benchmark-$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$OUT_DIR"

go run ./cmd/local-benchmark -base-url http://127.0.0.1:18080 -count 20 -output "$OUT_DIR/business.json"
start=$(date +%s%N)
go test ./pilot/coordinator ./pilot/toolbroker ./pilot/mutation -count=1 >"$OUT_DIR/agent-contract.log"
end=$(date +%s%N)
jq -n --argjson elapsed_ns "$((end-start))" '{kind:"deterministic-agent-tool-approval-contract",elapsed_ns:$elapsed_ns}' >"$OUT_DIR/agent.json"
{
  uname -a
  docker version
  docker info --format '{{json .}}'
  git rev-parse HEAD
  go list -m github.com/ceyewan/genesis
} >"$OUT_DIR/environment.txt"
echo "$OUT_DIR"
