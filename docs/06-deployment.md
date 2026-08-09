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
| `pilot` | Agent 控制面：Run/预算/Session/Tool Broker/Logic 回写 | 是 |
| `pilot-runtime` | 隔离 Runtime host：私有 UDS、Pi 子进程、Bridge、Tool Relay | 是 |
| `egress-proxy` | Runtime 唯一 Provider CONNECT 出口 | 是 |
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
    │      │
                      │      └──▶ pilot (depends: logic + postgres + nats + etcd + profile runtime)
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

### 4.1 消息幂等索引迁移门禁

`init` 创建 `uniq_message_client_id` 前会审计历史重复键，迁移后会验证索引唯一性、列顺序、Predicate 和 PostgreSQL 的 valid/ready 状态。任何不一致都会使 one-shot job 失败，业务服务不会在一个“看起来有索引、实际不防重”的 schema 上启动。

对已有大表，普通 AutoMigrate 建索引可能持有较重锁。生产发布应先在维护窗口执行重复键审计，再按 PostgreSQL 运维流程使用 `CREATE UNIQUE INDEX CONCURRENTLY` 预建同名索引；核对 `pg_index.indisvalid/indisready` 和 `pg_get_indexdef()` 后再运行 `init`。历史重复不得直接删除关联事实；应保留 canonical 消息，将其余旧行的 `client_msg_id` 按审计方案清空或改为唯一 legacy 值。失败的并发建索引可能留下 INVALID index，重试前必须显式检查并清理。

---

## 5. 配置来源

每个服务有一份主配置文件，环境差异通过环境变量覆盖：

```text
configs/
├── logic.yaml    # Logic 服务默认配置（本地直连 127.0.0.1）
├── gateway.yaml  # Gateway 服务默认配置
├── task.yaml     # Task 服务默认配置
├── pilot.yaml    # Pilot control、Tool Broker、Session、Budget 和 Stream 限额
├── pilot-runtime.yaml # Pi/Bridge/UDS/Relay 限额与固定版本
├── egress-proxy.yaml  # CONNECT host、DNS/TLS 和连接上限
└── web.yaml      # Web 静态服务配置
```

配置文件中的敏感字段（数据库密码、JWT 密钥、管理员密码）默认为空，通过环境变量注入：

| 环境变量 | 用途 |
| -------- | ---- |
| `RESONANCE_POSTGRES_PASSWORD` | 数据库密码 |
| `RESONANCE_AUTH_SECRET_KEY` | JWT 签发密钥 |
| `RESONANCE_GATEWAY_SERVICE_AUTH_SECRET` | Gateway → Logic 每请求服务签名密钥（不得与 JWT/Pilot 密钥复用） |
| `RESONANCE_ADMIN_PASSWORD` | 初始管理员密码 |
| `RESONANCE_PILOT_CAPABILITY_SECRET` | Pilot → Tool Bridge 短期 Capability 签名密钥 |
| `RESONANCE_PILOT_SERVICE_AUTH_ID` | user-assistant Pilot 的独立 Logic 工作负载 ID |
| `RESONANCE_PILOT_SERVICE_AUTH_SECRET` | Pilot → Logic 请求签名密钥 |
| `RESONANCE_PILOT_IAM_CAPABILITY_SECRET` | iam-admin Pilot 独立的 Capability 签名密钥 |
| `RESONANCE_PILOT_IAM_SERVICE_AUTH_ID` | iam-admin Pilot 的独立 Logic 工作负载 ID |
| `RESONANCE_PILOT_IAM_SERVICE_AUTH_SECRET` | iam-admin Pilot 独立的 Logic 请求签名密钥 |
| `RESONANCE_PILOT_TENANT_ID` / `RESONANCE_PILOT_IAM_TENANT_ID` | 两个 Pilot 工作负载各自唯一允许的 Tenant |
| `RESONANCE_USER_ASSISTANT_PROFILE_VERSION` | Logic 与 user-assistant Pilot 共同固定的版本 |
| `RESONANCE_IAM_ADMIN_PROFILE_VERSION` | Logic 与 iam-admin Pilot 共同固定的版本 |
| `DASHSCOPE_API_KEY` | 只注入 Runtime sidecar 的平台托管 API Key；control/proxy 不可见 |
| `DASHSCOPE_BASE_URL` | 百炼 OpenAI-compatible Base URL，当前固定为按量付费业务空间专属 endpoint |
| `DASHSCOPE_MODEL` | 默认模型，当前固定为 `qwen3.8-max` |

两个 Pilot 的 Capability 与 Logic service-auth 密钥不得复用。Compose 会把两个独立
service ID/secret/Tenant 同时注入 Logic 与对应 Pilot；Logic 只允许普通 Pilot 调用
Chat/History，iam-admin Pilot 才能创建 Approval 和调用 IAM Mutation。任一身份配置缺失、
复用或 tenant/profile/version 不匹配时必须 fail closed，不能退回共享密钥。

Docker 环境中，基础设施地址通过 Compose 的 `environment` 块覆盖（`RESONANCE_POSTGRES_HOST: postgres`），不需要修改配置文件。

Logic 的服务签名防重放依赖共享 Redis，键前缀由
`service_auth.nonce_key_prefix` 配置。所有 Logic 副本必须连接同一 Redis
安全域；`SET NX` 失败时请求会 fail closed。实例内存 NonceStore 只用于单元测试，
不能作为多副本生产配置。

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
docker compose --env-file .env -p resonance \
  -f deploy/base.yaml \
  -f deploy/services.yaml \
  -f deploy/services.local.yaml \
  up -d
