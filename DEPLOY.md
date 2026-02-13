# Resonance 部署指南

## 快速开始

### 1. 配置环境变量

```bash
# 复制配置文件
cp .env.example .env

# 编辑配置
vim .env
```

**重要配置项**：

- `RESONANCE_ENV=prod` - **Docker 环境必须设置为 prod**（连接 Docker hostname）
- `RESONANCE_MYSQL_PASSWORD` - MySQL 密码
- `RESONANCE_AUTH_SECRET_KEY` - JWT 密钥（至少 32 字符）

**注意**：`.env.example` 中已默认设置 `RESONANCE_ENV=prod`，适合 Docker 环境直接使用。

---

### 2. 本地开发（Docker）

```bash
# 方式 1：使用 Makefile
make up          # 启动所有服务
make logs        # 查看日志
make down        # 停止服务

# 方式 2：使用脚本
./scripts/deploy-local.sh
```

访问地址：

- Web: http://localhost:4173
- Gateway: http://localhost:8080

**关键点**：

- ✅ `.env` 中必须设置 `RESONANCE_ENV=prod`
- ✅ 服务会连接 Docker hostname（mysql、redis、nats、etcd）
- ❌ 如果 `RESONANCE_ENV` 为空或 `dev`，服务会尝试连接 `127.0.0.1` 导致失败

---

### 3. 本地开发（不用 Docker）

```bash
# 1. 修改 .env（可选）
vim .env
# 设置 RESONANCE_ENV=  （留空或 dev，连接 127.0.0.1）

# 2. 启动基础设施（MySQL、Redis 等）
make up

# 3. 启动业务服务（Go + Node.js）
make dev
```

访问地址：

- Web: http://localhost:5173
- Gateway: http://localhost:8080

**关键点**：

- ✅ `.env` 中 `RESONANCE_ENV` 留空或设置为 `dev`
- ✅ 服务会连接 `127.0.0.1`（本地 Docker 暴露的端口）
- ⚠️ 需要先 `make up` 启动基础设施，再 `make dev` 启动业务服务

---

### 4. 生产环境部署

#### 前置条件

- 服务器已安装 Docker 和 Docker Compose
- 已安装并配置 Caddy（用于反向代理和 HTTPS）
- DNS 已解析到服务器 IP

#### 部署步骤

```bash
# 1. 克隆仓库
git clone https://github.com/ceyewan/resonance.git
cd resonance

# 2. 配置环境变量
cp .env.example .env
vim .env

# 修改以下配置：
# RESONANCE_ENV=prod                       # 启用生产环境配置
# RESONANCE_IMAGE=ceyewan/resonance:v0.1   # 指定镜像 tag
# GATEWAY_PORT_BINDING=                    # 留空，不暴露端口
# WEB_PORT_BINDING=                        # 留空，不暴露端口
# CADDY_GATEWAY_DOMAIN=im-api.ceyewan.xyz
# CADDY_WEB_DOMAIN=chat.ceyewan.xyz
# RESONANCE_MYSQL_ROOT_PASSWORD=强密码
# RESONANCE_MYSQL_PASSWORD=强密码
# RESONANCE_AUTH_SECRET_KEY=强密钥至少32字符

# 3. 部署（指定 tag）
./scripts/deploy-production.sh v0.1
# 或使用 latest
./scripts/deploy-production.sh latest
```

访问地址：

- Gateway: https://im-api.ceyewan.xyz
- Web: https://chat.ceyewan.xyz

---

## 自动更新（Watchtower）

生产环境已集成 Watchtower，会自动检测镜像更新并重启容器。

**工作流程**：

1. 本地打 tag 并推送：`git tag v0.2 && git push origin v0.2`
2. GitHub Actions 自动构建并推送镜像到 Docker Hub（tag: v0.2 和 latest）
3. Watchtower 在 60 秒内检测到新镜像并自动更新容器

**完全自动化，无需手动操作！**

**注意**：

- 脚本参数 `./scripts/deploy-production.sh v0.1` 中的 tag 会覆盖 `.env` 中的 `RESONANCE_IMAGE`
- 建议在 `.env` 中设置默认 tag，脚本参数用于临时切换版本

---

## 常用命令

```bash
# 代码生成
make gen         # 生成 protobuf 代码
make tidy        # 整理 Go 依赖

# 本地开发
make dev         # 启动本地开发环境（不用 Docker）
make up          # 启动 Docker 环境
make down        # 停止服务
make logs        # 查看日志
make clean       # 清理所有数据

# 查看帮助
make help
```

---

## 配置说明

### 配置加载机制（Genesis Config）

**加载顺序**：`环境变量 > .env > configs/{service}.prod.yaml > configs/{service}.yaml`

**配置文件设计**：

