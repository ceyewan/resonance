#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
OUT_DIR=${BENCHMARK_OUT_DIR:-artifacts/local-v1/benchmark-$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$OUT_DIR"
OUT_DIR=$(cd "$OUT_DIR" && pwd)
PREFIX=${RESONANCE_BENCHMARK_PREFIX:-lb-$(date -u +%m%d%H%M%S)-$$}
if [[ ! "$PREFIX" =~ ^[A-Za-z0-9_-]{1,24}$ ]]; then
  echo "unsafe benchmark prefix" >&2
  exit 2
fi
sampler_pid=""
cleanup() {
  if [[ -n "$sampler_pid" ]]; then
    kill "$sampler_pid" >/dev/null 2>&1 || true
    wait "$sampler_pid" >/dev/null 2>&1 || true
  fi
  deploy/scripts/cleanup-local-test-data.sh "$PREFIX"
}
trap cleanup EXIT
COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)
export GOWORK=off
export RESONANCE_VERSION=${RESONANCE_VERSION:-$(git rev-parse --short=12 HEAD)}
deploy/scripts/verify-genesis-rc2-identity.sh >"$OUT_DIR/genesis-rc2-identity.json"

jq -n '{
  schema_version:1,seed:20260809,concurrency:1,message_count:20,
  suite_timeout_seconds:120,event_timeout_seconds:10,inbox_timeout_seconds:10,
  agent_provider:"local-deterministic",agent_cloud_access:false
}' >"$OUT_DIR/benchmark-parameters.json"

benchmark_started_s=$(date +%s)
sample_container_resources() {
  while true; do
    collected_at=$(date +%s)
    docker stats --no-stream --format '{{json .}}' |
      jq -c --argjson collected_at "$collected_at" '. + {collected_at_unix:$collected_at}'
    sleep 1
  done
}
sample_container_resources >"$OUT_DIR/container-resources.jsonl" &
sampler_pid=$!
go run ./cmd/local-benchmark -base-url http://127.0.0.1:18080 -count 20 \
  -prefix "$PREFIX-business" -output "$OUT_DIR/business.json"
deploy/scripts/run-deterministic-agent-e2e.sh "$PREFIX" "$OUT_DIR/agent.json" \
  >"$OUT_DIR/agent-e2e.log"
go test ./pilot/coordinator ./pilot/toolbroker ./pilot/mutation -count=1 \
  >"$OUT_DIR/agent-contract.log"
benchmark_finished_s=$(date +%s)
kill "$sampler_pid" >/dev/null 2>&1 || true
wait "$sampler_pid" >/dev/null 2>&1 || true
sampler_pid=""
jq -s '.' "$OUT_DIR/container-resources.jsonl" >"$OUT_DIR/container-resources.json"
jq -e 'length > 0' "$OUT_DIR/container-resources.json" >/dev/null
jq -n --argjson start "$benchmark_started_s" --argjson end "$benchmark_finished_s" \
  '{start_unix:$start,end_unix:$end,duration_seconds:($end-$start),requested_sample_interval_seconds:1}' \
  >"$OUT_DIR/resource-sampling-window.json"

"${COMPOSE[@]}" images --format json | jq '
  map({
    service:(.ContainerName | sub("^resonance-v1-"; "") | sub("-[0-9]+$"; "")),
    container:.ContainerName,
    image:.Repository,
    tag:.Tag,
    id:.ID,
    platform:.Platform,
    size:.Size
  })
' >"$OUT_DIR/compose-images.json"
metric_query='{__name__=~"go_goroutines|go_sql_.*|logic_outbox_backlog|task_gateway_queue_depth|task_push_enqueue_failed_total|pilot_run_queue_wait_seconds_.*|pilot_first_token_seconds_.*|pilot_run_duration_seconds_.*|pilot_active_runs|container_cpu_usage_seconds_total|container_memory_working_set_bytes"}'
encoded_query=$(jq -rn --arg query "$metric_query" '$query|@uri')
"${COMPOSE[@]}" exec -T grafana wget -qO- \
  "http://prometheus:9090/api/v1/query_range?query=$encoded_query&start=$benchmark_started_s&end=$benchmark_finished_s&step=1" >"$OUT_DIR/prometheus-resources.json"
jq -e '.status=="success" and (.data.result|length)>0 and any(.data.result[]; (.values|length)>0)' "$OUT_DIR/prometheus-resources.json" >/dev/null
host_kernel=$(uname -a)
docker_client_version=$(docker version --format '{{.Client.Version}}')
docker_server_version=$(docker version --format '{{.Server.Version}}')
docker_context=$(docker context show)
docker_server_os=$(docker info --format '{{.OperatingSystem}}')
docker_server_arch=$(docker info --format '{{.Architecture}}')
docker_server_kernel=$(docker info --format '{{.KernelVersion}}')
docker_server_cpus=$(docker info --format '{{.NCPU}}')
docker_server_memory=$(docker info --format '{{.MemTotal}}')
resonance_sha=$(git rev-parse HEAD)
jq -n \
  --arg host_kernel "$host_kernel" \
  --arg docker_client_version "$docker_client_version" \
  --arg docker_server_version "$docker_server_version" \
  --arg docker_context "$docker_context" \
  --arg docker_server_os "$docker_server_os" \
  --arg docker_server_arch "$docker_server_arch" \
  --arg docker_server_kernel "$docker_server_kernel" \
  --argjson docker_server_cpus "$docker_server_cpus" \
  --argjson docker_server_memory_bytes "$docker_server_memory" \
  --arg resonance_sha "$resonance_sha" \
  '{schema_version:1,host_kernel:$host_kernel,docker:{client_version:$docker_client_version,server_version:$docker_server_version,context:$docker_context,server_os:$docker_server_os,server_architecture:$docker_server_arch,server_kernel:$docker_server_kernel,server_cpus:$docker_server_cpus,server_memory_bytes:$docker_server_memory_bytes},resonance_sha:$resonance_sha}' \
  >"$OUT_DIR/environment.json"
shasum -a 256 deploy/base.yaml deploy/services.yaml deploy/services.local.yaml deploy/observability.yaml >"$OUT_DIR/compose-files.sha256"
deploy/scripts/check-evidence-secrets.sh "$OUT_DIR" >"$OUT_DIR/sensitive-field-scan.log"
echo "$OUT_DIR"
