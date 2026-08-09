#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)
EVIDENCE_DIR=${EVIDENCE_DIR:-artifacts/local-v1/$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$EVIDENCE_DIR"

"${COMPOSE[@]}" config | sed -E '/^[[:space:]]+[A-Z0-9_]*(PASSWORD|SECRET|API_KEY)[A-Z0-9_]*:/ s/:.*/: <redacted>/' >"$EVIDENCE_DIR/compose-config.yaml"
"${COMPOSE[@]}" ps --format json >"$EVIDENCE_DIR/compose-ps.jsonl"
for url in http://127.0.0.1:14173/ http://127.0.0.1:18080/ready http://127.0.0.1:13000/api/health; do
  curl --fail --silent --show-error --max-time 10 "$url" >/dev/null
done

targets=""
for _ in $(seq 1 30); do
  targets=$("${COMPOSE[@]}" exec -T grafana wget -qO- 'http://prometheus:9090/api/v1/targets?state=active')
  if test "$(jq '[.data.activeTargets[] | select(.labels.job == "resonance" and .health != "up")] | length' <<<"$targets")" -eq 0; then
    break
  fi
  sleep 2
done
printf '%s\n' "$targets" >"$EVIDENCE_DIR/prometheus-targets.json"
test "$(jq '[.data.activeTargets[] | select(.labels.job == "resonance" and .health != "up")] | length' <<<"$targets")" -eq 0

logs=$("${COMPOSE[@]}" exec -T grafana wget -qO- 'http://loki:3100/loki/api/v1/query_range?query=%7Bdeployment_environment%3D%22local-v1%22%7D&limit=100')
printf '%s\n' "$logs" >"$EVIDENCE_DIR/loki-logs.json"
test "$(jq '.data.result | length' <<<"$logs")" -gt 0

go test ./test/integration -count=1 | tee "$EVIDENCE_DIR/im-e2e.log"
go test ./pilot/coordinator ./pilot/toolbroker ./pilot/mutation ./pilot/stream -count=1 | tee "$EVIDENCE_DIR/agent-e2e.log"

curl --silent --output /dev/null http://127.0.0.1:18080/health
sleep 6
traces=$("${COMPOSE[@]}" exec -T grafana wget -qO- 'http://tempo:3200/api/search?limit=20')
printf '%s\n' "$traces" >"$EVIDENCE_DIR/tempo-traces.json"
test "$(jq '.traces | length' <<<"$traces")" -gt 0

git rev-parse HEAD >"$EVIDENCE_DIR/resonance-sha.txt"
go list -m github.com/ceyewan/genesis >"$EVIDENCE_DIR/genesis-version.txt"
docker version >"$EVIDENCE_DIR/docker-version.txt"
echo "$EVIDENCE_DIR"
