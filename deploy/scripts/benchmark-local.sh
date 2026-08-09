#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
OUT_DIR=${BENCHMARK_OUT_DIR:-artifacts/local-v1/benchmark-$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$OUT_DIR"
PREFIX=${RESONANCE_BENCHMARK_PREFIX:-lb-$(date -u +%m%d%H%M%S)-$$}
if [[ ! "$PREFIX" =~ ^[A-Za-z0-9_-]{1,24}$ ]]; then
  echo "unsafe benchmark prefix" >&2
  exit 2
fi
cleanup() {
  deploy/scripts/cleanup-local-test-data.sh "$PREFIX"
}
trap cleanup EXIT
COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)

go run ./cmd/local-benchmark -base-url http://127.0.0.1:18080 -count 20 \
  -prefix "$PREFIX-business" -output "$OUT_DIR/business.json"
deploy/scripts/run-deterministic-agent-e2e.sh "$PREFIX" "$OUT_DIR/agent.json" \
  >"$OUT_DIR/agent-e2e.log"
go test ./pilot/coordinator ./pilot/toolbroker ./pilot/mutation -count=1 \
  >"$OUT_DIR/agent-contract.log"

docker stats --no-stream --format '{{json .}}' | jq -s '.' >"$OUT_DIR/container-resources.json"
metric_query='{__name__=~"go_goroutines|go_sql_.*|logic_outbox_backlog|task_gateway_queue_depth|task_push_enqueue_failed_total|pilot_run_queue_wait_seconds_.*|pilot_first_token_seconds_.*|pilot_run_duration_seconds_.*|pilot_active_runs|container_cpu_usage_seconds_total|container_memory_working_set_bytes"}'
encoded_query=$(jq -rn --arg query "$metric_query" '$query|@uri')
"${COMPOSE[@]}" exec -T grafana wget -qO- \
  "http://prometheus:9090/api/v1/query?query=$encoded_query" >"$OUT_DIR/prometheus-resources.json"
jq -e '.status=="success" and (.data.result|length)>0' "$OUT_DIR/prometheus-resources.json" >/dev/null
{
  uname -a
  docker version
  docker info --format '{{json .}}'
  git rev-parse HEAD
  go list -m github.com/ceyewan/genesis
} >"$OUT_DIR/environment.txt"
echo "$OUT_DIR"
