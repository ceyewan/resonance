#!/bin/bash
# 本地 Docker 部署脚本
# 用法：./deploy/scripts/deploy-local.sh

set -e

echo "🚀 本地 Docker 部署"
echo ""

# 1. 检查 .env 文件
if [ ! -f .env ]; then
    echo "❌ 错误：.env 文件不存在"
    echo "请先创建 .env 文件："
    echo "  cp .env.example .env"
    echo "  vim .env  # 确保 RESONANCE_ENV=prod"
    exit 1
fi

# 2. 检查 RESONANCE_ENV 配置
if ! grep -q "^RESONANCE_ENV=prod" .env; then
    echo "⚠️  警告：.env 中 RESONANCE_ENV 未设置为 prod"
    echo ""
    echo "Docker 环境需要使用 prod 配置以连接 Docker hostname（postgres、redis 等）"
    echo "请在 .env 中设置："
    echo "  RESONANCE_ENV=prod"
    echo ""
    read -p "是否继续？(y/N) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 1
    fi
fi

# 3. 构建本地镜像
echo "📦 构建镜像..."
docker build --target final -t ceyewan/resonance:local -f deploy/Dockerfile .

# 4. 创建网络
docker network create caddy 2>/dev/null || true
docker network create resonance-net 2>/dev/null || true

# 5. 启动服务
echo "🚀 启动服务..."
docker compose -p resonance -f deploy/base.yaml -f deploy/services.yaml up -d

echo ""
echo "✅ 部署完成"
echo "📊 访问地址:"
echo "  - Web:     http://localhost:4173"
echo "  - Gateway: http://localhost:8080"
