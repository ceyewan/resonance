#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)
export GOWORK=off
export RESONANCE_VERSION=${RESONANCE_VERSION:-$(git rev-parse --short=12 HEAD)}
EVIDENCE_DIR=${EVIDENCE_DIR:-artifacts/local-v1/$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$EVIDENCE_DIR"
EVIDENCE_DIR=$(cd "$EVIDENCE_DIR" && pwd)
deploy/scripts/verify-genesis-rc2-identity.sh >"$EVIDENCE_DIR/genesis-rc2-identity.json"
TEST_PREFIX=${RESONANCE_E2E_PREFIX:-lv1-$(date -u +%m%d%H%M%S)-$$}
if [[ ! "$TEST_PREFIX" =~ ^[A-Za-z0-9_-]{1,24}$ ]]; then
  echo "unsafe E2E prefix" >&2
  exit 2
fi
cleanup_needed=1
cleanup() {
  if [[ "$cleanup_needed" -eq 1 ]]; then
    cleanup_needed=0
    deploy/scripts/cleanup-local-test-data.sh "$TEST_PREFIX"
  fi
}
trap cleanup EXIT

db_user=$(sed -n 's/^RESONANCE_POSTGRES_USER=//p' .env | tail -n 1)
db_name=$(sed -n 's/^RESONANCE_POSTGRES_DATABASE=//p' .env | tail -n 1)
db_user=${db_user:-resonance}
db_name=${db_name:-resonance}
snapshot_budget() {
  "${COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -U "$db_user" -d "$db_name" -At <<'SQL' | jq -S .
SELECT coalesce(json_agg(json_build_object(
  'tenant_id',tenant_id,
  'period_kind',period_kind,
  'period_start',period_start,
  'reserved_tokens',reserved_tokens,
  'settled_tokens',settled_tokens,
  'unknown_reserved_tokens',unknown_reserved_tokens,
  'reserved_cost_micros',reserved_cost_micros,
  'settled_cost_micros',settled_cost_micros,
  'unknown_reserved_cost_micros',unknown_reserved_cost_micros
) ORDER BY tenant_id,period_kind,period_start), '[]'::json)
FROM t_agent_budget_bucket;
SQL
}

"${COMPOSE[@]}" config --format json | jq 'walk(if type == "object" then del(.environment) else . end)' >"$EVIDENCE_DIR/compose-config.json"
"${COMPOSE[@]}" ps --all --format json >"$EVIDENCE_DIR/compose-ps.jsonl"
compose_ps=$(jq -s '.' "$EVIDENCE_DIR/compose-ps.jsonl")
test "$(jq '[.[] | select(.Service != "init" and .Service != "pilot-storage-init" and (.State != "running" or .Health != "healthy"))] | length' <<<"$compose_ps")" -eq 0
test "$(jq '[.[] | select((.Service == "init" or .Service == "pilot-storage-init") and (.State != "exited" or .ExitCode != 0))] | length' <<<"$compose_ps")" -eq 0
test "$(jq '[.[] | select(.Service == "init" or .Service == "pilot-storage-init")] | length' <<<"$compose_ps")" -eq 2
for url in http://127.0.0.1:14173/ http://127.0.0.1:18080/ready http://127.0.0.1:13000/api/health; do
  curl --fail --silent --show-error --max-time 10 "$url" >/dev/null
done

deploy/scripts/test-cleanup-local-test-data.sh \
  | tee "$EVIDENCE_DIR/cleanup-literal-prefix-safety.log"
deploy/scripts/test-evidence-secret-scan.sh \
  | tee "$EVIDENCE_DIR/evidence-sensitive-field-scan.log"
snapshot_budget >"$EVIDENCE_DIR/agent-budget-before.json"

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

# Loki, Tempo, and the service containers share the Compose VM clock. Use that
# clock for the evidence window so host sleep/wake drift cannot make start a
# future timestamp from the backends' point of view.
e2e_started_seconds=$("${COMPOSE[@]}" exec -T grafana date +%s)
[[ "$e2e_started_seconds" =~ ^[0-9]{10}$ ]]
e2e_started_ns="${e2e_started_seconds}000000000"

go run ./cmd/local-im-e2e \
  -base-url http://127.0.0.1:18080 \
  -prefix "$TEST_PREFIX-im" \
  -output "$EVIDENCE_DIR/im-e2e.json" 2>&1 | tee "$EVIDENCE_DIR/im-e2e.log"

deploy/scripts/run-deterministic-agent-e2e.sh "$TEST_PREFIX" "$EVIDENCE_DIR/agent-e2e.json" \
  | tee "$EVIDENCE_DIR/agent-e2e.log"

runtime_failure_run=$(jq -r '.runtime_failure_run_id' "$EVIDENCE_DIR/agent-e2e.json")
timeout_run=$(jq -r '.timeout_run_id' "$EVIDENCE_DIR/agent-e2e.json")
[[ -n "$runtime_failure_run" && "$runtime_failure_run" != null && -n "$timeout_run" && "$timeout_run" != null ]]
failed_run_count=0
for _ in $(seq 1 180); do
  failed_run_count=$("${COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 \
    -v runtime_failure_run="$runtime_failure_run" -v timeout_run="$timeout_run" \
    -U "$db_user" -d "$db_name" -At <<'SQL'
SELECT count(*) FROM t_agent_run
WHERE run_id IN (:'runtime_failure_run', :'timeout_run')
  AND status='FAILED_FINAL' AND attempt=max_attempts
  AND last_error_code IN ('runtime_start_failed','runtime_failed');
SQL
  )
  [[ "$failed_run_count" -eq 2 ]] && break
  sleep 1
done
[[ "$failed_run_count" -eq 2 ]]

rejected_execution_count=0
for _ in $(seq 1 50); do
  rejected_execution_count=$("${COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -v test_prefix="${TEST_PREFIX}%" \
    -U "$db_user" -d "$db_name" -At <<'SQL'
SELECT count(*) FROM t_agent_tool_execution e
JOIN t_agent_approval a ON a.call_id=e.call_id
WHERE a.requester_id LIKE :'test_prefix'
  AND e.status='FAILED_FINAL' AND e.error_code='APPROVAL_NOT_EXECUTABLE';
SQL
  )
  [[ "$rejected_execution_count" -ge 1 ]] && break
  sleep 0.2
done
[[ "$rejected_execution_count" -ge 1 ]]

# Failure and duplicate-terminal contracts remain deterministic component tests;
# the successful Tool/Approval/mutation path above is the real Compose path.
go test ./pilot/coordinator ./pilot/toolbroker ./pilot/mutation ./pilot/stream -count=1 \
  | tee "$EVIDENCE_DIR/agent-terminal-contracts.log"

"${COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -v test_prefix="${TEST_PREFIX}%" \
  -v runtime_failure_run="$runtime_failure_run" -v timeout_run="$timeout_run" \
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
	'failed_runs',(SELECT count(*) FROM t_agent_run WHERE actor_username LIKE :'test_prefix' AND status IN ('FAILED_FINAL','CANCELLED')),
  'tool_executions',(SELECT count(*) FROM t_agent_tool_execution e JOIN t_agent_run r ON r.run_id=e.run_id WHERE r.actor_username LIKE :'test_prefix'),
  'approvals',(SELECT count(*) FROM t_agent_approval WHERE requester_id LIKE :'test_prefix'),
	'rejected_approvals',(SELECT count(*) FROM t_agent_approval WHERE requester_id LIKE :'test_prefix' AND status='REJECTED'),
	'rejected_tool_executions',(SELECT count(*) FROM t_agent_tool_execution e JOIN t_agent_approval a ON a.call_id=e.call_id WHERE a.requester_id LIKE :'test_prefix' AND e.status='FAILED_FINAL' AND e.error_code='APPROVAL_NOT_EXECUTABLE'),
	'runtime_failure',(SELECT json_build_object('status',status,'attempt',attempt,'max_attempts',max_attempts,'error_code',last_error_code) FROM t_agent_run WHERE run_id=:'runtime_failure_run'),
	'timeout',(SELECT json_build_object('status',status,'attempt',attempt,'max_attempts',max_attempts,'error_code',last_error_code) FROM t_agent_run WHERE run_id=:'timeout_run'),
  'mutation_receipts',(SELECT count(*) FROM t_agent_iam_mutation_receipt WHERE requester_id LIKE :'test_prefix')
)::text;
SQL
jq -e 'map(select(.kind=="im"))[0] | .messages >= 3 and .published_outbox >= 3 and .inbox_rows >= 4' "$EVIDENCE_DIR/business-storage.json" >/dev/null
jq -e 'map(select(.kind=="agent"))[0] | .runs >= 4 and .succeeded_runs >= 2 and .failed_runs >= 2 and .tool_executions >= 2 and .approvals >= 2 and .rejected_approvals >= 1 and .rejected_tool_executions >= 1 and .mutation_receipts >= 1 and .runtime_failure.status == "FAILED_FINAL" and .runtime_failure.attempt == .runtime_failure.max_attempts and .timeout.status == "FAILED_FINAL" and .timeout.attempt == .timeout.max_attempts' "$EVIDENCE_DIR/business-storage.json" >/dev/null
jq -e '
  .runtime_failure_run_id != "" and .timeout_run_id != "" and
  .rejected_approval_call_id != ""
' "$EVIDENCE_DIR/agent-e2e.json" >/dev/null
deploy/scripts/verify-dashboard-data.sh "$EVIDENCE_DIR/dashboard-data-proof.json" "$EVIDENCE_DIR/business-storage.json"

curl --silent --output /dev/null http://127.0.0.1:18080/health
deploy/scripts/verify-e2e-telemetry.sh "$EVIDENCE_DIR" "$e2e_started_ns"

git rev-parse HEAD >"$EVIDENCE_DIR/resonance-sha.txt"
docker version >"$EVIDENCE_DIR/docker-version.txt"
cleanup
snapshot_budget >"$EVIDENCE_DIR/agent-budget-after.json"
cmp "$EVIDENCE_DIR/agent-budget-before.json" "$EVIDENCE_DIR/agent-budget-after.json"
deploy/scripts/check-evidence-secrets.sh "$EVIDENCE_DIR" >"$EVIDENCE_DIR/sensitive-field-scan.log"
echo "$EVIDENCE_DIR"
