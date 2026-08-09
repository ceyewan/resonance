#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"
prefix=${1:-}
report_path=${2:-}
if [[ ! "$prefix" =~ ^[A-Za-z0-9_-]{1,24}$ ]] || [[ -z "$report_path" ]]; then
  echo "usage: $0 <safe-test-prefix> <report-path>" >&2
  exit 2
fi

COMPOSE=(docker compose --env-file .env -p resonance-v1 -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml -f deploy/observability.yaml)
requester_username="$prefix-iam-requester"
requester_password='Resonance-Deterministic-E2E-2026!'
register_body=$(jq -nc --arg username "$requester_username" --arg password "$requester_password" \
  '{username:$username,password:$password,nickname:"Local deterministic IAM requester"}')
curl --fail --silent --show-error -H 'Content-Type: application/json' \
  --data "$register_body" http://127.0.0.1:18080/resonance.gateway.v1.AuthService/Register >/dev/null

db_user=$(sed -n 's/^RESONANCE_POSTGRES_USER=//p' .env | tail -n 1)
db_name=$(sed -n 's/^RESONANCE_POSTGRES_DATABASE=//p' .env | tail -n 1)
db_user=${db_user:-resonance}
db_name=${db_name:-resonance}
"${COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -v requester="$requester_username" \
  -U "$db_user" -d "$db_name" <<'SQL' >/dev/null
INSERT INTO t_system_role_binding (tenant_id,username,role,created_at,updated_at)
VALUES ('default', :'requester', 'iam-admin', NOW(), NOW())
ON CONFLICT DO NOTHING;
UPDATE t_tenant_membership SET version=version+1,updated_at=NOW()
WHERE tenant_id='default' AND username=:'requester';
SQL

admin_username=$(sed -n 's/^RESONANCE_ADMIN_USERNAME=//p' .env | tail -n 1)
admin_password=$(sed -n 's/^RESONANCE_ADMIN_PASSWORD=//p' .env | tail -n 1)
RESONANCE_DETERMINISTIC_AGENT_E2E=1 \
RESONANCE_LIVE_BASE_URL=http://127.0.0.1:18080 \
RESONANCE_E2E_PREFIX="$prefix" \
RESONANCE_E2E_ADMIN_USERNAME="$admin_username" \
RESONANCE_E2E_ADMIN_PASSWORD="$admin_password" \
RESONANCE_E2E_IAM_REQUESTER_USERNAME="$requester_username" \
RESONANCE_E2E_IAM_REQUESTER_PASSWORD="$requester_password" \
RESONANCE_AGENT_E2E_REPORT="$report_path" \
  go test ./test/live -run '^TestAgentServiceDeterministicCompose$' -count=1 -v
