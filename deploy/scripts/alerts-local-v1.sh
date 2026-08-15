#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)
export GOWORK=off
export RESONANCE_VERSION=${RESONANCE_VERSION:-$(git rev-parse --short=12 HEAD)}
OUT=${ALERT_EVIDENCE_DIR:-artifacts/local-v1/alerts-$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$OUT"
deploy/scripts/verify-genesis-rc2-identity.sh >"$OUT/genesis-rc2-identity.json"
PREFIX=${RESONANCE_ALERT_PREFIX:-al-$(date -u +%m%d%H%M%S)-$$}
if [[ ! "$PREFIX" =~ ^[A-Za-z0-9_-]{1,24}$ ]]; then
  echo "unsafe alert test prefix" >&2
  exit 2
fi
cleanup() {
  if [[ -n "${error_load_pid:-}" ]]; then
    kill "$error_load_pid" >/dev/null 2>&1 || true
    wait "$error_load_pid" >/dev/null 2>&1 || true
  fi
  if [[ -n "${backlog_report:-}" ]]; then
    rm -f "$backlog_report"
  fi
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
backlog_report=$(mktemp "${TMPDIR:-/tmp}/resonance-backlog-report.XXXXXX")
go run ./cmd/local-benchmark -base-url http://127.0.0.1:18080 -count 1 \
  -prefix "$PREFIX-backlog" -output "$backlog_report" >/dev/null 2>&1 || true
wait_state OutboxBacklog firing
alerts >"$OUT/outbox-backlog-firing.json"
jq '{
  schema_version: 1,
  injection: "NATS paused while local benchmark submitted a message",
  alert: ([.data.alerts[] | select(.labels.alertname == "OutboxBacklog")][0] | {
    state,
    observed_backlog: (.value | tonumber),
    summary: .annotations.summary
  })
}' "$OUT/outbox-backlog-firing.json" >"$OUT/backlog-load.json"
"${COMPOSE[@]}" unpause nats
wait_cleared OutboxBacklog
alerts >"$OUT/outbox-backlog-recovered.json"

gateway_container=$("${COMPOSE[@]}" ps -q gateway)
if [[ -z "$gateway_container" ]]; then
  echo "gateway container is not running" >&2
  exit 1
fi
curl -fsS http://127.0.0.1:18080/ready >/dev/null

# Keep Gateway online and sustain real 404 responses across several Prometheus
# scrapes. The global HTTP middleware records these as outcome="error".
(
  deadline=$((SECONDS + 45))
  while (( SECONDS < deadline )); do
    for _ in $(seq 1 10); do
      curl -sS -o /dev/null "http://127.0.0.1:18080/__alert_probe_${PREFIX}" || true
    done
    sleep 0.5
  done
) &
error_load_pid=$!
wait_state APIHighErrorRate firing
alerts >"$OUT/api-high-error-rate-firing.json"
docker inspect --format \
  '{"ContainerID":{{json .Id}},"Status":{{json .State.Status}},"Running":{{json .State.Running}},"Health":{{json .State.Health.Status}},"RestartCount":{{json .RestartCount}},"StartedAt":{{json .State.StartedAt}}}' \
  "$gateway_container" | jq . >"$OUT/api-high-error-rate-gateway-status.json"
curl -fsS http://127.0.0.1:18080/ready | jq . >"$OUT/api-high-error-rate-gateway-ready.json"
if [[ "$("${COMPOSE[@]}" ps -q gateway)" != "$gateway_container" ]]; then
  echo "gateway container changed during HTTP error injection" >&2
  exit 1
fi
kill "$error_load_pid" >/dev/null 2>&1 || true
wait "$error_load_pid" >/dev/null 2>&1 || true
error_load_pid=
wait_cleared APIHighErrorRate
alerts >"$OUT/api-high-error-rate-recovered.json"

deploy/scripts/check-evidence-secrets.sh "$OUT"

echo "$OUT"
