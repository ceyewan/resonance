# 测试策略

> 本文档描述 Resonance 的测试分层设计、Testcontainers 使用方式和各层测试的组织结构。阅读完本文后，应该能回答三个问题：为什么优先使用真实依赖而不是 Mock；各层测试分别验证什么、放在哪里；以及如何在 CI 中分层执行测试。

---

## 1. 核心原则

Resonance 的测试策略围绕一句话：**用真实依赖验证主链路，用小成本隔离非关键边界。**

对 PostgreSQL、Redis、NATS 这类基础设施，优先使用 Testcontainers 拉起真实容器，而不是自己造一层假的数据库行为。Mock 只用于隔离"当前不想纳入测试范围的邻接服务"，不用于模拟整套系统。

这个选择的原因很直接：IM 系统的核心正确性体现在数据如何写入、如何查询、如何在服务间流转，这些行为只有真实依赖才能验证。过度 Mock 会让测试通过但生产失败。

---

## 2. 测试分层

| 层级 | 验证目标 | 依赖形态 | 阻塞 PR |
| ---- | -------- | -------- | ------- |
| 单元测试 | 业务规则、权限判断、纯逻辑 | 手写 Fake / Stub | 是 |
| 组件测试 | 单服务在真实依赖下的行为 | Testcontainers + 单服务 | 是 |
| 集成测试 | 多服务联调的黄金链路 | Testcontainers + 多服务进程内组装 | 是 |
| 端到端/压测 | 完整用户场景与系统容量 | 真实全链路 | 否，定时执行 |

---

## 3. 单元测试

单元测试验证纯业务逻辑，不依赖外部基础设施，运行速度快。

**主要覆盖范围：**

- `logic/service/`：权限校验（非成员不能发消息）、事件生成、序列号逻辑、已读位点更新
- `task/dispatcher/`：事件分发、Inbox 记录构造、推送目标计算、未知 payload 安全跳过
- `gateway/transport/ws/`：WS 包编解码、连接管理

**依赖隔离方式：**

优先手写 Fake，接口小时直接实现一个测试用的假实现。复杂交互场景使用 `go.uber.org/mock/gomock`。断言库统一使用 `testify/require`（关键前置条件）和 `testify/assert`（字段校验）。

`logic/service/testutil_test.go` 和 `task/dispatcher/testutil_test.go` 提供了公共测试模块，包含 `testSessionRepo`、`testMessageRepo`、`testMQ` 等 Fake 实现，新增单元测试可以直接复用。

---

## 4. 组件测试（Testcontainers）

组件测试验证单个服务在真实依赖下的完整行为，是当前测试体系中最重要的一层。

### 4.1 Testcontainers 使用模式

`repo/testutil_test.go` 定义了标准的容器启动模式，所有组件测试都复用这套基础设施：

```go
// 容器在包级别单例启动，所有测试共享，避免重复拉起
var (
    postgresOnce sync.Once
    redisOnce    sync.Once
)

func startPostgresContainer() (string, int, error) {
    postgresOnce.Do(func() {
        // testcontainers-go 拉起 postgres:17-alpine
        // 等待 pg_isready 健康检查通过
    })
    // 返回 host:port
}
```

容器在包级别以单例模式启动，同一个测试包内的所有测试共享同一个容器实例，避免每个测试都重新拉起容器的开销。

### 4.2 Repo 组件测试

位置：`repo/*_test.go`

依赖：PostgreSQL + Redis（Testcontainers）

覆盖：消息存取、Inbox 批量写入与幂等、游标分页、会话成员查询、Router 读写、唯一约束验证。

```bash
go test ./repo/...
```

### 4.3 Logic 组件测试

位置：`logic/integration/logic_integration_test.go`

依赖：PostgreSQL + Redis + NATS（Testcontainers），进程内启动 Logic gRPC 服务

覆盖：注册 → 建会话 → 发消息完整链路，断言 `t_message_content` 和 `t_message_outbox` 写入正确。

```bash
go test ./logic/integration/...
```

### 4.4 Task 组件测试

位置：`task/integration/task_integration_test.go`

依赖：PostgreSQL + Redis + NATS（Testcontainers），进程内启动 Task Consumer + Dispatcher

