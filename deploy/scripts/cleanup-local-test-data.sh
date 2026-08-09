#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
prefix=${1:-}
if [[ ! "$prefix" =~ ^[A-Za-z0-9_-]{1,80}$ ]]; then
  echo "refusing cleanup: test prefix is missing or unsafe" >&2
  exit 2
fi

COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)
db_user=$(sed -n 's/^RESONANCE_POSTGRES_USER=//p' .env | tail -n 1)
db_name=$(sed -n 's/^RESONANCE_POSTGRES_DATABASE=//p' .env | tail -n 1)
db_user=${db_user:-resonance}
db_name=${db_name:-resonance}

"${COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -v test_prefix="${prefix}%" -U "$db_user" -d "$db_name" <<'SQL'
BEGIN;
CREATE TEMP TABLE cleanup_users ON COMMIT DROP AS
  SELECT username FROM t_user WHERE username LIKE :'test_prefix';
CREATE TEMP TABLE cleanup_sessions ON COMMIT DROP AS
  SELECT session_id FROM t_session
  WHERE owner_username IN (SELECT username FROM cleanup_users);
CREATE TEMP TABLE cleanup_runs ON COMMIT DROP AS
  SELECT run_id FROM t_agent_run
  WHERE actor_username IN (SELECT username FROM cleanup_users)
     OR conversation_id IN (SELECT session_id FROM cleanup_sessions);
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
