#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
validator=deploy/scripts/validate-stage3-evidence.sh
identity_gate=deploy/scripts/verify-genesis-rc2-identity.sh
fixtures=$(mktemp -d "${TMPDIR:-/tmp}/resonance-stage3-gates.XXXXXX")
trap 'rm -rf "$fixtures"' EXIT

must_reject() {
  if "$validator" "$@" >/dev/null 2>&1; then
    echo "gate unexpectedly accepted invalid evidence: $*" >&2
    exit 1
  fi
}

mkdir -p "$fixtures/source/vendor"
if "$identity_gate" --check-source-layout "$fixtures/source" >/dev/null 2>&1; then
  echo "identity gate accepted a vendored source tree" >&2
  exit 1
fi
rmdir "$fixtures/source/vendor"
"$identity_gate" --check-source-layout "$fixtures/source"

jq -n '{status:"success",data:{result:[]}}' >"$fixtures/metrics-empty.json"
must_reject dashboard-metrics "$fixtures/metrics-empty.json"
jq -n '{status:"success",data:{result:[
  {metric:{__name__:"logic_outbox_backlog"}},
  {metric:{__name__:"mq_publish_total"}},
  {metric:{__name__:"task_storage_process_duration_seconds_count"}},
  {metric:{__name__:"pilot_run_duration_seconds_count"}}
]}}' >"$fixtures/metrics-valid.json"
"$validator" dashboard-metrics "$fixtures/metrics-valid.json"

