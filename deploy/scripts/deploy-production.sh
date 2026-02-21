#!/bin/bash
# 生产环境部署脚本
# 用法：./deploy/scripts/deploy-production.sh [TAG]
# 示例：./deploy/scripts/deploy-production.sh latest

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$PROJECT_ROOT"

TAG=${1:-latest}

echo "🚀 生产环境部署 (镜像: ceyewan/resonance:$TAG)"
echo ""

get_env() {
    local key="$1"
    grep -E "^${key}=" .env | tail -n1 | cut -d= -f2-
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

# 检查运行环境
if ! grep -Eq "^RESONANCE_ENV=prod$" .env; then
    echo "❌ 错误：生产部署要求 RESONANCE_ENV=prod"
    exit 1
fi

# 检查域名与敏感配置
require_non_empty CADDY_GATEWAY_DOMAIN "CADDY_GATEWAY_DOMAIN=im-api.ceyewan.xyz"
require_non_empty CADDY_WEB_DOMAIN "CADDY_WEB_DOMAIN=ceyewan.xyz"
validate_prod_security

GATEWAY_DOMAIN="$(get_env CADDY_GATEWAY_DOMAIN)"
WEB_DOMAIN="$(get_env CADDY_WEB_DOMAIN)"

# 检查 Caddy 网络
if ! docker network inspect caddy >/dev/null 2>&1; then
    echo "❌ 错误: caddy 网络不存在"
    echo "请先安装 Caddy Docker Proxy"
    exit 1
fi

# 创建网络
docker network create resonance-net 2>/dev/null || true

# 拉取镜像
echo "📥 拉取镜像..."
docker pull ceyewan/resonance:$TAG

# 启动服务（使用 .env 中的配置 + profile production 启用 Watchtower）
echo "🚀 启动服务..."
RESONANCE_IMAGE=ceyewan/resonance:$TAG \
docker compose --env-file .env -p resonance \
    -f deploy/base.yaml \
    -f deploy/services.yaml \
    -f deploy/services.prod.yaml \
    --profile production up -d

echo ""
echo "✅ 部署完成"
echo "📊 访问地址:"
echo "  - Gateway: https://$GATEWAY_DOMAIN"
echo "  - Web:     https://$WEB_DOMAIN"
echo ""
echo "💡 Watchtower 已启用，每 60 秒检查镜像更新"
