#!/bin/bash
# 本地开发环境部署脚本
# 用法：./scripts/test-deploy-local.sh

set -e

# 定义颜色
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${GREEN}🚀 开始构建本地镜像...${NC}"

# 构建镜像，标记为 local (使用 static 目标，禁用 CGO)
docker build --target final -t ceyewan/resonance:local -f deploy/Dockerfile .

echo -e "${GREEN}✅ 镜像构建成功: ceyewan/resonance:local${NC}"

echo -e "${GREEN}🚀 启动本地服务...${NC}"

# 创建网络 (如果不存在)
docker network create caddy 2>/dev/null || true
docker network create resonance-net 2>/dev/null || true

# 启动服务（本地开发模式）
DEPLOY_ENV=local \
RESONANCE_IMAGE=ceyewan/resonance:local \
GATEWAY_PORT_BINDING="127.0.0.1:8080:8080" \
WEB_PORT_BINDING="127.0.0.1:4173:4173" \
docker compose -p resonance -f deploy/base.yaml -f deploy/services.yaml up -d

echo -e "${GREEN}✅ 服务已启动！${NC}"
echo -e "${YELLOW}访问地址：${NC}"
echo -e "  - Gateway API: http://127.0.0.1:8080"
echo -e "  - Web 前端:    http://127.0.0.1:4173"
