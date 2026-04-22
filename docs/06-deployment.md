# 部署与环境设计

> 本文档描述 Resonance 的部署拓扑、配置来源、服务依赖链和本地/生产环境的差异。阅读完本文后，应该能回答三个问题：系统如何通过单一入口按模块启动；本地开发和生产部署的配置差异在哪里；以及 `init` 模块在启动链路中扮演什么角色。

---

## 1. 部署目标

当前部署设计服务于"单人可维护、本地与生产心智统一"的目标。系统不追求复杂的多环境矩阵，而是用一套主配置加环境变量覆盖的方式处理环境差异，让维护者只需要记住一套心智模型。

核心原则是：默认可用、容易理解、出问题容易排查。

---

## 2. 运行时模块

系统通过 `main.go` 的 `-module` 参数启动不同角色，所有模块共享同一份构建产物：

| 模块 | 职责 | 是否常驻 |
| ---- | ---- | -------- |
| `init` | AutoMigrate 建表 + 种子数据初始化 | 否，one-shot job |
| `gateway` | 接入层：HTTP/ConnectRPC、WebSocket、Push 接收 | 是 |
| `logic` | 业务层：gRPC 服务、事务、Outbox | 是 |
| `task` | 异步层：MQ 消费、写扩散、在线推送 | 是 |
| `web` | 静态资源托管（生产构建产物） | 是 |

`init` 是一次性初始化任务，不是长期服务。它负责 GORM AutoMigrate（建表/更新表结构）和种子数据（默认管理员账号、默认会话）。在 Docker Compose 中，它被编排为 one-shot job，依赖数据库健康后执行，业务服务依赖它成功完成后再启动。

---

## 3. 基础设施依赖

系统依赖四个基础设施组件：

| 组件 | 版本 | 用途 | 默认端口 |
| ---- | ---- | ---- | -------- |
| PostgreSQL | 17-alpine | 主事实存储（消息、会话、Inbox、Outbox） | 5432 |
| Redis | 7.2-alpine | 在线路由、序列号计数器、WorkerID 分配 | 6379 |
| NATS | 2.10-alpine | Logic → Task 异步事件投递（JetStream） | 4222 |
| etcd | v3.5.12 | 服务注册与发现 | 2379 |

这四个组件的配置定义在 `deploy/base.yaml`，本地开发和生产环境都使用同一份基础设施配置。

---

## 4. 服务依赖链

```text
postgres (healthy)
    │
    ▼
init (completed successfully)
    │
    ├──▶ logic (depends: init + postgres + redis + nats + etcd)
    │
    └──▶ task  (depends: init + postgres + redis + nats + etcd)
              │
              ▼
         gateway (depends: redis + etcd)
              │
              ▼
           web (depends: gateway 可访问)
```

`logic` 和 `task` 都依赖 `init` 成功完成，确保表结构和种子数据在业务服务启动前已经就绪。`gateway` 不直接依赖数据库，只依赖 Redis（在线路由）和 etcd（服务发现），因此可以在 `logic` 启动后独立运行。

---

## 5. 配置来源

每个服务有一份主配置文件，环境差异通过环境变量覆盖：

```text
configs/
├── logic.yaml    # Logic 服务默认配置（本地直连 127.0.0.1）
├── gateway.yaml  # Gateway 服务默认配置
├── task.yaml     # Task 服务默认配置
└── web.yaml      # Web 静态服务配置
```

配置文件中的敏感字段（数据库密码、JWT 密钥、管理员密码）默认为空，通过环境变量注入：

| 环境变量 | 用途 |
| -------- | ---- |
| `RESONANCE_POSTGRES_PASSWORD` | 数据库密码 |
| `RESONANCE_AUTH_SECRET_KEY` | JWT 签发密钥 |
| `RESONANCE_ADMIN_PASSWORD` | 初始管理员密码 |

Docker 环境中，基础设施地址通过 Compose 的 `environment` 块覆盖（`RESONANCE_POSTGRES_HOST: postgres`），不需要修改配置文件。

---

## 6. 本地开发

