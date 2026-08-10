#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
prefix=${1:-}
if [[ ! "$prefix" =~ ^(lv1|rc|al|lb|cln)-?[A-Za-z0-9_-]{8,72}$ ]]; then
  echo "refusing cleanup: test prefix is missing or unsafe" >&2
  exit 2
fi

COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)
db_user=$(sed -n 's/^RESONANCE_POSTGRES_USER=//p' .env | tail -n 1)
db_name=$(sed -n 's/^RESONANCE_POSTGRES_DATABASE=//p' .env | tail -n 1)
db_user=${db_user:-resonance}
db_name=${db_name:-resonance}

"${COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -v test_prefix="$prefix" -U "$db_user" -d "$db_name" <<'SQL'
BEGIN;
CREATE TEMP TABLE cleanup_users ON COMMIT DROP AS
  -- left(...)=prefix is a literal prefix comparison. Unlike LIKE, underscores
  -- and percent signs cannot expand the deletion set as SQL wildcards.
  SELECT username FROM t_user
  WHERE left(username, length(:'test_prefix')) = :'test_prefix';
CREATE TEMP TABLE cleanup_sessions ON COMMIT DROP AS
  SELECT session_id FROM t_session
  WHERE owner_username IN (SELECT username FROM cleanup_users);
CREATE TEMP TABLE cleanup_runs ON COMMIT DROP AS
  SELECT run_id FROM t_agent_run
  WHERE actor_username IN (SELECT username FROM cleanup_users)
     OR conversation_id IN (SELECT session_id FROM cleanup_sessions);
-- The run row is the synchronization point shared with claiming, retry, and
-- settlement. Hold it until COMMIT and reject anything that is not fully
-- terminal before examining its budget attempts.
DO $$
BEGIN
  PERFORM 1 FROM t_agent_run
  WHERE run_id IN (SELECT run_id FROM cleanup_runs)
  ORDER BY run_id
  FOR UPDATE;
  IF EXISTS (
    SELECT 1 FROM t_agent_run
    WHERE run_id IN (SELECT run_id FROM cleanup_runs)
      AND (status NOT IN ('SUCCEEDED', 'FAILED_FINAL', 'CANCELLED')
        OR lease_owner <> '' OR lease_token <> '' OR lease_expires_at IS NOT NULL)
  ) THEN
    RAISE EXCEPTION 'refusing cleanup: Agent run is active or still leased';
  END IF;
END $$;
-- Lock the attempts before deriving the inverse bucket entries. A concurrent
-- settlement must not change an attempt between the snapshot and deletion.
CREATE TEMP TABLE cleanup_budget_attempts ON COMMIT DROP AS
  SELECT a.* FROM t_agent_budget_attempt a
  WHERE a.run_id IN (SELECT run_id FROM cleanup_runs)
  FOR UPDATE;
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM cleanup_budget_attempts
    WHERE status NOT IN ('RESERVED', 'SETTLED', 'RELEASED', 'UNKNOWN', 'OVERDRAWN')
  ) THEN
    RAISE EXCEPTION 'refusing cleanup: unsupported Agent budget attempt status';
  END IF;
