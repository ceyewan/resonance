#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)
EVIDENCE_DIR=${EVIDENCE_DIR:-artifacts/local-v1/$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$EVIDENCE_DIR"
EVIDENCE_DIR=$(cd "$EVIDENCE_DIR" && pwd)
TEST_PREFIX=${RESONANCE_E2E_PREFIX:-lv1-$(date -u +%m%d%H%M%S)-$$}
if [[ ! "$TEST_PREFIX" =~ ^[A-Za-z0-9_-]{1,24}$ ]]; then
  echo "unsafe E2E prefix" >&2
  exit 2
fi
cleanup() {
  deploy/scripts/cleanup-local-test-data.sh "$TEST_PREFIX"
}
trap cleanup EXIT

"${COMPOSE[@]}" config | sed -E '/^[[:space:]]+[A-Z0-9_]*(PASSWORD|SECRET|API_KEY)[A-Z0-9_]*:/ s/:.*/: <redacted>/' >"$EVIDENCE_DIR/compose-config.yaml"
"${COMPOSE[@]}" ps --all --format json >"$EVIDENCE_DIR/compose-ps.jsonl"
compose_ps=$(jq -s '.' "$EVIDENCE_DIR/compose-ps.jsonl")
test "$(jq '[.[] | select(.Service != "init" and .Service != "pilot-storage-init" and (.State != "running" or .Health != "healthy"))] | length' <<<"$compose_ps")" -eq 0
test "$(jq '[.[] | select((.Service == "init" or .Service == "pilot-storage-init") and (.State != "exited" or .ExitCode != 0))] | length' <<<"$compose_ps")" -eq 0
test "$(jq '[.[] | select(.Service == "init" or .Service == "pilot-storage-init")] | length' <<<"$compose_ps")" -eq 2
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

go run ./cmd/local-im-e2e \
  -base-url http://127.0.0.1:18080 \
  -prefix "$TEST_PREFIX-im" \
  -output "$EVIDENCE_DIR/im-e2e.json" 2>&1 | tee "$EVIDENCE_DIR/im-e2e.log"

deploy/scripts/run-deterministic-agent-e2e.sh "$TEST_PREFIX" "$EVIDENCE_DIR/agent-e2e.json" \
  | tee "$EVIDENCE_DIR/agent-e2e.log"

db_user=$(sed -n 's/^RESONANCE_POSTGRES_USER=//p' .env | tail -n 1)
db_name=$(sed -n 's/^RESONANCE_POSTGRES_DATABASE=//p' .env | tail -n 1)
db_user=${db_user:-resonance}
db_name=${db_name:-resonance}

# Failure and duplicate-terminal contracts remain deterministic component tests;
# the successful Tool/Approval/mutation path above is the real Compose path.
go test ./pilot/coordinator ./pilot/toolbroker ./pilot/mutation ./pilot/stream -count=1 \
  | tee "$EVIDENCE_DIR/agent-terminal-contracts.log"

"${COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -v test_prefix="${TEST_PREFIX}%" \
  -U "$db_user" -d "$db_name" -At <<'SQL' | jq -s '.' >"$EVIDENCE_DIR/business-storage.json"
SELECT json_build_object(
  'kind','im',
  'messages',(SELECT count(*) FROM t_message_content WHERE sender_username LIKE :'test_prefix'),
  'published_outbox',(SELECT count(*) FROM t_message_outbox o JOIN t_message_content m ON m.event_id=o.event_id WHERE m.sender_username LIKE :'test_prefix' AND o.status=1),
  'inbox_rows',(SELECT count(*) FROM t_inbox WHERE owner_username LIKE :'test_prefix')
)::text;
SELECT json_build_object(
  'kind','agent',
  'runs',(SELECT count(*) FROM t_agent_run WHERE actor_username LIKE :'test_prefix'),
  'succeeded_runs',(SELECT count(*) FROM t_agent_run WHERE actor_username LIKE :'test_prefix' AND status='SUCCEEDED'),
  'tool_executions',(SELECT count(*) FROM t_agent_tool_execution e JOIN t_agent_run r ON r.run_id=e.run_id WHERE r.actor_username LIKE :'test_prefix'),
  'approvals',(SELECT count(*) FROM t_agent_approval WHERE requester_id LIKE :'test_prefix'),
  'mutation_receipts',(SELECT count(*) FROM t_agent_iam_mutation_receipt WHERE requester_id LIKE :'test_prefix')
)::text;
SQL
jq -e 'map(select(.kind=="im"))[0] | .messages >= 3 and .published_outbox >= 3 and .inbox_rows >= 4' "$EVIDENCE_DIR/business-storage.json" >/dev/null
jq -e 'map(select(.kind=="agent"))[0] | .runs >= 2 and .succeeded_runs >= 2 and .tool_executions >= 1 and .approvals >= 1 and .mutation_receipts >= 1' "$EVIDENCE_DIR/business-storage.json" >/dev/null

curl --silent --output /dev/null http://127.0.0.1:18080/health
sleep 6
traces=$("${COMPOSE[@]}" exec -T grafana wget -qO- 'http://tempo:3200/api/search?limit=20')
printf '%s\n' "$traces" >"$EVIDENCE_DIR/tempo-traces.json"
test "$(jq '.traces | length' <<<"$traces")" -gt 0

git rev-parse HEAD >"$EVIDENCE_DIR/resonance-sha.txt"
go list -m github.com/ceyewan/genesis >"$EVIDENCE_DIR/genesis-version.txt"
docker version >"$EVIDENCE_DIR/docker-version.txt"
echo "$EVIDENCE_DIR"
