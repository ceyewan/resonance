#!/usr/bin/env bash
# Explicit production deployment; never prints Provider or signing credentials.
set -euo pipefail
cd "$(dirname "$0")/../.."
[[ -f .env ]] || { echo '请先创建 .env'; exit 1; }
tag=${1:-latest}
[[ "$tag" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$ ]] || exit 1
get_env() { local line; line=$(grep -E "^$1=" .env | tail -n1 || true); printf '%s' "${line#*=}"; }
keys=(RESONANCE_AUTH_SECRET_KEY RESONANCE_GATEWAY_SERVICE_AUTH_SECRET RESONANCE_PILOT_SERVICE_AUTH_SECRET)
for key in "${keys[@]}"; do
  value=$(get_env "$key")
  [[ ${#value} -ge 32 && "$value" != *replace-with-* ]] || { echo "$key 需要独立强密钥"; exit 1; }
done
for ((i=0;i<${#keys[@]};i++)); do
  for ((j=i+1;j<${#keys[@]};j++)); do
    [[ "$(get_env "${keys[i]}")" != "$(get_env "${keys[j]}")" ]] || { echo '签名密钥不能复用'; exit 1; }
  done
done
for key in RESONANCE_POSTGRES_PASSWORD RESONANCE_ADMIN_PASSWORD DASHSCOPE_API_KEY CADDY_GATEWAY_DOMAIN CADDY_WEB_DOMAIN; do
  value=$(get_env "$key")
  [[ -n "$value" && "$value" != *replace-with-* && "$value" != resonance123 && "$value" != admin123 ]] || { echo "$key 未配置或使用默认值"; exit 1; }
done
[[ "$(get_env DASHSCOPE_BASE_URL)" == https://* ]] || { echo 'Provider endpoint 必须使用 HTTPS'; exit 1; }
[[ "$(get_env RESONANCE_PILOT_IMAGE)" =~ ^[^[:space:]@]+@sha256:[0-9a-f]{64}$ ]] || { echo 'RESONANCE_PILOT_IMAGE 必须固定 digest'; exit 1; }
export RESONANCE_IMAGE="ceyewan/resonance:$tag"
compose=(docker compose --env-file .env -p resonance -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.prod.yaml --profile production)
"${compose[@]}" config --quiet
"${compose[@]}" pull
"${compose[@]}" up -d --remove-orphans
