# Resonance 部署指南

本目录包含 Resonance IM 系统的 Docker Compose 部署配置与部署说明。

## 文件索引

- `base.yaml`：基础设施服务（PostgreSQL、Redis、NATS、Etcd）
- `services.yaml`：业务服务（Logic、Gateway、Task、Web、Watchtower）
- `Dockerfile`：统一多阶段构建文件

## 当前支持的部署方式

### 1. 本地全 Docker（推荐）

特点：

- 全部服务运行在容器中
- Gateway 暴露到 `127.0.0.1:8080`
- Web 暴露到 `127.0.0.1:4173`

命令：

```bash
# 方式 1：Makefile
make up
make logs
make down

# 方式 2：脚本
./scripts/deploy-local.sh
```

访问地址：

- Web: `http://localhost:4173`
- Gateway: `http://localhost:8080`

关键点：

- `.env` 中需设置 `RESONANCE_ENV=prod`（使用 Docker hostname）
- Docker 网络中的 PostgreSQL 主机名为 `postgres`

### 2. 本地混合模式（业务进程本地 + 基础设施 Docker）

特点：

- 基础设施（PostgreSQL/Redis/NATS/Etcd）用 Docker
- Logic/Task/Gateway/Web 本机运行

命令：

```bash
# 1) 起基础设施
make up

# 2) 本地运行业务服务
make dev
```

访问地址：

- Web: `http://localhost:5173`
- Gateway: `http://localhost:8080`

关键点：

- `make dev` 会强制注入 `RESONANCE_ENV=dev`
- 本地配置连接 `127.0.0.1:5432`（PostgreSQL）

### 3. 生产环境部署（Caddy 反向代理）

前置条件：

- 服务器已安装 Docker / Docker Compose
- 已安装并配置 Caddy Docker Proxy
- DNS 已解析到服务器 IP
- Docker 网络 `caddy` 已存在

命令：

```bash
# 指定 tag
./scripts/deploy-production.sh v0.1

# 或 latest
./scripts/deploy-production.sh latest
```

访问地址（示例）：

- Gateway: `https://im-api.ceyewan.xyz`
- Web: `https://chat.ceyewan.xyz`

关键点：

- 生产脚本会带 `--profile production`，启用 Watchtower
- 脚本参数 tag 会覆盖 `.env` 里的 `RESONANCE_IMAGE`

## 环境变量说明

### 核心部署变量

| 变量 | 说明 | 示例 |
| --- | --- | --- |
| `RESONANCE_ENV` | 配置环境（dev/prod） | `prod` |
| `RESONANCE_IMAGE` | 业务镜像 | `ceyewan/resonance:v0.1` |
| `GATEWAY_PORT_BINDING` | Gateway 端口映射 | `127.0.0.1:8080:8080` |
| `WEB_PORT_BINDING` | Web 端口映射 | `127.0.0.1:4173:4173` |
| `CADDY_GATEWAY_DOMAIN` | Gateway 域名 | `im-api.ceyewan.xyz` |
| `CADDY_WEB_DOMAIN` | Web 域名 | `chat.ceyewan.xyz` |

### PostgreSQL 变量

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `RESONANCE_POSTGRES_DATABASE` | 数据库名 | `resonance` |
| `RESONANCE_POSTGRES_USER` | 用户名 | `resonance` |
| `RESONANCE_POSTGRES_PASSWORD` | 密码 | `resonance123` |

### 配置加载顺序

Genesis Config 加载顺序：

`环境变量 > .env > configs/{service}.prod.yaml > configs/{service}.yaml`

环境差异：

| RESONANCE_ENV | 数据库连接地址 | 用途 |
| --- | --- | --- |
| 留空或 `dev` | `127.0.0.1:5432` | 本地业务进程直跑（`make dev`） |
| `prod` | `postgres:5432` | Docker 环境（`make up` / 生产） |

## 镜像构建与发布

```bash
# 本地镜像
./scripts/build-push.sh local

# 构建 amd64
./scripts/build-push.sh amd64

# 构建并推送
./scripts/build-push.sh push 0 v0.1
```

## 常用运维命令

```bash
# 状态
docker compose -p resonance -f deploy/base.yaml -f deploy/services.yaml ps

# 日志
docker compose -p resonance -f deploy/base.yaml -f deploy/services.yaml logs -f

# 关闭
docker compose -p resonance -f deploy/base.yaml -f deploy/services.yaml down

# 重启单服务
docker compose -p resonance -f deploy/base.yaml -f deploy/services.yaml restart gateway

# 清理卷并重建
make clean && make up
```

## 生产检查清单

- [ ] 修改 `RESONANCE_POSTGRES_PASSWORD`
- [ ] 修改 `RESONANCE_AUTH_SECRET_KEY`（至少 32 字符）
- [ ] 修改 `RESONANCE_ADMIN_PASSWORD`
- [ ] 设置 `RESONANCE_ENV=prod`
- [ ] 设置 `CADDY_GATEWAY_DOMAIN` / `CADDY_WEB_DOMAIN`

## 故障排查

1. 服务起不来：先看 `make logs`，再看单服务日志。
2. 本地混合模式连不上数据库：确认 PostgreSQL 端口 `127.0.0.1:5432` 已监听。
3. 生产无自动更新：确认是通过 `scripts/deploy-production.sh` 启动（含 `--profile production`）。

## 相关文档

- [Docker Compose 文档](https://docs.docker.com/compose/)
- [Caddy Docker Proxy](https://github.com/lucaslorentz/caddy-docker-proxy)
- [Genesis 组件库](https://github.com/ceyewan/genesis)