END $$;
CREATE TEMP TABLE cleanup_budget_delta ON COMMIT DROP AS
  SELECT tenant_id, 'DAY'::varchar(8) AS period_kind, day_period_start AS period_start,
    sum(CASE WHEN status IN ('RESERVED', 'UNKNOWN') THEN reserved_tokens ELSE 0 END)::bigint AS reserved_tokens,
    sum(CASE WHEN status IN ('SETTLED', 'OVERDRAWN') THEN actual_total_tokens ELSE 0 END)::bigint AS settled_tokens,
    sum(CASE WHEN status = 'UNKNOWN' THEN reserved_tokens ELSE 0 END)::bigint AS unknown_reserved_tokens,
    sum(CASE WHEN status IN ('RESERVED', 'UNKNOWN') THEN reserved_cost_micros ELSE 0 END)::bigint AS reserved_cost_micros,
    sum(CASE WHEN status IN ('SETTLED', 'OVERDRAWN') THEN actual_cost_micros ELSE 0 END)::bigint AS settled_cost_micros,
    sum(CASE WHEN status = 'UNKNOWN' THEN reserved_cost_micros ELSE 0 END)::bigint AS unknown_reserved_cost_micros
  FROM cleanup_budget_attempts GROUP BY tenant_id, day_period_start
  UNION ALL
  SELECT tenant_id, 'MONTH'::varchar(8), month_period_start,
    sum(CASE WHEN status IN ('RESERVED', 'UNKNOWN') THEN reserved_tokens ELSE 0 END)::bigint,
    sum(CASE WHEN status IN ('SETTLED', 'OVERDRAWN') THEN actual_total_tokens ELSE 0 END)::bigint,
    sum(CASE WHEN status = 'UNKNOWN' THEN reserved_tokens ELSE 0 END)::bigint,
    sum(CASE WHEN status IN ('RESERVED', 'UNKNOWN') THEN reserved_cost_micros ELSE 0 END)::bigint,
    sum(CASE WHEN status IN ('SETTLED', 'OVERDRAWN') THEN actual_cost_micros ELSE 0 END)::bigint,
    sum(CASE WHEN status = 'UNKNOWN' THEN reserved_cost_micros ELSE 0 END)::bigint
  FROM cleanup_budget_attempts GROUP BY tenant_id, month_period_start;
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM cleanup_budget_delta d
    LEFT JOIN t_agent_budget_bucket b
      ON b.tenant_id=d.tenant_id AND b.period_kind=d.period_kind AND b.period_start=d.period_start
    WHERE b.tenant_id IS NULL
       OR b.reserved_tokens < d.reserved_tokens
       OR b.settled_tokens < d.settled_tokens
       OR b.unknown_reserved_tokens < d.unknown_reserved_tokens
       OR b.reserved_cost_micros < d.reserved_cost_micros
       OR b.settled_cost_micros < d.settled_cost_micros
       OR b.unknown_reserved_cost_micros < d.unknown_reserved_cost_micros
  ) THEN
    RAISE EXCEPTION 'refusing cleanup: Agent budget bucket is missing or smaller than the test delta';
  END IF;
END $$;
UPDATE t_agent_budget_bucket b SET
  reserved_tokens=b.reserved_tokens-d.reserved_tokens,
  settled_tokens=b.settled_tokens-d.settled_tokens,
  unknown_reserved_tokens=b.unknown_reserved_tokens-d.unknown_reserved_tokens,
  reserved_cost_micros=b.reserved_cost_micros-d.reserved_cost_micros,
  settled_cost_micros=b.settled_cost_micros-d.settled_cost_micros,
  unknown_reserved_cost_micros=b.unknown_reserved_cost_micros-d.unknown_reserved_cost_micros,
  version=b.version+1,
  updated_at=NOW()
FROM cleanup_budget_delta d
WHERE b.tenant_id=d.tenant_id AND b.period_kind=d.period_kind AND b.period_start=d.period_start;
CREATE TEMP TABLE cleanup_events ON COMMIT DROP AS
  SELECT event_id FROM t_message_content
  WHERE session_id IN (SELECT session_id FROM cleanup_sessions);

DELETE FROM t_agent_iam_mutation_receipt
 WHERE run_id IN (SELECT run_id FROM cleanup_runs)
    OR requester_id IN (SELECT username FROM cleanup_users)
    OR target_username IN (SELECT username FROM cleanup_users);
DELETE FROM t_agent_tool_execution WHERE run_id IN (SELECT run_id FROM cleanup_runs);
DELETE FROM t_agent_frozen_tool_args WHERE run_id IN (SELECT run_id FROM cleanup_runs);
DELETE FROM t_agent_approval
 WHERE run_id IN (SELECT run_id FROM cleanup_runs)
    OR requester_id IN (SELECT username FROM cleanup_users);
DELETE FROM t_agent_audit_log WHERE run_id IN (SELECT run_id FROM cleanup_runs);
DELETE FROM t_agent_budget_attempt WHERE run_id IN (SELECT run_id FROM cleanup_runs);
DELETE FROM t_agent_run WHERE run_id IN (SELECT run_id FROM cleanup_runs);
DELETE FROM t_agent_session_binding WHERE conversation_id IN (SELECT session_id FROM cleanup_sessions);
DELETE FROM t_inbox
 WHERE owner_username IN (SELECT username FROM cleanup_users)
    OR session_id IN (SELECT session_id FROM cleanup_sessions);
DELETE FROM t_message_outbox WHERE event_id IN (SELECT event_id FROM cleanup_events);
DELETE FROM t_message_content WHERE event_id IN (SELECT event_id FROM cleanup_events);
DELETE FROM t_session_member
 WHERE session_id IN (SELECT session_id FROM cleanup_sessions)
    OR username IN (SELECT username FROM cleanup_users);
DELETE FROM t_session WHERE session_id IN (SELECT session_id FROM cleanup_sessions);
DELETE FROM t_system_role_binding WHERE username IN (SELECT username FROM cleanup_users);
DELETE FROM t_tenant_membership WHERE username IN (SELECT username FROM cleanup_users);
DELETE FROM t_user WHERE username IN (SELECT username FROM cleanup_users);
COMMIT;
SQL
