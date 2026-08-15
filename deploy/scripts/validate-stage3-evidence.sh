#!/usr/bin/env bash
set -euo pipefail

command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 2; }

case "${1:-}" in
  dashboard-metrics)
    input=${2:-}
    jq -e '
      .status == "success" and (
        ([.data.result[]?.metric.__name__] | unique) as $metric_names |
        ["logic_outbox_backlog","mq_publish_total","task_storage_process_duration_seconds_count","pilot_run_duration_seconds_count"] as $required |
        all($required[]; . as $name | $metric_names | index($name))
      )
    ' "$input" >/dev/null
    ;;
  telemetry-bindings)
    logs=${2:-}
    im=${3:-}
    agent=${4:-}
    im_event_id=$(jq -er '.message_event_id | select(type == "number" and . > 0)' "$im")
    agent_run_id=$(jq -er '.read_tool_run_id | select(type == "string" and length > 0)' "$agent")
    jq -n -e \
      --slurpfile logs "$logs" \
      --argjson im_event_id "$im_event_id" \
      --arg agent_run_id "$agent_run_id" '
      def valid_trace: type == "string" and test("^[0-9a-f]{32}$");
      ($logs[0] | [ .[] | select(.service == "task" and .event_id == $im_event_id and (.trace_id | valid_trace)) | .trace_id ] | unique) as $im_traces |
      ($logs[0] | [ .[] | select(.service == "pilot" and .run_id == $agent_run_id and .msg == "agent run started" and (.trace_id | valid_trace)) | .trace_id ] | unique) as $agent_traces |
      ($logs[0] | [ .[] | select(.trace_id? | valid_trace) | {trace_id,service} ] | group_by(.trace_id) | map({key:.[0].trace_id,value:([.[].service] | unique)}) | from_entries) as $services |
      ($im_traces | length) == 1 and ($agent_traces | length) == 1 and
      $im_traces[0] != $agent_traces[0] and
      all(["gateway","logic","task"][]; . as $service | $services[$im_traces[0]] | index($service)) and
      all(["gateway","logic","pilot"][]; . as $service | $services[$agent_traces[0]] | index($service))
    ' >/dev/null
    ;;
  grafana-links)
    loki=${2:-}
    tempo=${3:-}
    jq -e '
      .uid == "loki" and .type == "loki" and
      any(.jsonData.derivedFields[]?;
        .name == "TraceID" and .datasourceUid == "tempo" and
        .url == "${__value.raw}" and
        (.matcherRegex | test("trace_id")))
    ' "$loki" >/dev/null
    jq -e '
      .uid == "tempo" and .type == "tempo" and
      .jsonData.tracesToLogsV2.datasourceUid == "loki" and
      .jsonData.tracesToLogsV2.filterByTraceID == true and
      any(.jsonData.tracesToLogsV2.tags[]?;
        .key == "service.name" and .value == "otel_service_name")
    ' "$tempo" >/dev/null
    ;;
  shutdown-flush)
    input=${2:-}
    service=${3:-}
    trace_id=${4:-}
    command -v xxd >/dev/null 2>&1 || { echo "xxd is required" >&2; exit 2; }
    trace_id_base64=$(printf '%s' "$trace_id" | xxd -r -p | base64 | tr -d '\n')
    jq -e --arg service "$service" --arg trace_id_base64 "$trace_id_base64" '
      ([.batches[]? |
        select(any(.resource.attributes[]?;
          .key == "service.name" and .value.stringValue == $service)) |
        .scopeSpans[]?.spans[]? |
        select(.traceId == $trace_id_base64 and .name == "stage3.shutdown.flush")
      ] | length) == 1
    ' "$input" >/dev/null
    ;;
  nats-continuity)
    before=${2:-}
    after=${3:-}
    probe=${4:-}
    jq -n -e --slurpfile before "$before" --slurpfile after "$after" --slurpfile probe "$probe" '
      def snapshot($root):
        [ $root.account_details[]?.stream_detail[]? as $stream |
          $stream.consumer_detail[]? |
          {key:($stream.name + "/" + .name),created,config:(.config // null),
           delivered_consumer:(.delivered.consumer_seq // 0),
           delivered_stream:(.delivered.stream_seq // 0),
           ack_consumer:(.ack_floor.consumer_seq // 0),
           ack_stream:(.ack_floor.stream_seq // 0)}
        ] | sort_by(.key);
      snapshot($before[0]) as $b | snapshot($after[0]) as $a |
      $probe[0] as $p |
      ($b | length) > 0 and
      ([$b[].key] == [$a[].key]) and
      all(range(0; $b|length);
        ($b[.].config | type) == "object" and
        $a[.].config == $b[.].config and
        $a[.].delivered_consumer >= $b[.].delivered_consumer and
        $a[.].delivered_stream >= $b[.].delivered_stream and
        $a[.].ack_consumer >= $b[.].ack_consumer and
        $a[.].ack_stream >= $b[.].ack_stream and
        $a[.].ack_consumer <= $a[.].delivered_consumer and
        $a[.].ack_stream <= $a[.].delivered_stream) and
      $p.schema_version == 1 and
      $p.message_event_id > 0 and $p.message_seq_id > 0 and
      $p.duplicate_event_id == $p.message_event_id and
      $p.offline_message_event_id > 0 and $p.offline_message_seq_id > 0 and
      $p.recall_event_id > 0 and
      $p.inbox_recovery_verified == true and
      $p.multi_device_read_seen == true and
      $p.idempotency_verified == true
    ' >/dev/null
    ;;
  nats-created-audit)
    before=${2:-}
    after=${3:-}
    issue=${4:-}
    jq -n --slurpfile before "$before" --slurpfile after "$after" --arg issue "$issue" '
      def snapshot($root):
        [ $root.account_details[]?.stream_detail[]? as $stream |
          $stream.consumer_detail[]? |
          {key:($stream.name + "/" + .name),created}
        ] | sort_by(.key);
      snapshot($before[0]) as $b | snapshot($after[0]) as $a |
      [range(0; $b|length) |
        select($b[.].key == $a[.].key and $b[.].created != $a[.].created) |
        {key:$b[.].key,before_created:$b[.].created,after_created:$a[.].created}
      ] as $changed |
      {
        schema_version:1,
        compatibility_issue:$issue,
        created_is_durable_identity:false,
        total_consumers:($b|length),
        changed_count:($changed|length),
        changed_consumers:$changed,
        conclusion:(if ($changed|length) == 0 then "created_stable" else "created_metadata_drift_observed" end)
      }
    '
    ;;
  redis-continuity)
    before=${2:-}
    after=${3:-}
    jq -n -e --slurpfile before "$before" --slurpfile after "$after" '
      $before[0] as $b | $after[0] as $a |
      $b.sequencer.key == $a.sequencer.key and
      ($b.sequencer.value | tonumber) <= ($a.sequencer.value | tonumber) and
      ($b.allocators | length) == 2 and
      ([$b.allocators[] | {key,value}] | sort_by(.key)) ==
        ([$a.allocators[] | {key,value}] | sort_by(.key)) and
      all($a.allocators[]; .pttl_ms > 0)
    ' >/dev/null
    ;;
  etcd-continuity)
    before=${2:-}
    after=${3:-}
    jq -n -e --slurpfile before "$before" --slurpfile after "$after" '
      $before[0] as $b | $after[0] as $a |
      ($b.registrations | length) >= 2 and
      ([$b.registrations[] | {key,value,lease_id}] | sort_by(.key)) ==
        ([$a.registrations[] | {key,value,lease_id}] | sort_by(.key)) and
      all($b.registrations[]; .lease_id > 0) and
      all($a.registrations[]; .lease_id > 0) and
      $a.watch_probe.service_name == "logic-service" and
      $a.watch_probe.after > $a.watch_probe.before
    ' >/dev/null
    ;;
  hosted-ci)
    input=${2:-}
    sha=${3:-}
    jq -e --arg sha "$sha" '
      ["check-gen","docs-and-format","go-lint","go-security","go-test","pilot-bridge","pilot-image","proto-lint","web"] as $required |
      ([.checks[].name] | sort) == ($required | sort) and
      (.checks | length) == 9 and
      all(.checks[]; .head_sha == $sha and .status == "completed" and .conclusion == "success")
    ' "$input" >/dev/null
    ;;
  *)
    echo "usage: $0 {dashboard-metrics|telemetry-bindings|grafana-links|shutdown-flush|nats-continuity|nats-created-audit|redis-continuity|etcd-continuity|hosted-ci} ..." >&2
    exit 2
    ;;
esac
