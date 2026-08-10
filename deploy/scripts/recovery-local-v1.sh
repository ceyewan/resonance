#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)
export GOWORK=off
export RESONANCE_VERSION=${RESONANCE_VERSION:-$(git rev-parse --short=12 HEAD)}
EVIDENCE_DIR=${EVIDENCE_DIR:-artifacts/local-v1/recovery-$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$EVIDENCE_DIR"
EVIDENCE_DIR=$(cd "$EVIDENCE_DIR" && pwd)
deploy/scripts/verify-genesis-rc2-identity.sh >"$EVIDENCE_DIR/genesis-rc2-identity.json"
PREFIX=${RESONANCE_RECOVERY_PREFIX:-rc-$(date -u +%m%d%H%M%S)-$$}
if [[ ! "$PREFIX" =~ ^[A-Za-z0-9_-]{1,20}$ ]]; then
  echo "unsafe recovery prefix" >&2
  exit 2
fi
telemetry_paused=0
stopped_service=""
cleanup() {
  if [[ -n "$stopped_service" ]]; then
    "${COMPOSE[@]}" start "$stopped_service" >/dev/null 2>&1 || true
  fi
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

wait_runtime_stable() {
  local nats_total registry_keys redis_logic_workers redis_gateway_workers
  for _ in $(seq 1 60); do
    for service in logic task gateway pilot; do
      wait_service_healthy "$service" || return 1
    done
    wait_ready http://127.0.0.1:18080/ready || return 1
    nats_total=$("${COMPOSE[@]}" exec -T grafana wget -qO- 'http://nats:8222/connz?limit=1' | jq '.total')
    registry_keys=$("${COMPOSE[@]}" exec -T etcd etcdctl get /resonance/services --prefix --keys-only | sed '/^$/d' | wc -l | tr -d ' ')
    redis_logic_workers=$("${COMPOSE[@]}" exec -T redis redis-cli --raw --scan --pattern 'resonance:logic:worker:*' | wc -l | tr -d ' ')
    redis_gateway_workers=$("${COMPOSE[@]}" exec -T redis redis-cli --raw --scan --pattern 'resonance:gateway:worker:*' | wc -l | tr -d ' ')
    if [[ "$nats_total" -ge 4 && "$registry_keys" -ge 2 &&
          "$redis_logic_workers" -eq 1 && "$redis_gateway_workers" -eq 1 ]]; then
      # Health checks can turn green before existing subscriptions and database
      # pools have completed their reconnect backoff.
      sleep 5
      return 0
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

db_user=$(sed -n 's/^RESONANCE_POSTGRES_USER=//p' .env | tail -n 1)
db_name=$(sed -n 's/^RESONANCE_POSTGRES_DATABASE=//p' .env | tail -n 1)
db_user=${db_user:-resonance}
db_name=${db_name:-resonance}

capture_continuity() {
  local name=$1
  "${COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -v test_prefix="${PREFIX}%" \
    -U "$db_user" -d "$db_name" -At <<'SQL' | jq . >"$EVIDENCE_DIR/$name-postgres-continuity.json"
SELECT json_build_object(
  'messages',(SELECT count(*) FROM t_message_content WHERE sender_username LIKE :'test_prefix'),
  'outbox',(SELECT count(*) FROM t_message_outbox o JOIN t_message_content m ON m.event_id=o.event_id WHERE m.sender_username LIKE :'test_prefix'),
  'published_outbox',(SELECT count(*) FROM t_message_outbox o JOIN t_message_content m ON m.event_id=o.event_id WHERE m.sender_username LIKE :'test_prefix' AND o.status=1),
  'inbox',(SELECT count(*) FROM t_inbox WHERE owner_username LIKE :'test_prefix')
)::text;
SQL
  "${COMPOSE[@]}" exec -T grafana wget -qO- 'http://nats:8222/jsz?streams=true&consumers=true' \
    >"$EVIDENCE_DIR/$name-nats-jetstream.json"
  redis_db_size=$("${COMPOSE[@]}" exec -T redis redis-cli --raw DBSIZE)
  redis_logic_worker=$("${COMPOSE[@]}" exec -T redis redis-cli --raw --scan --pattern 'resonance:logic:worker:*' | wc -l | tr -d ' ')
  redis_gateway_worker=$("${COMPOSE[@]}" exec -T redis redis-cli --raw --scan --pattern 'resonance:gateway:worker:*' | wc -l | tr -d ' ')
  jq -n --argjson db_size "$redis_db_size" --argjson logic_worker "$redis_logic_worker" --argjson gateway_worker "$redis_gateway_worker" \
    '{db_size:$db_size,logic_allocator_key:$logic_worker,gateway_allocator_key:$gateway_worker}' \
    >"$EVIDENCE_DIR/$name-redis-continuity.json"
  etcd_service_keys=$("${COMPOSE[@]}" exec -T etcd etcdctl get /resonance/services --prefix --keys-only | sed '/^$/d' | wc -l | tr -d ' ')
  jq -n --argjson service_keys "$etcd_service_keys" '{registry_service_keys:$service_keys}' \
    >"$EVIDENCE_DIR/$name-etcd-continuity.json"
}

recovery_started_s=$("${COMPOSE[@]}" exec -T grafana date +%s)
run_im_probe baseline
capture_continuity before

for service in logic task gateway pilot; do
	started=$(date +%s%N)
  container=$("${COMPOSE[@]}" ps -q "$service")
  restart_count_before=$(docker inspect -f '{{.RestartCount}}' "$container")
  registry_keys_before=$("${COMPOSE[@]}" exec -T etcd etcdctl get /resonance/services --prefix --keys-only | sed '/^$/d' | wc -l | tr -d ' ')
  registry_service_name=""
  case "$service" in
    logic) registry_service_name=logic-service ;;
    gateway) registry_service_name=gateway-service ;;
  esac
  registry_release_checked=false
  registry_owned_before=0
  if [[ -n "$registry_service_name" ]]; then
    registry_release_checked=true
    registry_owned_before=$("${COMPOSE[@]}" exec -T etcd etcdctl get "/resonance/services/$registry_service_name" --prefix --keys-only | sed '/^$/d' | wc -l | tr -d ' ')
    if [[ "$registry_owned_before" -lt 1 ]]; then
      echo "$service has no registry lease to validate" >&2
      exit 1
    fi
  fi
  nats_connections_before=$("${COMPOSE[@]}" exec -T grafana wget -qO- 'http://nats:8222/connz?limit=1' | jq '.total')
  "${COMPOSE[@]}" stop -t 65 "$service"
  stopped_service=$service
  exit_code=$(docker inspect -f '{{.State.ExitCode}}' "$container")
  stopped_state=$(docker inspect -f '{{.State.Status}}' "$container")
  [[ "$exit_code" -eq 0 && "$stopped_state" == "exited" ]]
  registry_keys_stopped=$("${COMPOSE[@]}" exec -T etcd etcdctl get /resonance/services --prefix --keys-only | sed '/^$/d' | wc -l | tr -d ' ')
  registry_owned_stopped=$registry_owned_before
  if [[ "$registry_release_checked" == true ]]; then
    for _ in $(seq 1 20); do
      registry_owned_stopped=$("${COMPOSE[@]}" exec -T etcd etcdctl get "/resonance/services/$registry_service_name" --prefix --keys-only | sed '/^$/d' | wc -l | tr -d ' ')
      [[ "$registry_owned_stopped" -lt "$registry_owned_before" ]] && break
      sleep 1
    done
    if [[ "$registry_owned_stopped" -ge "$registry_owned_before" ]]; then
      echo "$service registry lease was not released" >&2
      exit 1
    fi
  fi
  nats_connections_stopped=$("${COMPOSE[@]}" exec -T grafana wget -qO- 'http://nats:8222/connz?limit=1' | jq '.total')
  nats_release_checked=false
  if [[ "$service" != "gateway" ]]; then
    nats_release_checked=true
    if [[ "$nats_connections_stopped" -ge "$nats_connections_before" ]]; then
      echo "$service NATS connection was not released" >&2
      exit 1
    fi
  fi
  if [[ "$service" == "gateway" ]]; then
    ! curl -fsS --max-time 2 http://127.0.0.1:18080/ready >/dev/null 2>&1
  fi
  "${COMPOSE[@]}" start "$service"
	stopped_service=""
	wait_service_healthy "$service"
	wait_ready http://127.0.0.1:18080/ready
	finished=$(date +%s%N)
	restart_count_after=$(docker inspect -f '{{.RestartCount}}' "$container")
	[[ "$restart_count_after" -eq "$restart_count_before" ]]
	jq -nc --arg service "$service" --argjson exit_code "$exit_code" \
	  --argjson restart_count "$restart_count_after" --argjson port_released "$([[ "$service" == "gateway" ]] && echo true || echo false)" \
	  --argjson registry_release_checked "$registry_release_checked" --argjson registry_owned_before "$registry_owned_before" --argjson registry_owned_stopped "$registry_owned_stopped" \
	  --argjson registry_keys_before "$registry_keys_before" --argjson registry_keys_stopped "$registry_keys_stopped" \
	  --argjson nats_release_checked "$nats_release_checked" --argjson nats_connections_before "$nats_connections_before" --argjson nats_connections_stopped "$nats_connections_stopped" \
	  '{service:$service,graceful_exit:($exit_code==0),process_released:true,restart_count:$restart_count,port_release_checked:$port_released,etcd_lease_release_checked:$registry_release_checked,etcd_lease_released:(if $registry_release_checked then $registry_owned_stopped<$registry_owned_before else null end),registry_owned_before:$registry_owned_before,registry_owned_stopped:$registry_owned_stopped,nats_connection_release_checked:$nats_release_checked,nats_connection_released:(if $nats_release_checked then $nats_connections_stopped<$nats_connections_before else null end),registry_keys_before:$registry_keys_before,registry_keys_stopped:$registry_keys_stopped,nats_connections_before:$nats_connections_before,nats_connections_stopped:$nats_connections_stopped}' \
	  >>"$EVIDENCE_DIR/graceful-lifecycle.jsonl"
	record_duration service "$service" "$started" "$finished"
done
sleep 6
recovery_finished_s=$("${COMPOSE[@]}" exec -T grafana date +%s)
"${COMPOSE[@]}" exec -T grafana wget -qO- "http://tempo:3200/api/search?limit=100&start=$recovery_started_s&end=$recovery_finished_s" >"$EVIDENCE_DIR/graceful-trace-flush.json"
jq -e '(.traces | length) > 0' "$EVIDENCE_DIR/graceful-trace-flush.json" >/dev/null
for dependency in nats redis etcd postgres; do
	started=$(date +%s%N)
  gateway_restarts_before=$(docker inspect -f '{{.RestartCount}}' "$("${COMPOSE[@]}" ps -q gateway)")
  logic_restarts_before=$(docker inspect -f '{{.RestartCount}}' "$("${COMPOSE[@]}" ps -q logic)")
  task_restarts_before=$(docker inspect -f '{{.RestartCount}}' "$("${COMPOSE[@]}" ps -q task)")
  pilot_restarts_before=$(docker inspect -f '{{.RestartCount}}' "$("${COMPOSE[@]}" ps -q pilot)")
  "${COMPOSE[@]}" restart "$dependency"
	wait_service_healthy "$dependency"
  wait_ready http://127.0.0.1:18080/ready
	for service in logic task gateway pilot; do wait_service_healthy "$service"; done
	case "$dependency" in
	  nats)
	    for _ in $(seq 1 60); do
	      nats_total=$("${COMPOSE[@]}" exec -T grafana wget -qO- 'http://nats:8222/connz?limit=1' | jq '.total')
	      [[ "$nats_total" -ge 4 ]] && break
	      sleep 1
	    done
	    [[ "$nats_total" -ge 4 ]]
	    ;;
	  redis)
	    for _ in $(seq 1 60); do
	      redis_logic_workers=$("${COMPOSE[@]}" exec -T redis redis-cli --raw --scan --pattern 'resonance:logic:worker:*' | wc -l | tr -d ' ')
	      redis_gateway_workers=$("${COMPOSE[@]}" exec -T redis redis-cli --raw --scan --pattern 'resonance:gateway:worker:*' | wc -l | tr -d ' ')
	      [[ "$redis_logic_workers" -eq 1 && "$redis_gateway_workers" -eq 1 ]] && break
	      sleep 1
	    done
	    [[ "$redis_logic_workers" -eq 1 && "$redis_gateway_workers" -eq 1 ]]
	    wait_service_healthy logic
	    wait_service_healthy gateway
	    wait_ready http://127.0.0.1:18080/ready
	    ;;
	  etcd)
	    for _ in $(seq 1 60); do
	      registry_keys=$("${COMPOSE[@]}" exec -T etcd etcdctl get /resonance/services --prefix --keys-only | sed '/^$/d' | wc -l | tr -d ' ')
	      [[ "$registry_keys" -ge 2 ]] && break
	      sleep 1
	    done
	    [[ "$registry_keys" -ge 2 ]]
	    ;;
	esac
	run_im_probe "$dependency"
	finished=$(date +%s%N)
  gateway_restarts_after=$(docker inspect -f '{{.RestartCount}}' "$("${COMPOSE[@]}" ps -q gateway)")
  logic_restarts_after=$(docker inspect -f '{{.RestartCount}}' "$("${COMPOSE[@]}" ps -q logic)")
  task_restarts_after=$(docker inspect -f '{{.RestartCount}}' "$("${COMPOSE[@]}" ps -q task)")
  pilot_restarts_after=$(docker inspect -f '{{.RestartCount}}' "$("${COMPOSE[@]}" ps -q pilot)")
  recovery_mode=connection_recovered
  if [[ "$dependency" == "redis" ]]; then
    recovery_mode=allocator_lease_preserved
    if [[ "$gateway_restarts_after" -gt "$gateway_restarts_before" || "$logic_restarts_after" -gt "$logic_restarts_before" ]]; then
      recovery_mode=allocator_reacquired_after_supervised_restart
    fi
  fi
  jq -nc --arg dependency "$dependency" --arg recovery_mode "$recovery_mode" \
    --argjson gateway_before "$gateway_restarts_before" --argjson gateway_after "$gateway_restarts_after" \
    --argjson logic_before "$logic_restarts_before" --argjson logic_after "$logic_restarts_after" \
    --argjson task_before "$task_restarts_before" --argjson task_after "$task_restarts_after" \
    --argjson pilot_before "$pilot_restarts_before" --argjson pilot_after "$pilot_restarts_after" \
    '{dependency:$dependency,recovery_mode:$recovery_mode,gateway:{before:$gateway_before,after:$gateway_after},logic:{before:$logic_before,after:$logic_after},task:{before:$task_before,after:$task_after},pilot:{before:$pilot_before,after:$pilot_after}}' \
    >>"$EVIDENCE_DIR/dependency-restarts.jsonl"
  record_duration dependency "$dependency" "$started" "$finished"