jq -n '{message_event_id:42}' >"$fixtures/im.json"
jq -n '{read_tool_run_id:"run-1"}' >"$fixtures/agent.json"
jq -n '[
  {service:"gateway",trace_id:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  {service:"logic",trace_id:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  {service:"task",trace_id:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",event_id:42},
  {service:"pilot",trace_id:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",run_id:"run-1",msg:"agent run started"}
]' >"$fixtures/logs-same-trace.json"
must_reject telemetry-bindings "$fixtures/logs-same-trace.json" "$fixtures/im.json" "$fixtures/agent.json"
jq -n '[
  {service:"gateway",trace_id:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  {service:"logic",trace_id:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  {service:"task",trace_id:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",event_id:42},
  {service:"gateway",trace_id:"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
  {service:"logic",trace_id:"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
  {service:"pilot",trace_id:"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",run_id:"run-1",msg:"agent run started"}
]' >"$fixtures/logs-valid.json"
"$validator" telemetry-bindings "$fixtures/logs-valid.json" "$fixtures/im.json" "$fixtures/agent.json"

jq -n '{uid:"loki",type:"loki",jsonData:{derivedFields:[]}}' >"$fixtures/loki-invalid.json"
jq -n '{uid:"tempo",type:"tempo",jsonData:{tracesToLogsV2:{datasourceUid:"loki",filterByTraceID:true,tags:[{key:"service.name",value:"otel_service_name"}]}}}' >"$fixtures/tempo-valid.json"
must_reject grafana-links "$fixtures/loki-invalid.json" "$fixtures/tempo-valid.json"
jq -n '{uid:"loki",type:"loki",jsonData:{derivedFields:[{name:"TraceID",datasourceUid:"tempo",url:"${__value.raw}",matcherRegex:"trace_id"}]}}' >"$fixtures/loki-valid.json"
"$validator" grafana-links "$fixtures/loki-valid.json" "$fixtures/tempo-valid.json"

jq -n '{batches:[{resource:{attributes:[{key:"service.name",value:{stringValue:"resonance-task"}}]},scopeSpans:[{spans:[{traceId:"qqqqqqqqqqqqqqqqqqqqqg==",name:"unrelated"}]}]}]}' >"$fixtures/shutdown-wrong-span.json"
must_reject shutdown-flush "$fixtures/shutdown-wrong-span.json" resonance-task aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
jq '(.batches[0].scopeSpans[0].spans[0].name)="stage3.shutdown.flush"' "$fixtures/shutdown-wrong-span.json" >"$fixtures/shutdown-valid.json"
"$validator" shutdown-flush "$fixtures/shutdown-valid.json" resonance-task aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa

jq -n '{checks:[
  "check-gen","docs-and-format","go-lint","go-security","go-test","pilot-bridge","pilot-image","proto-lint","web"
] | map({name:.,head_sha:"abc",status:"completed",conclusion:"success"})}' >"$fixtures/ci-valid.json"
"$validator" hosted-ci "$fixtures/ci-valid.json" abc
jq '(.checks[] | select(.name=="web")).name="unrelated-green"' "$fixtures/ci-valid.json" >"$fixtures/ci-wrong-name.json"
must_reject hosted-ci "$fixtures/ci-wrong-name.json" abc

jq -n '{schema_version:1,message_event_id:42,message_seq_id:1,duplicate_event_id:42,offline_message_event_id:43,offline_message_seq_id:2,recall_event_id:44,inbox_recovery_verified:true,multi_device_read_seen:true,idempotency_verified:true}' >"$fixtures/nats-im-valid.json"
jq -n '{account_details:[{stream_detail:[{name:"S",consumer_detail:[{name:"durable",created:"t1",config:{durable_name:"durable",filter_subject:"events",ack_policy:"explicit"},delivered:{consumer_seq:10,stream_seq:10},ack_floor:{consumer_seq:10,stream_seq:10}}]}]}]}' >"$fixtures/nats-before.json"
jq -n '{account_details:[{stream_detail:[{name:"S",consumer_detail:[{name:"replacement",created:"t2",config:{durable_name:"replacement",filter_subject:"events",ack_policy:"explicit"},delivered:{consumer_seq:11,stream_seq:11},ack_floor:{consumer_seq:11,stream_seq:11}}]}]}]}' >"$fixtures/nats-replaced.json"
must_reject nats-continuity "$fixtures/nats-before.json" "$fixtures/nats-replaced.json" "$fixtures/nats-im-valid.json"
jq -n '{account_details:[{stream_detail:[{name:"S",consumer_detail:[{name:"durable",created:"t2",config:{durable_name:"durable",filter_subject:"events",ack_policy:"explicit"},delivered:{consumer_seq:11,stream_seq:11},ack_floor:{consumer_seq:11,stream_seq:11}}]}]}]}' >"$fixtures/nats-created-drift.json"
"$validator" nats-continuity "$fixtures/nats-before.json" "$fixtures/nats-created-drift.json" "$fixtures/nats-im-valid.json"
"$validator" nats-created-audit "$fixtures/nats-before.json" "$fixtures/nats-created-drift.json" "https://github.com/ceyewan/genesis/issues/67" >"$fixtures/nats-created-audit.json"
jq -e '.created_is_durable_identity == false and .changed_count == 1 and .changed_consumers[0].key == "S/durable"' "$fixtures/nats-created-audit.json" >/dev/null
jq '(.account_details[0].stream_detail[0].consumer_detail[0].config.max_deliver)=9' "$fixtures/nats-created-drift.json" >"$fixtures/nats-config-drift.json"
must_reject nats-continuity "$fixtures/nats-before.json" "$fixtures/nats-config-drift.json" "$fixtures/nats-im-valid.json"
jq '(.account_details[0].stream_detail[0].consumer_detail[0].ack_floor.stream_seq)=9' "$fixtures/nats-created-drift.json" >"$fixtures/nats-position-regressed.json"
must_reject nats-continuity "$fixtures/nats-before.json" "$fixtures/nats-position-regressed.json" "$fixtures/nats-im-valid.json"
jq '.duplicate_event_id=99' "$fixtures/nats-im-valid.json" >"$fixtures/nats-im-duplicate.json"
must_reject nats-continuity "$fixtures/nats-before.json" "$fixtures/nats-created-drift.json" "$fixtures/nats-im-duplicate.json"

jq -n '{sequencer:{key:"seq",value:"10"},allocators:[{key:"a",value:"1",pttl_ms:100},{key:"b",value:"2",pttl_ms:100}]}' >"$fixtures/redis-before.json"
jq -n '{sequencer:{key:"seq",value:"9"},allocators:[{key:"a",value:"1",pttl_ms:100},{key:"c",value:"2",pttl_ms:100}]}' >"$fixtures/redis-regressed.json"
must_reject redis-continuity "$fixtures/redis-before.json" "$fixtures/redis-regressed.json"
jq -n '{sequencer:{key:"seq",value:"11"},allocators:[{key:"a",value:"1",pttl_ms:90},{key:"b",value:"2",pttl_ms:90}]}' >"$fixtures/redis-valid.json"
"$validator" redis-continuity "$fixtures/redis-before.json" "$fixtures/redis-valid.json"

jq -n '{registrations:[{key:"logic/1",value:"v",lease_id:1},{key:"gateway/1",value:"v",lease_id:2}],watch_probe:{service_name:"logic-service",before:2,after:2}}' >"$fixtures/etcd-before.json"
cp "$fixtures/etcd-before.json" "$fixtures/etcd-no-watch.json"
must_reject etcd-continuity "$fixtures/etcd-before.json" "$fixtures/etcd-no-watch.json"
jq '(.registrations[0].lease_id)=3 | (.registrations[1].lease_id)=4 | .watch_probe.after=3' "$fixtures/etcd-before.json" >"$fixtures/etcd-recreated-leases.json"
must_reject etcd-continuity "$fixtures/etcd-before.json" "$fixtures/etcd-recreated-leases.json"
jq '.watch_probe.after=3' "$fixtures/etcd-before.json" >"$fixtures/etcd-valid.json"
"$validator" etcd-continuity "$fixtures/etcd-before.json" "$fixtures/etcd-valid.json"

echo "Stage 3 evidence negative gates: PASS"
