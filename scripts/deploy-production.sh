#!/bin/bash
# 生产环境部署脚本（使用宿主机 Caddy + Watchtower 自动更新）
# 用法：./scripts/deploy-production.sh [TAG]
# 示例：./scripts/deploy-production.sh latest

set -e

# 定义颜色
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

TAG=${1:-latest}

echo -e "${GREEN}🚀 生产环境部署 (镜像版本: $TAG)${NC}"

# 检查 Caddy 网络是否存在
if ! docker network inspect caddy >/dev/null 2>&1; then
    echo -e "${RED}❌ 错误: caddy 网络不存在${NC}"
    echo -e "${YELLOW}请先在宿主机上安装并配置 Caddy Docker Proxy${NC}"
    echo -e "参考: https://github.com/lucaslorentz/caddy-docker-proxy"
    exit 1
fi

# 创建 resonance-net 网络 (如果不存在)
docker network create resonance-net 2>/dev/null || true

echo -e "${GREEN}📥 拉取最新镜像...${NC}"
docker pull ceyewan/resonance:$TAG

echo -e "${GREEN}🚀 启动服务（生产模式 + Watchtower 自动更新）...${NC}"

# 启动服务（生产模式 - 使用宿主机 Caddy + Watchtower）
DEPLOY_ENV=production \
RESONANCE_IMAGE=ceyewan/resonance:$TAG \
CADDY_GATEWAY_DOMAIN="im-api.ceyewan.xyz" \
CADDY_WEB_DOMAIN="chat.ceyewan.xyz" \
GATEWAY_PORT_BINDING="" \
WEB_PORT_BINDING="" \
docker compose -p resonance -f deploy/base.yaml -f deploy/services.yaml --profile production up -d

echo -e "${GREEN}✅ 服务已启动！${NC}"
echo -e "${YELLOW}访问地址（通过 Caddy 反向代理）：${NC}"
echo -e "  - Gateway API: https://im-api.ceyewan.xyz"
echo -e "  - Web 前端:    https://chat.ceyewan.xyz"
echo -e ""
echo -e "${YELLOW}Watchtower 自动更新：${NC}"
echo -e "  - 每 60 秒检查一次镜像更新"
echo -e "  - 检测到新镜像会自动拉取并重启容器"
echo -e "  - 自动清理旧镜像"
echo -e ""
echo -e "${YELLOW}提示：${NC}"
echo -e "  - 确保宿主机 Caddy 已正确配置 Docker 集成"
echo -e "  - 确保 DNS 已正确解析到服务器 IP"
echo -e "  - Caddy 会自动申请和续期 SSL 证书"
echo -e "  - 推送新 tag 后，Watchtower 会在 60 秒内自动更新"