done

wait_runtime_stable
run_im_probe final
capture_continuity after
jq -e '.messages > 0 and .outbox > 0 and .published_outbox > 0 and .inbox > 0' "$EVIDENCE_DIR/before-postgres-continuity.json" >/dev/null
jq -e --slurpfile before "$EVIDENCE_DIR/before-postgres-continuity.json" '
  .messages > $before[0].messages and .outbox > $before[0].outbox and
  .published_outbox > $before[0].published_outbox and .inbox > $before[0].inbox
' "$EVIDENCE_DIR/after-postgres-continuity.json" >/dev/null
jq -e '.db_size > 0 and .logic_allocator_key == 1 and .gateway_allocator_key == 1' "$EVIDENCE_DIR/after-redis-continuity.json" >/dev/null
jq -e '.registry_service_keys >= 2' "$EVIDENCE_DIR/after-etcd-continuity.json" >/dev/null
jq -e --slurpfile before "$EVIDENCE_DIR/before-nats-jetstream.json" '
  .streams > 0 and .consumers > 0 and .messages >= $before[0].messages and
  ([.. | objects | .delivered?.stream_seq? // empty] | max // 0) >=
    ([$before[0] | .. | objects | .delivered?.stream_seq? // empty] | max // 0)
' "$EVIDENCE_DIR/after-nats-jetstream.json" >/dev/null

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
telemetry_recovered_s=$("${COMPOSE[@]}" exec -T grafana date +%s)
telemetry_recovered_ns="${telemetry_recovered_s}000000000"
run_im_probe telemetry-recovered
sleep 6
new_logs=$("${COMPOSE[@]}" exec -T grafana wget -qO- "http://loki:3100/loki/api/v1/query_range?query=%7Bdeployment_environment%3D%22local-v1%22%7D&start=$telemetry_recovered_ns&direction=forward&limit=1000")
jq '[.data.result[]? as $stream | $stream.values[]? |
  {timestamp:.[0],service:$stream.stream.service_name,json:(.[1] | fromjson?)} |
  select(.json.time and .json.level and .json.msg) |
  {timestamp,service,time:.json.time,level:.json.level,msg:.json.msg,trace_id:(.json.trace_id // null),span_id:(.json.span_id // null)}
]' <<<"$new_logs" >"$EVIDENCE_DIR/telemetry-recovered-logs.json"
jq -e 'length > 0' "$EVIDENCE_DIR/telemetry-recovered-logs.json" >/dev/null
recovered_trace_ids=$(jq -r '.[].trace_id // empty' "$EVIDENCE_DIR/telemetry-recovered-logs.json" |
  rg '^[0-9a-f]{32}$' | sort -u)
[[ -n "$recovered_trace_ids" ]]
recovered_trace_id=""
for _ in $(seq 1 60); do
  for trace_id in $recovered_trace_ids; do
    if "${COMPOSE[@]}" exec -T grafana wget -qO /tmp/resonance-recovered-trace.json \
      "http://tempo:3200/api/traces/$trace_id" >/dev/null 2>&1; then
      recovered_trace_id=$trace_id
      break 2
    fi
  done
  sleep 2
done
[[ -n "$recovered_trace_id" ]]
"${COMPOSE[@]}" exec -T grafana cat /tmp/resonance-recovered-trace.json \
  >"$EVIDENCE_DIR/telemetry-recovered-trace.json"
jq -n --arg trace_id "$recovered_trace_id" \
  --argjson resource_spans "$(jq '[.batches[]?.scopeSpans[]?.spans[]?] | length' "$EVIDENCE_DIR/telemetry-recovered-trace.json")" \
  '{trace_id:$trace_id,loki_to_tempo_correlated:true,span_count:$resource_spans}' \
  >"$EVIDENCE_DIR/telemetry-recovered-traces.json"
jq -e '.loki_to_tempo_correlated and .span_count > 0' "$EVIDENCE_DIR/telemetry-recovered-traces.json" >/dev/null
deploy/scripts/check-evidence-secrets.sh "$EVIDENCE_DIR" >"$EVIDENCE_DIR/sensitive-field-scan.log"
echo "$EVIDENCE_DIR"
