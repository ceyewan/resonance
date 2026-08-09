#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)
EVIDENCE_DIR=${EVIDENCE_DIR:-artifacts/local-v1/recovery-$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$EVIDENCE_DIR"
PREFIX=${RESONANCE_RECOVERY_PREFIX:-rc-$(date -u +%m%d%H%M%S)-$$}
if [[ ! "$PREFIX" =~ ^[A-Za-z0-9_-]{1,20}$ ]]; then
  echo "unsafe recovery prefix" >&2
  exit 2
fi
telemetry_paused=0
cleanup() {
  if [[ "$telemetry_paused" -eq 1 ]]; then
    "${COMPOSE[@]}" unpause alloy loki tempo >/dev/null 2>&1 || true
  fi
  deploy/scripts/cleanup-local-test-data.sh "$PREFIX"
}
trap cleanup EXIT

wait_ready() {
  local url=$1
  for _ in $(seq 1 60); do curl -fsS --max-time 2 "$url" >/dev/null && return 0; sleep 2; done
  return 1
}

wait_internal() {
  local url=$1
  for _ in $(seq 1 60); do
    "${COMPOSE[@]}" exec -T grafana wget -qO- "$url" >/dev/null 2>&1 && return 0
    sleep 2
  done
  return 1
}

wait_service_healthy() {
  local service=$1
  local container status
  for _ in $(seq 1 60); do
    container=$("${COMPOSE[@]}" ps -q "$service")
    if [[ -n "$container" ]]; then
      status=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container")
      [[ "$status" == "healthy" || "$status" == "running" ]] && return 0
    fi
    sleep 2
  done
  return 1
}

record_duration() {
  local kind=$1 name=$2 started=$3 finished=$4
  jq -nc --arg kind "$kind" --arg name "$name" --argjson duration_ms "$(((finished-started)/1000000))" \
    '{kind:$kind,name:$name,duration_ms:$duration_ms,status:"recovered"}' >>"$EVIDENCE_DIR/recovery-times.jsonl"
}

run_im_probe() {
  local name=$1
  go run ./cmd/local-im-e2e -base-url http://127.0.0.1:18080 \
    -prefix "$PREFIX-$name" -output "$EVIDENCE_DIR/$name-im.json" \
    >"$EVIDENCE_DIR/$name-im.log" 2>&1
}

for service in logic task gateway pilot; do
	started=$(date +%s%N)
  "${COMPOSE[@]}" restart "$service"
	wait_service_healthy "$service"
	wait_ready http://127.0.0.1:18080/ready
	finished=$(date +%s%N)
	record_duration service "$service" "$started" "$finished"
done
for dependency in nats redis etcd postgres; do
	started=$(date +%s%N)
  "${COMPOSE[@]}" restart "$dependency"
	wait_service_healthy "$dependency"
  wait_ready http://127.0.0.1:18080/ready
	for service in logic task gateway pilot; do wait_service_healthy "$service"; done
	run_im_probe "$dependency"
	finished=$(date +%s%N)
	record_duration dependency "$dependency" "$started" "$finished"
done

"${COMPOSE[@]}" pause alloy loki tempo
telemetry_paused=1
wait_ready http://127.0.0.1:18080/ready
run_im_probe telemetry-down
deploy/scripts/run-deterministic-agent-e2e.sh "$PREFIX" "$EVIDENCE_DIR/telemetry-down-agent.json" \
  >"$EVIDENCE_DIR/telemetry-down-agent.log"
"${COMPOSE[@]}" unpause alloy loki tempo
telemetry_paused=0
wait_internal http://loki:3100/ready
wait_internal http://tempo:3200/ready
sleep 6
new_logs=$("${COMPOSE[@]}" exec -T grafana wget -qO- 'http://loki:3100/loki/api/v1/query_range?query=%7Bdeployment_environment%3D%22local-v1%22%7D&limit=20')
printf '%s\n' "$new_logs" >"$EVIDENCE_DIR/telemetry-recovered-logs.json"
test "$(jq '.data.result | length' <<<"$new_logs")" -gt 0
echo "$EVIDENCE_DIR"