本地开发推荐使用 `make dev`，直接运行 Go 程序，连接本地基础设施：

```bash
# 第一步：启动基础设施
make up-infra

# 第二步（首次或表结构变更后）：初始化数据库
make init

# 第三步：启动所有业务服务
make dev
```

`make dev` 会并发启动 logic、task、gateway 和 web 开发服务器，Ctrl+C 统一停止。本地配置文件中的地址默认为 `127.0.0.1`，与 Docker 环境中的 hostname 不同，这是唯一需要注意的差异。

---

## 7. Docker 部署

### 7.1 本地 Docker 模式

```bash
make up
```

等价于：

```bash
docker compose -p resonance \
  -f deploy/base.yaml \
  -f deploy/services.yaml \
  up -d
```

这个模式会暴露 Gateway（8080）和 Web（4173）端口到 `127.0.0.1`，适合本地验证 Docker 构建产物。

### 7.2 生产模式

```bash
make up-prod
```

等价于：

```bash
docker compose -p resonance \
  -f deploy/base.yaml \
  -f deploy/services.yaml \
  -f deploy/services.prod.yaml \
  --profile production \
  up -d
```

生产模式通过 `services.prod.yaml` 覆盖以下配置：

- Gateway 和 Web 不对宿主机暴露端口（由宿主机 Caddy 反向代理）
- 注入 Caddy 网络和反代 labels
- 注入 Web 运行时 API/WS 地址（`RESONANCE_WEB_API_BASE_URL`、`RESONANCE_WEB_WS_BASE_URL`）
- 启用 Watchtower 自动更新（`production` profile）

生产环境需要在 `.env` 中配置域名和密钥：

```bash
CADDY_GATEWAY_DOMAIN=api.example.com
CADDY_WEB_DOMAIN=app.example.com
RESONANCE_AUTH_SECRET_KEY=<strong-secret>
RESONANCE_POSTGRES_PASSWORD=<strong-password>
RESONANCE_ADMIN_PASSWORD=<admin-password>
```

---

## 8. 端口规划

| 服务 | 端口 | 协议 | 用途 |
| ---- | ---- | ---- | ---- |
| Gateway | 8080 | HTTP/WS | 客户端接入（ConnectRPC + WebSocket） |
| Gateway | 15091 | gRPC | Task → Gateway Push 内部调用 |
| Logic | 15090 | gRPC | Gateway → Logic 内部调用 |
| Logic | 9091 | HTTP | Prometheus metrics |
| Gateway | 9092 | HTTP | Prometheus metrics |
| PostgreSQL | 5432 | TCP | 数据库连接 |
| Redis | 6379 | TCP | 缓存/路由 |
| NATS | 4222 | TCP | 消息队列 |
| NATS | 8222 | HTTP | NATS 监控 |
| etcd | 2379 | TCP | 服务发现 |

---

## 9. 当前实现结构

部署相关文件的主要落点：

- `deploy/base.yaml`：基础设施（PostgreSQL、Redis、NATS、etcd）
- `deploy/services.yaml`：业务服务编排（init、logic、task、gateway、web）
- `deploy/services.prod.yaml`：生产环境覆盖（Caddy 反代、端口关闭、Watchtower）
- `deploy/Dockerfile`：多阶段构建，最终产物为单一二进制
- `configs/*.yaml`：各服务默认配置（本地直连地址）
- `Makefile`：所有常用操作的入口

---

## 10. 阅读建议

| 文档 | 内容 |
| ---- | ---- |
| `00-overview.md` | 服务职责与整体架构 |
| `07-cicd-and-quality.md` | 构建门禁与质量要求 |
| `40-developer-onboarding.md` | 新开发者本地启动指南 |

---

## 11. 小结

Resonance 的部署设计以"单一入口、按模块启动、环境变量覆盖差异"为核心。本地开发和生产部署共享同一套配置骨架，差异只在基础设施地址和域名配置上。`init` 模块作为 one-shot job 编排在启动链路中，业务服务依赖它成功完成后再启动，保证表结构和种子数据始终就绪。
