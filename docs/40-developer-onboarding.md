# 开发者快速上手

> 本文档面向第一次进入 Resonance 仓库的开发者，说明本地启动、常用命令、调试入口和常见问题。阅读完本文后，应该能在本地跑起一套完整的开发环境，并知道遇到问题时先看哪里。

---

## 1. 你需要先理解什么

Resonance 不是单体应用，而是一个分层的 IM 系统：Gateway 负责接入和连接，Logic 负责业务规则与事务，Task 负责异步扩散和推送，Web 是前端客户端。第一次进入项目时，建议先读：

1. `00-overview.md` — 建立整体心智
2. `01-protocol.md` — 理解 `ChatEvent` 和边界协议
3. `10-gateway.md` / `11-logic.md` / `12-task.md` — 理解三层职责分工

只要先把这四篇读完，后面看代码会顺很多。

---

## 2. 环境要求

本地开发需要以下依赖：

| 工具 | 建议版本 | 用途 |
| ---- | -------- | ---- |
| Go | 1.26+ | 后端开发 |
| Node.js | 20+ | 前端与 repo 级工具 |
| Docker | 最新稳定版 | Testcontainers、基础设施容器 |
| npm | 跟随 Node | 前端依赖安装 |

---

## 3. 第一次启动

### 3.1 安装依赖

```bash
# 后端依赖
make tidy

# repo 级前端工具（Prettier、markdownlint）
cd tools && npm ci

# Web 前端依赖
cd ../web && npm install
```

### 3.2 启动基础设施

```bash
make up-infra
```

这会启动 PostgreSQL、Redis、NATS 和 etcd。第一次启动或表结构变化后，需要初始化数据库：

```bash
make init
```

`make init` 会执行 `go run main.go -module init`，自动建表并写入种子数据（默认管理员等）。这一步是幂等的，可以重复执行。

### 3.3 启动所有业务服务

```bash
make dev
```

`make dev` 会依次启动：

- Logic
- Task
- Gateway
- Web（Vite dev server）

启动成功后可访问：

- Web: `http://localhost:5173`
- Gateway: `http://localhost:8080`

按 `Ctrl+C` 可同时停止所有服务。

---

## 4. 常用命令

### 4.1 代码生成

```bash
make gen   # 生成 proto 的 Go + TS 代码
make tidy  # 整理 Go 依赖
```

### 4.2 质量门禁

```bash
make format   # Go / Proto / Prettier / Markdown 全量格式化
make lint     # Go / Proto / Web / Markdown 全量检查
make test     # go test ./...
```

提交前必须通过：

```bash
make format && make lint
```

### 4.3 单独运行某个服务

```bash
go run main.go -module logic
go run main.go -module gateway
go run main.go -module task
go run main.go -module init
```

当你只改某个服务时，单独启动会更快。

### 4.4 Docker 模式

```bash
make up        # Docker 本地模式
make up-prod   # 生产模式（需要 Caddy 网络）
make down      # 停止服务
make logs      # 查看日志
```

---

## 5. 测试怎么跑

项目的后端测试优先使用 Testcontainers，而不是要求你先手动启动数据库。

```bash
# 全量测试（需要 Docker daemon）
make test

# Repo 组件测试
go test ./repo/...

# 黄金链路集成测试
go test ./test/integration/...
```

当前已覆盖的三条黄金链路：

- 在线消息投递
- 离线消息补偿
- 已读回执

如果 Docker 不可用，Testcontainers 测试会跳过并给出明确原因。

---

## 6. 前端开发入口

前端代码位于 `web/src/`：

- `features/`：业务页面和模块
- `api/`：ConnectRPC 和 WebSocket 客户端
- `db/`：Dexie 本地数据库
- `sync/`：事件同步与本地状态更新
- `stores/`：Zustand 全局状态

本地开发：

```bash
cd web
npm run dev
npm run build
npm run type-check
```

---

## 7. 调试入口

### 7.1 看链路问题时先看哪里

| 问题 | 先看哪里 |
| ---- | -------- |
| 发消息失败 | `logic/service/chat.go` |
| 收到 Ack 但别人收不到 | `logic/job/outbox.go` → `task/consumer/consumer.go` → `task/dispatcher/dispatcher.go` |
| WS 连不上 | `gateway/middleware/auth.go`、`gateway/transport/ws/` |
| 离线补偿不工作 | `sessionClient.pullInboxDelta`、`web/src/sync/inbox.ts` |
| 未读数不对 | `repo/session.go` 的 `last_read_seq` 更新逻辑 |

### 7.2 日志与指标

各服务默认都暴露 metrics 端口：

- Logic: `:9091`
- Gateway: `:9092`

日志默认输出到 stdout，本地开发使用 console 格式，便于直接读。

---

## 8. 常见问题

### Q1. 为什么 `make dev` 启不起来？

最常见原因是基础设施没启动。先执行：

```bash
make up-infra
make init
make dev
```

### Q2. 为什么测试报 Docker 相关错误？

因为 Repo/组件/集成测试依赖 Testcontainers。确认 Docker Desktop 已启动，并且当前用户能访问 Docker daemon。

### Q3. 为什么 WebSocket 认证要走查询参数而不是 Header？

浏览器原生 WebSocket API 不支持自定义 Authorization header，所以前端在连接 URL 上追加 `?token=<jwt>`。Gateway 连接建立后会验证 token，并把 username 绑定到连接上。

### Q4. 为什么收到 Ack 了，但对方还没看到消息？

Ack 只表示消息已经在 Logic 持久化，后续还需要经过 NATS → Task → Inbox → Push 这条异步链路。如果 Task 推送失败，对方重连后仍然可以通过 Inbox 补偿拿到消息。

---

## 9. 建议阅读顺序

### 后端开发

1. `00-overview.md`
2. `01-protocol.md`
3. `02-database.md`
4. `11-logic.md`
5. `12-task.md`
6. `20-message-flow.md`

### 前端开发

1. `00-overview.md`
2. `01-protocol.md`
3. `13-web.md`
4. `21-write-fanout.md`
5. `43-testing-strategy.md`

---

## 10. 小结

本地开发最短路径是：`make up-infra` → `make init` → `make dev`。理解项目的关键不是先读所有代码，而是先理解 Gateway / Logic / Task 三层边界和 `ChatEvent` 这条主线。只要这套心智先建立起来，后续无论是查问题、加功能还是补测试，都会快很多。