覆盖：向 NATS 发布 `MQEvent` → Task 消费 → Inbox 落库，验证 Push client 不可用时不影响消费 ACK。

```bash
go test ./task/integration/...
```

### 4.5 Gateway 组件测试

位置：`gateway/integration/gateway_integration_test.go`

依赖：Redis + etcd（Testcontainers），Logic 用 Fake gRPC server 代替

覆盖：gRPC PushService → WS 客户端投递最后一跳。

---

## 5. 集成测试（黄金链路）

集成测试验证多服务联调，位于 `test/integration/`，使用 Testcontainers 启动真实基础设施，进程内组装 Logic + Task + Gateway 多个服务组件。

当前已覆盖三条黄金链路：

**链路 A：在线消息投递**（`message_delivery_test.go`）

```text
注册用户 → 建立 WS 连接 → 创建会话 → 发送消息
→ Task 消费 MQEvent → 写 Inbox → Gateway Push → WS 收包
同时断言：接收方 Inbox 落库正确
```

**链路 B：离线消息补偿**（`offline_sync_test.go`）

```text
用户 B 离线 → 用户 A 发消息 → Task 落 Inbox
→ 用户 B 重连 → PullInboxDelta 拉取增量 → 收到消息
```

**链路 C：已读回执**（`read_receipt_test.go`）

```text
发送多条消息 → 分步 UpdateReadPosition
→ UnreadCount 与 LastReadSeq 正确变化
```

```bash
go test ./test/integration/...
```

---

## 6. 测试目录结构

```text
repo/
  *_test.go                    # Repo 组件测试（Testcontainers）

logic/
  service/
    *_test.go                  # 单元测试（手写 Fake）
    testutil_test.go           # 公共 Fake 模块
  integration/
    logic_integration_test.go  # Logic 单服务组件测试

task/
  dispatcher/
    *_test.go                  # 单元测试
    testutil_test.go           # 公共 Fake 模块
  consumer/
    *_test.go
  pusher/
    *_test.go
  integration/
    task_integration_test.go   # Task 单服务组件测试

gateway/
  transport/ws/
    *_test.go                  # WS 编解码单测
  pushserver/
    *_test.go
  integration/
    gateway_integration_test.go

test/
  integration/
    message_delivery_test.go   # 黄金链路：在线投递
    offline_sync_test.go       # 黄金链路：离线补偿
    read_receipt_test.go       # 黄金链路：已读回执
```

---

## 7. 执行命令

```bash
# 全量测试（需要 Docker daemon）
make test
# 等价于：go test ./...

# 只跑单元测试（无需 Docker）
go test ./logic/service/... ./task/dispatcher/... ./gateway/transport/...

# 只跑 Repo 组件测试
go test ./repo/...

# 只跑集成测试
go test ./test/integration/...

# 单个测试
go test ./repo/ -run TestMessageRepo_SaveInboxBatch -v

# 启用数据竞争检测
go test -race ./...
```

Docker 不可用时，Testcontainers 测试会跳过并给出明确原因，不会静默失败。

---

## 8. 前端测试

前端测试位于各模块的 `*.test.ts` 文件中，主要覆盖：

- `web/src/stores/chat.test.ts`：状态管理逻辑
- `web/src/sync/applier.test.ts`：ChatEvent → 本地 DB 写入逻辑
- `web/src/sync/inbox.test.ts`：InboxSyncManager 游标逻辑
- `web/src/sync/reconcile.test.ts`：事件分发逻辑
- `web/src/api/ws/dispatcher.test.ts`：WsPacket 分发
- `web/src/api/ws/outbox.test.ts`：客户端发件箱

```bash
cd web && npm run type-check  # 类型检查
```

---

## 9. 阅读建议

| 文档 | 内容 |
| ---- | ---- |
| `07-cicd-and-quality.md` | 质量门禁与 CI 执行策略 |
| `20-message-flow.md` | 集成测试验证的主链路 |
| `05-reliability.md` | 幂等与重试设计，测试需要覆盖的边界 |

---

## 10. 小结

Resonance 的测试体系以 Testcontainers 为基础，优先使用真实依赖验证核心链路。单元测试钉住业务规则，组件测试验证单服务行为，集成测试覆盖三条黄金链路（在线投递、离线补偿、已读回执）。这三层共同保证了系统在任意节点失败时的行为是可预期的。
