#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)
db_user=$(sed -n 's/^RESONANCE_POSTGRES_USER=//p' .env | tail -n 1)
db_name=$(sed -n 's/^RESONANCE_POSTGRES_DATABASE=//p' .env | tail -n 1)
db_user=${db_user:-resonance}
db_name=${db_name:-resonance}

base="cln$(date -u +%m%d%H%M%S)$$"
literal_prefix="${base}_"
target="${literal_prefix}target"
adjacent="${base}Xneighbor"
active_run="${base}-active-run"

psql_exec() {
  "${COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 \
    -v target="$target" -v adjacent="$adjacent" -v active_run="$active_run" -U "$db_user" -d "$db_name" "$@"
}

cleanup() {
  psql_exec <<'SQL' >/dev/null 2>&1 || true
DELETE FROM t_agent_run WHERE run_id = :'active_run';
DELETE FROM t_user WHERE username IN (:'target', :'adjacent');
SQL
}
trap cleanup EXIT

# Shell glob and SQL LIKE wildcard characters must be rejected before Docker or
# SQL is reached. Underscore remains valid because cleanup uses literal prefix
# comparison rather than LIKE.
if deploy/scripts/cleanup-local-test-data.sh "unsafe*prefix" >/dev/null 2>&1; then
  echo "cleanup accepted a shell wildcard" >&2
  exit 1
fi
if deploy/scripts/cleanup-local-test-data.sh "unsafe%prefix" >/dev/null 2>&1; then
  echo "cleanup accepted a SQL wildcard" >&2
  exit 1
fi
if deploy/scripts/cleanup-local-test-data.sh "lv1-short" >/dev/null 2>&1; then
  echo "cleanup accepted a low-entropy prefix" >&2
  exit 1
fi

psql_exec <<'SQL' >/dev/null
INSERT INTO t_user (username, nickname, password, kind, created_at, updated_at)
VALUES
  (:'target', 'cleanup target', 'not-a-login-password', 0, NOW(), NOW()),
  (:'adjacent', 'cleanup neighbor', 'not-a-login-password', 0, NOW(), NOW());
INSERT INTO t_agent_run (
  run_id,tenant_id,conversation_id,source_event_id,source_seq_id,source_timestamp_ms,source_hash,prompt,
  actor_id,actor_username,profile_id,profile_version,runtime_kind,runtime_version,bridge_version,
  model_provider,model_id,status,max_attempts,available_at,queued_at,created_at,updated_at
) VALUES (
  :'active_run','cleanup-test','cleanup-active-session',-900001,1,1,repeat('a',64),'active cleanup guard',
  :'target',:'target','cleanup-profile',1,'test','1','1','test','test','RUNNING',1,NOW(),NOW(),NOW(),NOW()
);
SQL

if deploy/scripts/cleanup-local-test-data.sh "$literal_prefix" >/dev/null 2>&1; then
  echo "cleanup accepted a nonterminal Agent run" >&2
  exit 1
fi
psql_exec <<'SQL' >/dev/null
DELETE FROM t_agent_run WHERE run_id = :'active_run';
SQL

deploy/scripts/cleanup-local-test-data.sh "$literal_prefix" >/dev/null

result=$(psql_exec -At <<'SQL'
SELECT
  (SELECT count(*) FROM t_user WHERE username = :'target') || ',' ||
  (SELECT count(*) FROM t_user WHERE username = :'adjacent');
SQL
)
if [[ "$result" != "0,1" ]]; then
  echo "literal-prefix cleanup safety check failed: expected 0,1, got $result" >&2
  exit 1
fi

echo "cleanup literal-prefix safety: PASS"
