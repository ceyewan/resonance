#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
output_dir=${1:-}
start_ns=${2:-}
im_evidence=${3:-}
agent_evidence=${4:-}
if [[ -z "$output_dir" || ! "$start_ns" =~ ^[0-9]{16,20}$ || ! -f "$im_evidence" || ! -f "$agent_evidence" ]]; then
  echo "usage: $0 <evidence-directory> <e2e-start-unix-nanoseconds> <im-evidence-json> <agent-evidence-json>" >&2
  exit 2
fi
mkdir -p "$output_dir"
output_dir=$(cd "$output_dir" && pwd)
COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)

entries='[]'
for service in gateway logic task pilot; do
  service_entries='[]'
  query="{deployment_environment=\"local-v1\",service_name=\"$service\"} |= \"\\\"trace_id\\\"\""
  encoded_query=$(jq -rn --arg query "$query" '$query|@uri')
  loki_url="http://loki:3100/loki/api/v1/query_range?query=$encoded_query&start=$start_ns&direction=forward&limit=1000"
  for _ in $(seq 1 30); do
    logs=$("${COMPOSE[@]}" exec -T grafana wget -qO- "$loki_url" 2>/dev/null || true)
    if ! jq -e '.status == "success" and (.data.result | type == "array")' <<<"$logs" >/dev/null 2>&1; then
      sleep 2
      continue
    fi
    service_entries=$(jq '[
      .data.result[]? as $stream |
      $stream.values[]? |
      {timestamp:.[0],service:$stream.stream.service_name,line:.[1],json:(.[1] | fromjson?)}
    ]' <<<"$logs")
    if jq -e --arg service "$service" '
      any(.[]; .service == $service and .json.time and .json.level and .json.msg and
        (.json.trace_id | type == "string") and (.json.trace_id | test("^[0-9a-f]{32}$")))
    ' <<<"$service_entries" >/dev/null; then
      break
    fi
    sleep 2
  done
  if ! jq -e --arg service "$service" '
    any(.[]; .service == $service and .json.time and .json.level and .json.msg and
      (.json.trace_id | type == "string") and (.json.trace_id | test("^[0-9a-f]{32}$")))
  ' <<<"$service_entries" >/dev/null; then
    echo "Loki did not return a trace-correlated JSON log for $service within the retry window" >&2
    exit 1
  fi
  entries=$(jq -n --argjson accumulated "$entries" --argjson current "$service_entries" '$accumulated + $current')
