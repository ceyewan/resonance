#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)

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

for service in logic task gateway pilot; do
  "${COMPOSE[@]}" restart "$service"
  wait_ready http://127.0.0.1:18080/ready
done
for dependency in nats redis etcd postgres; do
  "${COMPOSE[@]}" restart "$dependency"
  wait_ready http://127.0.0.1:18080/ready
done

"${COMPOSE[@]}" pause alloy loki tempo
wait_ready http://127.0.0.1:18080/ready
"${COMPOSE[@]}" unpause alloy loki tempo
wait_internal http://loki:3100/ready
wait_internal http://tempo:3200/ready
