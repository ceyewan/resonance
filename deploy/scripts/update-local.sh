#!/bin/bash
# 重新构建并更新本地 Docker 部署。
# 用法：./deploy/scripts/update-local.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$PROJECT_ROOT"

if [ ! -f .env ]; then
    echo "错误：.env 文件不存在"
    echo "请先创建 .env 文件：cp .env.example .env"
    exit 1
fi

echo "更新本地 Docker 部署..."
docker compose --env-file .env -p resonance -f deploy/base.yaml -f deploy/services.yaml up -d postgres redis nats etcd
docker compose --env-file .env -p resonance -f deploy/base.yaml -f deploy/services.yaml up -d --build --force-recreate init logic task gateway web

echo ""
docker compose --env-file .env -p resonance -f deploy/base.yaml -f deploy/services.yaml ps
echo ""
echo "本地 Docker 部署已更新"
echo "Web:     http://localhost:4173"
echo "Gateway: http://localhost:8080"
