#!/bin/bash
# 本地 Docker 部署脚本
# 用法：./deploy/scripts/deploy-local.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$PROJECT_ROOT"

echo "🚀 本地 Docker 部署"
echo ""

# 1. 检查 .env 文件
if [ ! -f .env ]; then
    echo "❌ 错误：.env 文件不存在"
    echo "请先创建 .env 文件："
    echo "  cp .env.example .env"
    exit 1
fi

# 2. 构建本地镜像
echo "📦 构建镜像..."
docker build --target final -t ceyewan/resonance:local -f deploy/Dockerfile .

# 3. 启动服务
echo "🚀 启动服务..."
docker compose --env-file .env -p resonance -f deploy/base.yaml -f deploy/services.yaml -f deploy/services.local.yaml up -d

echo ""
echo "✅ 部署完成"
echo "📊 访问地址:"
echo "  - Web:     http://localhost:4173"
echo "  - Gateway: http://localhost:8080"