done
jq '[
  .[] | select(.json.time and .json.level and .json.msg) |
  {timestamp,service,time:.json.time,level:.json.level,msg:.json.msg,
   trace_id:(.json.trace_id // null),span_id:(.json.span_id // null),
   event_id:(.json.event_id // null),run_id:(.json.run_id // null)}
]' <<<"$entries" >"$output_dir/loki-e2e-logs.json"

jq -e '
  ["gateway","logic","task","pilot"] as $required |
  ([.[] | select(.json.time and .json.level and .json.msg) | .service] | unique) as $seen |
  all($required[]; . as $service | $seen | index($service))
' <<<"$entries" >/dev/null

trace_groups=$(jq '[
  .[] |
  select(.json.trace_id? | type == "string" and test("^[0-9a-f]{32}$")) |
  {trace_id:.json.trace_id,service:.service}
] | group_by(.trace_id) | map({trace_id:.[0].trace_id,services:([.[].service] | unique)})' <<<"$entries")

deploy/scripts/validate-stage3-evidence.sh telemetry-bindings \
  "$output_dir/loki-e2e-logs.json" "$im_evidence" "$agent_evidence"
im_event_id=$(jq -r '.message_event_id' "$im_evidence")
agent_run_id=$(jq -r '.read_tool_run_id' "$agent_evidence")
im_trace_id=$(jq -r --argjson event_id "$im_event_id" '
  [.[] | select(.service == "task" and .event_id == $event_id) | .trace_id] | unique | .[0]
' "$output_dir/loki-e2e-logs.json")
agent_trace_id=$(jq -r --arg run_id "$agent_run_id" '
  [.[] | select(.service == "pilot" and .run_id == $run_id and .msg == "agent run started") | .trace_id] | unique | .[0]
' "$output_dir/loki-e2e-logs.json")

verify_tempo_trace() {
	local name=$1 trace_id=$2 required_json=$3
	local trace_file="$output_dir/tempo-$name-trace.json"
  local trace=''
  for _ in $(seq 1 30); do
    trace=$("${COMPOSE[@]}" exec -T grafana wget -qO- "http://tempo:3200/api/traces/$trace_id" 2>/dev/null || true)
    if jq -e --argjson required "$required_json" '
      ([.. | objects | select(.key? == "service.name") | .value.stringValue? // empty] | unique) as $seen |
      all($required[]; . as $service | $seen | index($service))
    ' <<<"$trace" >/dev/null 2>&1; then
      jq --arg trace_id "$trace_id" '{
        trace_id:$trace_id,
        services:([.. | objects | select(.key? == "service.name") | .value.stringValue? // empty] | unique),
        spans:([.. | objects | select(.spanId? and .name?) | {
          trace_id:(.traceId // $trace_id),span_id:.spanId,parent_span_id:(.parentSpanId // ""),name:.name,
          start_time_unix_nano:(.startTimeUnixNano // ""),end_time_unix_nano:(.endTimeUnixNano // "")
        }] | unique_by(.span_id))
      }' <<<"$trace" >"$trace_file"
      return 0
    fi
    sleep 2
  done
  echo "Tempo trace $trace_id does not contain the required $name service path" >&2
  return 1
}

verify_tempo_trace im "$im_trace_id" '["resonance-gateway","resonance-logic","resonance-task"]'
verify_tempo_trace agent "$agent_trace_id" '["resonance-gateway","resonance-logic","resonance-pilot"]'

"${COMPOSE[@]}" exec -T grafana sh -c \
  'auth=$(printf "%s:%s" "$GF_SECURITY_ADMIN_USER" "$GF_SECURITY_ADMIN_PASSWORD" | base64); wget -qO- --header "Authorization: Basic $auth" http://127.0.0.1:3000/api/datasources/uid/loki' \
  >"$output_dir/grafana-loki-datasource.json"
"${COMPOSE[@]}" exec -T grafana sh -c \
  'auth=$(printf "%s:%s" "$GF_SECURITY_ADMIN_USER" "$GF_SECURITY_ADMIN_PASSWORD" | base64); wget -qO- --header "Authorization: Basic $auth" http://127.0.0.1:3000/api/datasources/uid/tempo' \
  >"$output_dir/grafana-tempo-datasource.json"
deploy/scripts/validate-stage3-evidence.sh grafana-links \
  "$output_dir/grafana-loki-datasource.json" "$output_dir/grafana-tempo-datasource.json"

jq -n \
  --argjson im_event_id "$im_event_id" \
  --arg agent_run_id "$agent_run_id" \
  --arg im_trace_id "$im_trace_id" \
  --arg agent_trace_id "$agent_trace_id" \
  --argjson trace_groups "$trace_groups" \
  --argjson entries "$entries" \
  '{
    schema_version:1,
    four_service_json_logs:true,
    loki_to_tempo:true,
    tempo_to_loki:true,
    im_trace:{event_id:$im_event_id,trace_id:$im_trace_id,loki_services:["gateway","logic","task"],tempo_services:["resonance-gateway","resonance-logic","resonance-task"]},
    agent_trace:{run_id:$agent_run_id,trace_id:$agent_trace_id,loki_services:["gateway","logic","pilot"],tempo_services:["resonance-gateway","resonance-logic","resonance-pilot"]},
    traces_are_distinct:($im_trace_id != $agent_trace_id),
    grafana_runtime_links_validated:true,
    json_log_counts:($entries | map(select(.json.time and .json.level and .json.msg)) | group_by(.service) | map({key:.[0].service,value:length}) | from_entries),
    trace_groups:$trace_groups
  }' >"$output_dir/telemetry-summary.json"
