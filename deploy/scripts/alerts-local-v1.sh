#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)
OUT=${ALERT_EVIDENCE_DIR:-artifacts/local-v1/alerts-$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$OUT"
PREFIX=${RESONANCE_ALERT_PREFIX:-al-$(date -u +%m%d%H%M%S)-$$}
if [[ ! "$PREFIX" =~ ^[A-Za-z0-9_-]{1,24}$ ]]; then
  echo "unsafe alert test prefix" >&2
  exit 2
fi
cleanup() {
  "${COMPOSE[@]}" unpause nats >/dev/null 2>&1 || true
  "${COMPOSE[@]}" start task alloy gateway >/dev/null 2>&1 || true
  deploy/scripts/cleanup-local-test-data.sh "$PREFIX"
}
trap cleanup EXIT

alerts() { "${COMPOSE[@]}" exec -T grafana wget -qO- http://prometheus:9090/api/v1/alerts; }
wait_state() {
  local name=$1 state=$2
  for _ in $(seq 1 60); do
    if alerts | jq -e --arg name "$name" --arg state "$state" '.data.alerts[] | select(.labels.alertname == $name and .state == $state)' >/dev/null; then return 0; fi
    sleep 2
  done
  return 1
}

wait_cleared() {
  local name=$1
  for _ in $(seq 1 60); do
    if ! alerts | jq -e --arg name "$name" '.data.alerts[] | select(.labels.alertname == $name and .state == "firing")' >/dev/null; then return 0; fi
    sleep 2
  done
  return 1
}

"${COMPOSE[@]}" stop task
wait_state ServiceDown firing
alerts >"$OUT/service-down-firing.json"
"${COMPOSE[@]}" start task
wait_cleared ServiceDown
alerts >"$OUT/service-down-recovered.json"

"${COMPOSE[@]}" stop alloy
wait_state TelemetryPipelineDown firing
alerts >"$OUT/telemetry-pipeline-down-firing.json"
curl -fsS http://127.0.0.1:18080/ready >/dev/null
"${COMPOSE[@]}" start alloy
wait_cleared TelemetryPipelineDown
alerts >"$OUT/telemetry-pipeline-down-recovered.json"

"${COMPOSE[@]}" pause nats
go run ./cmd/local-benchmark -base-url http://127.0.0.1:18080 -count 1 \
  -prefix "$PREFIX-backlog" -output "$OUT/backlog-load.json" >/dev/null 2>&1 || true
wait_state OutboxBacklog firing
alerts >"$OUT/outbox-backlog-firing.json"
"${COMPOSE[@]}" unpause nats
wait_cleared OutboxBacklog
alerts >"$OUT/outbox-backlog-recovered.json"

"${COMPOSE[@]}" stop gateway
wait_state APIHighErrorRate firing
alerts >"$OUT/api-high-error-rate-firing.json"
"${COMPOSE[@]}" start gateway
wait_cleared APIHighErrorRate
alerts >"$OUT/api-high-error-rate-recovered.json"

echo "$OUT"
