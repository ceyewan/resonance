# Resonance 部署指南

本目录包含 Resonance IM 系统的 Docker Compose 部署配置。

## 📁 文件说明

- `base.yaml` - 基础设施服务（MySQL、Redis、NATS、Etcd）
- `services.yaml` - 业务服务（Logic、Gateway、Task、Web）
- `Dockerfile` - 统一的多阶段构建文件

## 🚀 部署方式

### 本地开发环境

**特点**：

- 直接暴露 Gateway (8080) 和 Web (4173) 端口到 `127.0.0.1`
- 不使用 Caddy，直接访问服务
- 不暴露基础设施端口（MySQL、Redis 等）

**快速启动**：

```bash
./scripts/deploy-local.sh
```

**手动启动**：

```bash
# 1. 构建本地镜像
docker build --target final -t ceyewan/resonance:local -f deploy/Dockerfile .

# 2. 创建网络
docker network create caddy 2>/dev/null || true
docker network create resonance-net 2>/dev/null || true

# 3. 启动服务
DEPLOY_ENV=local \
RESONANCE_IMAGE=ceyewan/resonance:local \
GATEWAY_PORT_BINDING="127.0.0.1:8080:8080" \
WEB_PORT_BINDING="127.0.0.1:4173:4173" \
docker compose -f deploy/base.yaml -f deploy/services.yaml up -d
```

**访问地址**：

- Gateway API: http://127.0.0.1:8080
- Web 前端: http://127.0.0.1:4173

---

### 生产环境（使用宿主机 Caddy）

**特点**：

- 不暴露端口到宿主机
- 通过 Docker labels 让宿主机 Caddy 自动发现和反向代理
- 自动 HTTPS（Caddy 自动申请和续期证书）

**前置要求**：

1. 宿主机已安装 Caddy 并配置 Docker 集成
2. DNS 已正确解析到服务器 IP
3. 已创建 `caddy` Docker 网络

**快速部署**：

```bash
./scripts/deploy-production.sh v0.1
```

**手动部署**：

```bash
# 1. 拉取镜像
docker pull ceyewan/resonance:v0.1

# 2. 创建网络
docker network create caddy 2>/dev/null || true
docker network create resonance-net 2>/dev/null || true

# 3. 启动服务
DEPLOY_ENV=production \
RESONANCE_IMAGE=ceyewan/resonance:v0.1 \
CADDY_GATEWAY_DOMAIN="im-api.ceyewan.xyz" \
CADDY_WEB_DOMAIN="chat.ceyewan.xyz" \
GATEWAY_PORT_BINDING="" \
WEB_PORT_BINDING="" \
docker compose -f deploy/base.yaml -f deploy/services.yaml up -d
```

**访问地址**：

- Gateway API: https://im-api.ceyewan.xyz
- Web 前端: https://chat.ceyewan.xyz

---

## 🔧 环境变量说明

### 部署模式控制

| 变量                   | 说明             | 本地开发                  | 生产环境                 |
| ---------------------- | ---------------- | ------------------------- | ------------------------ |
| `DEPLOY_ENV`           | 部署环境         | `local`                   | `production`             |
| `RESONANCE_IMAGE`      | Docker 镜像      | `ceyewan/resonance:local` | `ceyewan/resonance:v0.1` |
| `GATEWAY_PORT_BINDING` | Gateway 端口绑定 | `127.0.0.1:8080:8080`     | 空（不暴露）             |
| `WEB_PORT_BINDING`     | Web 端口绑定     | `127.0.0.1:4173:4173`     | 空（不暴露）             |
| `CADDY_GATEWAY_DOMAIN` | Gateway 域名     | 空                        | `im-api.ceyewan.xyz`     |
| `CADDY_WEB_DOMAIN`     | Web 域名         | 空                        | `chat.ceyewan.xyz`       |

### 基础设施配置

所有环境变量统一使用 `RESONANCE_` 前缀：

| 变量                            | 说明            | 默认值         |
| ------------------------------- | --------------- | -------------- |
| `RESONANCE_MYSQL_ROOT_PASSWORD` | MySQL root 密码 | `root123`      |
| `RESONANCE_MYSQL_DATABASE`      | MySQL 数据库名  | `resonance`    |
| `RESONANCE_MYSQL_USER`          | MySQL 用户名    | `resonance`    |
| `RESONANCE_MYSQL_PASSWORD`      | MySQL 密码      | `resonance123` |

详见 `.env.example` 文件。

---

## 📦 镜像构建与推送

### 构建本地镜像

```bash
./scripts/build-push.sh local
```

### 构建 amd64 镜像

```bash
./scripts/build-push.sh amd64
```

### 构建并推送到 Docker Hub

```bash
./scripts/build-push.sh push 0 v0.1
```

---

## 🛠️ 常用命令

### 查看服务状态

```bash
docker compose -f deploy/base.yaml -f deploy/services.yaml ps
```

### 查看日志

```bash
# 查看所有服务日志
docker compose -f deploy/base.yaml -f deploy/services.yaml logs -f

# 查看特定服务日志
docker compose -f deploy/base.yaml -f deploy/services.yaml logs -f gateway
```

### 停止服务

```bash
docker compose -f deploy/base.yaml -f deploy/services.yaml down
```

### 重启服务

```bash
docker compose -f deploy/base.yaml -f deploy/services.yaml restart gateway
```

---

## 🔒 安全建议

1. **生产环境**：
    - 修改 `.env` 中的默认密码
    - 使用强密码（MySQL、Redis 等）
    - 定期更新镜像和依赖

2. **网络隔离**：
    - 基础设施服务（MySQL、Redis）仅在 `resonance-net` 网络中可访问
    - 仅 Gateway 和 Web 通过 Caddy 对外暴露

3. **日志管理**：
    - 所有服务已配置日志轮转（最大 5MB，保留 2 个文件）
    - 生产环境建议集成日志收集系统

---

## 📚 相关文档

- [Caddy Docker Proxy](https://github.com/lucaslorentz/caddy-docker-proxy)
- [Docker Compose 文档](https://docs.docker.com/compose/)
- [Genesis 组件库](https://github.com/ceyewan/genesis)
