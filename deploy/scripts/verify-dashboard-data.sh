#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
output=${1:-}
business_storage=${2:-}
if [[ -z "$output" || ! -f "$business_storage" ]]; then
  echo "usage: $0 <output-json> <business-storage-json>" >&2
  exit 2
fi
COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)
query='{__name__=~"logic_outbox_backlog|mq_publish_total|mq_consume_total|task_storage_process_duration_seconds_count|pilot_run_duration_seconds_count|connector_health_checks_total|connector_reconnects_total|registry_registrations_total|registry_watch_events_total|registry_lease_failures_total|container_cpu_usage_seconds_total|container_memory_working_set_bytes"}'
encoded_query=$(jq -rn --arg query "$query" '$query|@uri')
metrics=$("${COMPOSE[@]}" exec -T grafana wget -qO- "http://prometheus:9090/api/v1/query?query=$encoded_query")
jq -e '.status == "success"' <<<"$metrics" >/dev/null

deploy/scripts/validate-stage3-evidence.sh dashboard-metrics <(printf '%s\n' "$metrics")
jq -e '
  map(select(.kind=="im"))[0] as $im |
  map(select(.kind=="agent"))[0] as $agent |
  $im.published_outbox > 0 and $im.inbox_rows > 0 and
  $agent.approvals > 0 and $agent.mutation_receipts > 0
' "$business_storage" >/dev/null

jq -n \
  --argjson prometheus "$metrics" \
  --argjson storage "$(cat "$business_storage")" \
  --argjson dashboards "$(jq -s '[.[] | {uid,title,panels:[.panels[] | {title,targets:[.targets[] | {refId,expr:(.expr // "")}]}]}]' deploy/observability/grafana/dashboards/*.json)" \
  '{schema_version:1,prometheus:$prometheus,business_storage:$storage,provisioned_dashboards:$dashboards}' >"$output"
