#!/bin/bash
# 生产环境部署脚本
# 用法：./deploy/scripts/deploy-production.sh [TAG]
# 示例：./deploy/scripts/deploy-production.sh latest

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$PROJECT_ROOT"

TAG=${1:-latest}

if [[ ! "$TAG" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$ ]]; then
    echo "❌ 错误：主应用镜像 tag 非法"
    exit 1
fi

echo "🚀 生产环境部署 (镜像: ceyewan/resonance:$TAG)"
echo ""

get_env() {
    local key="$1"
    local line
    line="$(grep -E "^${key}=" .env | tail -n1 || true)"
    printf '%s' "${line#*=}"
}

require_non_empty() {
    local key="$1"
    local hint="$2"
    local value
    value="$(get_env "$key")"
    if [ -z "$value" ]; then
        echo "❌ 错误：.env 中 ${key} 未设置"
        echo "例如：${hint}"
        exit 1
    fi
}

require_digest_ref() {
    local key="$1"
    local value
    value="$(get_env "$key")"
    if [[ ! "$value" =~ ^[^[:space:]@]+@sha256:[0-9a-f]{64}$ ]]; then
        echo "❌ 错误：${key} 必须是不变的 repository@sha256:<64 lowercase hex>"
        exit 1
    fi
}

require_strong_secret() {
    local key="$1"
    local value
    value="$(get_env "$key")"
    if [[ ${#value} -lt 32 || "$value" == *"replace-with-"* ]]; then
        echo "❌ 错误：${key} 需至少 32 位且不能是占位符"
        exit 1
    fi
}

require_dashscope_config() {
    local api_key base_url model
    api_key="$(get_env DASHSCOPE_API_KEY)"
    base_url="$(get_env DASHSCOPE_BASE_URL)"
    model="$(get_env DASHSCOPE_MODEL)"
    if [[ ${#api_key} -lt 8 || "$api_key" == *"replace-with-"* || "$api_key" == *"<"* ]]; then
        echo "❌ 错误：DASHSCOPE_API_KEY 未设置或仍是占位符"
        exit 1
    fi
    if [[ "$base_url" != "https://llm-3rwbpx52jtt7759p.cn-beijing.maas.aliyuncs.com/compatible-mode/v1" ]]; then
        echo "❌ 错误：DASHSCOPE_BASE_URL 必须与 egress allowlist 对应的按量付费业务空间 endpoint 完全一致"
        exit 1
    fi
    if [[ "$model" != "qwen3.8-max" ]]; then
        echo "❌ 错误：DASHSCOPE_MODEL 必须是已验证并由 Profile 固定的 qwen3.8-max"
        exit 1
    fi
}

require_distinct_secrets() {
    local keys=(
        RESONANCE_AUTH_SECRET_KEY
        RESONANCE_GATEWAY_SERVICE_AUTH_SECRET
        RESONANCE_PILOT_CAPABILITY_SECRET
        RESONANCE_PILOT_SERVICE_AUTH_SECRET
        RESONANCE_PILOT_IAM_CAPABILITY_SECRET
        RESONANCE_PILOT_IAM_SERVICE_AUTH_SECRET
    )
    local left right
    for ((left = 0; left < ${#keys[@]}; left++)); do
        for ((right = left + 1; right < ${#keys[@]}; right++)); do
            if [[ "$(get_env "${keys[left]}")" == "$(get_env "${keys[right]}")" ]]; then
                echo "❌ 错误：${keys[left]} 与 ${keys[right]} 不得复用"
                exit 1
            fi
        done
    done
}

# 生产环境：校验常见弱配置，避免误上生产
validate_prod_security() {
    local auth_secret postgres_password admin_password
    auth_secret="$(get_env RESONANCE_AUTH_SECRET_KEY)"
    postgres_password="$(get_env RESONANCE_POSTGRES_PASSWORD)"
    admin_password="$(get_env RESONANCE_ADMIN_PASSWORD)"

    if [ -z "$auth_secret" ] || [ "${#auth_secret}" -lt 32 ] || [[ "$auth_secret" == *"replace-with-"* ]]; then
        echo "❌ 错误：RESONANCE_AUTH_SECRET_KEY 不安全（需至少 32 位且不能是占位符）"
        exit 1
    fi
    if [ -z "$postgres_password" ] || [ "$postgres_password" = "resonance123" ] || [[ "$postgres_password" == *"replace-with-"* ]]; then
        echo "❌ 错误：RESONANCE_POSTGRES_PASSWORD 使用了默认/占位值，请修改"
        exit 1
    fi
    if [ -z "$admin_password" ] || [ "$admin_password" = "admin123" ] || [[ "$admin_password" == *"replace-with-"* ]]; then
        echo "❌ 错误：RESONANCE_ADMIN_PASSWORD 使用了默认/占位值，请修改"
        exit 1
    fi
}

# 检查 .env 文件
if [ ! -f .env ]; then
    echo "❌ 错误：.env 文件不存在"
    echo "请先创建：cp .env.example .env"
    exit 1
fi

# 检查域名与敏感配置
require_non_empty CADDY_GATEWAY_DOMAIN "CADDY_GATEWAY_DOMAIN=im-api.ceyewan.xyz"
require_non_empty CADDY_WEB_DOMAIN "CADDY_WEB_DOMAIN=ceyewan.xyz"
require_digest_ref RESONANCE_PILOT_IMAGE
require_digest_ref RESONANCE_PILOT_RUNTIME_IMAGE
require_strong_secret RESONANCE_GATEWAY_SERVICE_AUTH_SECRET
require_strong_secret RESONANCE_PILOT_CAPABILITY_SECRET
require_strong_secret RESONANCE_PILOT_SERVICE_AUTH_SECRET
require_strong_secret RESONANCE_PILOT_IAM_CAPABILITY_SECRET
require_strong_secret RESONANCE_PILOT_IAM_SERVICE_AUTH_SECRET
require_dashscope_config
validate_prod_security
require_distinct_secrets

GATEWAY_DOMAIN="$(get_env CADDY_GATEWAY_DOMAIN)"
WEB_DOMAIN="$(get_env CADDY_WEB_DOMAIN)"
PILOT_IMAGE="$(get_env RESONANCE_PILOT_IMAGE)"
PILOT_RUNTIME_IMAGE="$(get_env RESONANCE_PILOT_RUNTIME_IMAGE)"

# 检查 Caddy 网络
if ! docker network inspect caddy >/dev/null 2>&1; then
    echo "❌ 错误: caddy 网络不存在"
    echo "请先安装 Caddy Docker Proxy"
    exit 1
fi

# 拉取镜像
echo "📥 拉取镜像..."
docker pull "ceyewan/resonance:$TAG"
docker pull "$PILOT_IMAGE"
docker pull "$PILOT_RUNTIME_IMAGE"

# 启动服务（使用 .env 中的配置 + profile production 启用 Watchtower）
echo "🚀 启动服务..."
RESONANCE_IMAGE=ceyewan/resonance:$TAG \
docker compose --env-file .env -p resonance \
    -f deploy/base.yaml \
    -f deploy/services.yaml \
    -f deploy/services.prod.yaml \
    --profile production up -d --wait --wait-timeout 180

echo ""
echo "✅ 部署完成"
echo "📊 访问地址:"
echo "  - Gateway: https://$GATEWAY_DOMAIN"
echo "  - Web:     https://$WEB_DOMAIN"
echo ""
echo "💡 Watchtower 已启用，每 60 秒检查镜像更新"
