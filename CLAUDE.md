# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

**全程使用中文交流**。本仓库是 Resonance IM 系统,**专注于 IM 业务逻辑实现**,通过合理使用 [Genesis](github.com/ceyewan/genesis) 组件库解决基础架构问题。

---

## 架构状态：Phase 0–8 已完成，Phase 9 生产验证中

事件驱动 IM 骨架、撤回/编辑/已读、Go Pilot + Pi Runtime、tenant-scoped AI Session、IAM 审批/Mutation 和 Agent Stream 已落地。当前重点是固定 Runtime 发布、业务 Eval 和多实例生产验收；一手依据见 `docs/14-ai-service.md`、`docs/15-agent-harness.md`、`docs/16-agent-operations.md` 与 `DEVELOPMENT.md`。

设计决策的**一手依据**已迁移到 `docs/`：

| 文档 | 用途 |
|------|------|
| `docs/00-overview.md` | 架构总览、ChatEvent 核心抽象、服务职责与通信方式 |
| `docs/01-protocol.md` | Proto 分层、契约边界、身份传递与错误处理约定 |
| `docs/02-database.md` | 表结构、索引策略、事务边界、Redis 键设计 |
| `docs/10-gateway.md` | Gateway 职责、连接管理、Push 落点 |
| `docs/11-logic.md` | Logic 业务规则、Outbox 一致性 |
| `docs/12-task.md` | Task 写扩散、Inbox 语义、消费者处理 |
| `docs/20-message-flow.md` | 发消息主链路时序 |
| `docs/21-write-fanout.md` | 写扩散模型与一致性策略 |
| `docs/22-recall-edit-read.md` | 撤回、编辑、已读事件的统一处理模式（Phase 5–6 依据） |

**动手前必读 `docs/00-overview.md`**。功能设计先对照 `docs/01-protocol.md` 看是否已有模式可复用，Phase 5+ 具体任务清单见 `DEVELOPMENT.md`。

改完代码要同步更新 `docs/` 对应章节，并在 `DEVELOPMENT.md` 对应任务上打勾。

---

## 架构速览

```
Web ──HTTP/ConnectRPC──▶ Gateway ──gRPC──▶ Logic ──MQ(NATS)──▶ Task
 ▲         WebSocket        │                │                    │
 │                          │                └── PostgreSQL/Redis │
 └────────── WS ◀──── gRPC Push ◀────────────────────────────────┘
```

| 服务 | 一句话职责 | 不负责 |
|------|-----------|--------|
| **Gateway** | 协议转换 + 连接管理 + 鉴权中间件 | 业务规则、持久化 |
| **Logic** | 业务规则 + 事件生成 + Outbox 事务一致性 | 推送、写扩散 |
| **Task** | 消费 MQ,按事件类型落地 + 推送到 Gateway | 业务判断 |
| **Pilot** | Go 控制面调度隔离的 Pi Runtime，以 Actor 权限调用 Go Tool Broker，并以 Bot 身份回复 | 连接管理、Agent Loop、绕过 Logic 写主事实 |

### 核心抽象:ChatEvent

所有"会话内用户可感知事件"的统一载体,`oneof payload` 承载 Message / Recall / Edit / ReadReceipt / SessionUpdate / ...。新功能几乎都是加一个 oneof 分支,不改 WS 协议、Push RPC、Inbox 表结构。

### 身份传递约定

- Web → Gateway:`Authorization: Bearer <jwt>` Header
- Gateway → Logic：Gateway workload 对 method + payload hash + tenant + actor + membership version + timestamp + nonce 签名；Logic 验签后从 IAM 权威回查 Principal。生产拒绝 `x-username`、内部 Bearer 和 body 身份字段
- **业务 body 里永远不带 `username` / `access_token`**

### 错误处理约定

失败用 `status.Errorf(codes.X, ...)` 返回 gRPC status,**响应 message 里不放 `string error` 字段**。

---

## 技术栈

