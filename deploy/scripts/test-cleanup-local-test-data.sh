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

psql_exec() {
  "${COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 \
    -v target="$target" -v adjacent="$adjacent" -U "$db_user" -d "$db_name" "$@"
}

cleanup() {
  psql_exec <<'SQL' >/dev/null 2>&1 || true
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

psql_exec <<'SQL' >/dev/null
INSERT INTO t_user (username, nickname, password, kind, created_at, updated_at)
VALUES
  (:'target', 'cleanup target', 'not-a-login-password', 0, NOW(), NOW()),
  (:'adjacent', 'cleanup neighbor', 'not-a-login-password', 0, NOW(), NOW());
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