- `{service}.yaml` - **本地直接运行配置**（127.0.0.1，console 日志，debug 级别）
- `{service}.prod.yaml` - **Docker 环境覆盖配置**（Docker hostname，JSON 日志，info 级别）

**环境切换**：通过 `.env` 中的 `RESONANCE_ENV` 控制加载哪个配置

| RESONANCE_ENV | 加载的配置文件                           | 连接地址        | 日志格式 | 用途                                |
| ------------- | ---------------------------------------- | --------------- | -------- | ----------------------------------- |
| 留空 或 `dev` | `{service}.yaml`                         | 127.0.0.1       | console  | 本地直接运行（`make dev`）          |
| `prod`        | `{service}.prod.yaml` + `{service}.yaml` | Docker hostname | JSON     | Docker 环境（`make up` / 生产环境） |

**使用步骤**：

1. **Docker 环境**（`make up`）：

    ```bash
    # .env 中设置
    RESONANCE_ENV=prod

    # 启动
    make up
    # 加载：环境变量 > .env > logic.prod.yaml > logic.yaml
    # 连接：mysql:3306, redis:6379
    ```

2. **本地直接运行**（`make dev`）：

    ```bash
    # .env 中设置（可选）
    RESONANCE_ENV=

    # 启动
    make dev
    # 加载：环境变量 > .env > logic.yaml
    # 连接：127.0.0.1:3306, 127.0.0.1:6379
    ```

### 配置文件层级

1. **`.env`** - 敏感信息（密码、密钥）+ Docker 部署配置
2. **`configs/*.yaml`** - 本地直接运行配置（127.0.0.1，console 日志，debug 级别）
3. **`configs/*.prod.yaml`** - Docker 环境覆盖（Docker hostname，JSON 日志，info 级别）

### 环境变量命名规范

所有环境变量统一使用 `RESONANCE_` 前缀：

- `RESONANCE_ENV` - 环境模式（dev/prod）
- `RESONANCE_MYSQL_*` - MySQL 配置
- `RESONANCE_REDIS_*` - Redis 配置
- `RESONANCE_NATS_*` - NATS 配置
- `RESONANCE_ETCD_*` - Etcd 配置
- `RESONANCE_AUTH_*` - JWT 认证配置
- `RESONANCE_*_HOSTNAME` - 服务主机名

详见 `.env.example`。

### 敏感信息管理

**原则**：不在 `configs/*.yaml` 中硬编码密码和密钥

**实践**：

- MySQL 密码：`RESONANCE_MYSQL_PASSWORD`
- JWT 密钥：`RESONANCE_AUTH_SECRET_KEY`
- 其他密码：统一在 `.env` 中配置

**生产环境检查清单**：

- [ ] 修改 `RESONANCE_MYSQL_ROOT_PASSWORD`
- [ ] 修改 `RESONANCE_MYSQL_PASSWORD`
- [ ] 修改 `RESONANCE_AUTH_SECRET_KEY`（至少 32 字符）
- [ ] 设置 `RESONANCE_ENV=prod`
- [ ] 配置 Caddy 域名

---

## 故障排查

### 查看日志

```bash
# 所有服务
make logs

# 特定服务
docker compose -p resonance logs -f gateway
```

### 重启服务

```bash
docker compose -p resonance restart gateway
```

### 清理并重新部署

```bash
make clean
make up
```

---

## 配置文件对比

| 配置项     | 本地直接运行 (`*.yaml`) | Docker 环境 (`*.prod.yaml`) |
| ---------- | ----------------------- | --------------------------- |
| 使用场景   | `make dev`              | `make up` / 生产环境        |
| 日志格式   | console                 | JSON                        |
| 日志级别   | debug                   | info                        |
| MySQL 主机 | 127.0.0.1               | mysql                       |
| Redis 地址 | 127.0.0.1:6379          | redis:6379                  |
| NATS URL   | nats://127.0.0.1:4222   | nats://nats:4222            |
| Etcd 端点  | 127.0.0.1:2379          | etcd:2379                   |

**关键点**：

- `make dev` - 本地直接运行，连接 `127.0.0.1`（需要先 `make up` 启动基础设施）
- `make up` - Docker 环境，自动使用 `RESONANCE_ENV=prod`，连接 Docker hostname

所有敏感信息（密码、密钥）都从 `.env` 读取，不在配置文件中硬编码！🎉

---

## 目录结构

```
resonance/
├── .env.example          # 环境变量模板
├── Makefile              # 任务编排
├── deploy/
│   ├── base.yaml         # 基础设施（MySQL、Redis 等）
│   ├── services.yaml     # 业务服务
│   └── Dockerfile        # 统一镜像
└── scripts/
    ├── deploy-local.sh       # 本地部署脚本
    ├── deploy-production.sh  # 生产部署脚本
    └── build-push.sh         # 镜像构建脚本
```