- **后端**:Go 1.26+、[Genesis v1.0.0-rc.1](https://github.com/ceyewan/genesis/releases/tag/v1.0.0-rc.1)（已发布 Go module，禁止本地 `replace`）、gRPC、NATS
- **存储**:PostgreSQL 17(消息/用户/会话)、Redis(路由映射、缓存)
- **前端**:React 18 + TypeScript + Vite + Zustand + ConnectRPC + Dexie(IndexedDB)
- **协议**:Protobuf(`api/proto/`),`make gen` 生成 Go + TS 代码

---

## Genesis 组件使用指南

| 层级 | 组件 | 用途 | 状态 |
|------|------|------|------|
| L0 | `clog` / `config` / `xerrors` | 日志(slog)、配置、错误 | ✅ 必要 |
| L0 | `metrics` | OpenTelemetry 指标 | 按需 |
| L1 | `connector` / `db` | MySQL/PG/Redis/NATS 连接器、GORM 封装 | ✅ 必要 |
| L2 | `idgen` | Snowflake / UUID / Sequence | 推荐 |
| L2 | `mq` / `cache` | NATS Publisher、Redis 统一缓存接口 | 推荐 |
| L2 | `dlock` / `idempotency` | 分布式锁、幂等 | 按需 |
| L3 | `auth` | JWT 签发与验证 | 推荐 |
| L3 | `ratelimit` / `breaker` / `registry` | 限流、熔断、Etcd 注册 | 按需 |

查看文档:

```bash
go doc -all github.com/ceyewan/genesis/connector   # 组件文档
cat genesis/connector/redis.go                      # 本地源码
cat logic/logic.go                                  # 项目中的资源组装范例
```

**初始化模式**:显式依赖注入,由 `logic/logic.go`、`gateway/gateway.go`、`task/task.go` 这类入口统一组装。业务层只接受 `repo.*Repo` 接口,**不接受 `connector.*` 底层类型**(见八荣八耻第 2 条)。

---

## 常用命令

### 后端

```bash
make gen              # 生成 Protobuf 代码(Go + TS)
make tidy             # 整理 Go 依赖

# 本地开发
make dev              # 一键起 logic/gateway/task/web（先执行 make up-infra）
go run main.go -module logic    # 手动起单个服务：logic/gateway/task/init

# 质量守门
make format           # 一键格式化 (Go/Proto/Prettier/Markdown)
make lint             # 一键静态检查 (golangci-lint + buf + prettier + markdown + web)
make lint-security    # govulncheck 漏洞扫描（按需）
make test             # go test ./...

# 基础设施与部署
make up-infra         # 启动 PostgreSQL/Redis/NATS/etcd
make init             # GORM AutoMigrate + 种子数据,幂等
make up               # 本地构建镜像 + Compose 起所有服务
make up-prod          # 生产配置（Caddy 反代，profile=production）
```

### 测试

```bash
go test ./repo/...                                 # 全量(依赖 testcontainers,需可用的 Docker daemon)
go test ./repo/ -run TestUserRepo_GetByUsername    # 单个测试
go test -race ./...                                # 启用数据竞争检测
```

`repo/` 层测试会通过 testcontainers 自动拉起 `postgres:17-alpine` + `redis:7.2-alpine`。Docker 不可用时测试会跳过或失败并给出明确原因。

### 前端(`web/` 目录)

```bash
cd web
npm install
npm run dev           # Vite 开发服务器(http://localhost:5173)
npm run build
npm run type-check
```

---

## 业务开发规范

### 1. 面向业务接口,隐藏基础组件

```go
// ✅ 业务层只认 repo 接口
type ChatService struct { msgRepo repo.MessageRepo }

// ❌ 不要把 connector.RedisConnector 直接暴露到 service 层
```

### 2. 资源生命周期

服务入口(`logic.go` / `gateway.go` / `task.go`)集中初始化与关闭,**关闭顺序与创建相反**,使用 `defer` 或显式 `Close()`。

### 3. 协议优先

新功能涉及"用户可感知的会话事件"时，**优先考虑扩展 `ChatEvent.payload`** 而非新增 RPC。具体落点见 `docs/01-protocol.md`；Phase 5+ 功能时序见 `docs/22-recall-edit-read.md`。

### 4. 事务与可靠性

业务变更 + 事件发布必须走 Outbox 模式(同一 DB 事务内写业务表 + `t_message_outbox`),事务外异步投 MQ,失败由 `logic/job/outbox.go` 兜底补偿。参考 `logic/service/chat.go` 现有实现。

---

## Git 工作流

按全局 `~/.claude/CLAUDE.md` 约定:分支 `<type>/<description>`、提交 `<type>(<scope>): <subject>` 中文祈使语气。

本项目常用 scope:`logic` / `gateway` / `task` / `web` / `api` / `docs` / `architecture`。

提交前必须通过 `make format && make lint`。

---

## IM 开发八荣八耻

1. 以过度设计为耻,以业务实现为荣
2. 以基础组件暴露为耻,以业务封装为荣
3. 以盲目复制为耻,以场景适配为荣
4. 以单点瓶颈为耻,以可扩展性为荣
5. 以消息丢失为耻,以可靠性保证为荣
6. 以连接泄漏为耻,以资源管理为荣
7. 以忽略性能为耻,以性能监控为荣
8. 以数据不一致为耻,以一致性保证为荣