```

这个模式会暴露 Gateway（8080）和 Web（4173）端口到 `127.0.0.1`，适合本地验证 Docker 构建产物。

注意：Docker 模式下 `logic` 和 `gateway` 不能继续使用配置文件中的 `service.host=localhost` 做服务注册。`services.yaml` 会显式注入 `RESONANCE_SERVICE_HOST=<container-hostname>`，让 Logic 注册为 `logic-service-001:15090`、Gateway 注册为 `gateway-service-001:15091`。否则其他容器通过 etcd 做服务发现时会把 `localhost` 解析到自身容器，最终出现 `dial tcp [::1]:15090: connect: connection refused` 这类错误。

本地 Docker 模式下，`services.yaml` 会为 `web` 容器默认注入：

```bash
RESONANCE_WEB_API_BASE_URL=http://localhost:8080
RESONANCE_WEB_WS_BASE_URL=ws://localhost:8080/ws
```

这样浏览器访问 `http://localhost:4173` 时，前端请求会直接命中宿主机暴露的 Gateway，而不会把 `/resonance.gateway.v1.*` 或 `/ws` 误发到静态 Web 服务自身。

本地修复代码后需要把结果应用到 Docker 环境时，直接执行：

```bash
make update-local
```

该命令会重新构建镜像、按 `deploy/base.yaml + deploy/services.yaml + deploy/services.local.yaml`
重建业务容器，并输出最新服务状态。RFC 2544 合成地址兼容只存在于这个 local override；
基础和生产配置均默认关闭。

### 7.2 生产模式

```bash
make up-prod
```

等价于：

```bash
docker compose --env-file .env -p resonance \
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
RESONANCE_GATEWAY_SERVICE_AUTH_SECRET=<distinct-at-least-32-random-bytes>
RESONANCE_POSTGRES_PASSWORD=<strong-password>
RESONANCE_ADMIN_PASSWORD=<admin-password>
RESONANCE_PILOT_CAPABILITY_SECRET=<at-least-32-random-bytes>
RESONANCE_PILOT_SERVICE_AUTH_SECRET=<at-least-32-random-bytes>
DASHSCOPE_API_KEY=<server-provider-key>
DASHSCOPE_BASE_URL=https://llm-3rwbpx52jtt7759p.cn-beijing.maas.aliyuncs.com/compatible-mode/v1
DASHSCOPE_MODEL=qwen3.8-max
RESONANCE_PILOT_IMAGE=registry/resonance-pilot@sha256:<64-lowercase-hex>
RESONANCE_PILOT_RUNTIME_IMAGE=registry/resonance-pilot-runtime@sha256:<64-lowercase-hex>
```

Pilot 使用两个兼容镜像：`pilot-control-final` 只有 Go control，不含 Node/Pi/Provider Key；`pilot-runtime-final` 固定 Pi `0.84.1`、Node 22 和可信 Bridge，不含 PostgreSQL/NATS/Etcd/Logic 凭证。两者均以 uid 10001 非 root 运行，rootfs 只读、移除全部 Linux capabilities、启用 `no-new-privileges`，并限制 CPU、内存、PID 和 `/tmp`。只共享 profile-specific Session/socket volume，不得挂 Docker Socket、宿主 Home 或 `~/.pi`。

生产脚本拒绝 Agent 镜像的 tag、空值或非法 digest。GitHub 发布工作流会将 control/runtime 两个 digest、Pi/Bridge/Remote Runtime 协议版本和源码 SHA 写入同一个 `agent-release-<tag>` artifact，同时为两个镜像发布 SBOM 和 provenance attestation。发布和回滚必须把该 digest 对当作不可拆分的组合。`deploy/scripts/rollback-agent.sh` 默认只校验，仅显式 `--execute` 才会停止 ingress、依次恢复 Runtime/control 并生成操作证据。

Control 只加入 `resonance-net`，Runtime 只加入 `runtime-internal`。Runtime 无默认业务网络出口，只能经 `provider-egress-proxy` 的精确 CONNECT allowlist 访问 `llm-3rwbpx52jtt7759p.cn-beijing.maas.aliyuncs.com:443`；Tool Bridge 经 loopback Relay 和私有 UDS 回到 control。Compose 的 stop grace 必须覆盖 control 停止摄入、Run drain、Remote Shutdown 和 Pi Abort/Kill。Docker Desktop/VPN 可能把公网域名解析到 RFC 2544 合成地址，本地 Compose 对该地址段有显式兼容；生产覆盖强制关闭此兼容，继续只接受真实公网地址。

---

## 8. 端口规划

| 服务 | 端口 | 协议 | 用途 |
| ---- | ---- | ---- | ---- |
| Gateway | 8080 | HTTP/WS | 客户端接入（ConnectRPC + WebSocket） |
| Gateway | 15091 | gRPC | Task → Gateway Push 内部调用 |
| Logic | 15090 | gRPC | Gateway → Logic 内部调用 |
| Logic | 9091 | HTTP | Prometheus metrics |
| Gateway | 9092 | HTTP | Prometheus metrics |
| Pilot | 15093 | HTTP | health/readiness |
| Pilot Runtime | 15095 | HTTP loopback only | sidecar health/readiness |
| Runtime Tool Relay | 15094 | HTTP loopback only | Bridge → private Tool Broker UDS |
| Provider Egress Proxy | 18080 | HTTP CONNECT（仅容器网络） | Runtime → Provider TLS tunnel |
| Runtime/Tool Broker | `/run/resonance-agent/*.sock` | Unix socket | control ↔ Runtime/Tool Broker 私有协议 |
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
