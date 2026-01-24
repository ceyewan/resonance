#!/bin/bash
# 生产环境部署脚本
# 用法：./scripts/deploy-production.sh [TAG]
# 示例：./scripts/deploy-production.sh latest

set -e

TAG=${1:-latest}

echo "🚀 生产环境部署 (镜像: ceyewan/resonance:$TAG)"
echo ""

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
docker compose -p resonance -f deploy/base.yaml -f deploy/services.yaml --profile production up -d

echo ""
echo "✅ 部署完成"
echo "📊 访问地址:"
echo "  - Gateway: https://im-api.ceyewan.xyz"
echo "  - Web:     https://chat.ceyewan.xyz"
echo ""
echo "💡 Watchtower 已启用，每 60 秒检查镜像更新"

