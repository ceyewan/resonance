# 可靠性设计

> 本文档描述 Resonance 在消息投递链路上的可靠性保证机制，包括 Outbox 补偿、MQ at-least-once 语义、幂等设计和失败降级策略。阅读完本文后，应该能回答三个问题：系统如何保证消息不丢失；哪些失败点会触发重试、哪些不会；以及幂等性在哪几个层面得到保证。

---

## 1. 可靠性目标

Resonance 对消息投递的可靠性承诺是：**主事实一旦在 Logic 成立，事件最终一定会进入每个目标用户的 Inbox**。这是最终一致性，不是强一致性——在 Task 完成写扩散之前存在短暂窗口期，但系统保证这个窗口期最终会被消除。

在线推送不在可靠性承诺范围内。推送是加速路径，失败时不触发重试，用户重连后通过 Inbox 补偿。

---

## 2. Outbox 模式

Outbox 是整个可靠性设计的起点，解决"主事实已提交但事件未投递"的问题。

### 2.1 为什么需要 Outbox

如果 Logic 在事务提交后直接发布 MQ，存在一个窗口：事务已提交，但 MQ 发布失败（网络抖动、NATS 重启）。这种情况下消息已经写入数据库，但 Task 永远不会收到通知，接收方永远看不到这条消息。

Outbox 通过把"MQ 投递记录"和"主事实写入"放在同一个数据库事务中，消除了这个窗口。

### 2.2 工作机制

```text
Logic.SendEvent 事务内：
  ├── INSERT t_message_content（主事实）
  ├── CAS UPDATE t_session.max_seq_id
  └── INSERT t_message_outbox（status=0, 投递记录）

事务提交后（异步）：
  ├── Publish MQEvent → NATS
  │     成功：UPDATE t_message_outbox SET status=1
  │     失败：不更新，由补偿任务处理
  │
  └── Outbox 补偿任务（logic/job/outbox.go）
        每秒扫描：WHERE status=0 AND next_retry_time <= NOW()
        重新发布 MQ，成功后更新 status=1
        超过最大重试次数（默认 5 次）后 status=2（失败）
```

补偿任务的扫描间隔为 1 秒，批次大小 100，并发 Worker 数 5，配置在 `configs/logic.yaml` 的 `outbox` 节。

### 2.3 重试退避

每次重试后，`next_retry_time` 按指数退避更新，避免在 NATS 不可用时产生大量无效扫描。

---

## 3. MQ at-least-once 语义

NATS JetStream 提供 at-least-once 投递语义：消息可能被重复投递，但不会丢失。Task 的 Consumer 在处理完成后发送 ACK，处理失败时发送 NAK 触发重新投递。

这意味着 Task 的所有处理逻辑必须是幂等的。

---

## 4. 幂等设计

系统在三个层面保证幂等性：

### 4.1 Outbox 投递幂等

Outbox 补偿任务可能在 MQ 发布成功但状态更新失败的情况下重复投递同一条消息。Task 消费到重复的 `MQEvent` 时，写扩散的幂等性保证不会产生重复 Inbox 记录。

### 4.2 Inbox 写入幂等

`t_inbox` 表上有唯一约束 `uniq_owner_sess_seq(owner_username, session_id, seq_id)`。`SaveInboxBatch` 使用 `ON CONFLICT DO NOTHING`，重复写入时静默忽略，不返回错误。

```sql
INSERT INTO t_inbox (...) VALUES (...)
ON CONFLICT (owner_username, session_id, seq_id) DO NOTHING
```

### 4.3 客户端发送幂等

客户端生成不超过 64 字节的 `client_msg_id`，并在重试时保持不变。Logic 先按 `(session_id, authenticated_sender, client_msg_id)` 查询，命中同一规范化请求时直接返回第一次的 ACK，不再分配 event/seq；并发未命中由数据库部分唯一索引和同一事务内的 `ON CONFLICT DO NOTHING` 收口。只有 `Created=true` 的调用才会创建 Outbox 并触发 look-aside 发布。

幂等不是“任意同 ID 都成功”：同键但消息类型、内容、引用目标或 Mention 列表不同会返回 `AlreadyExists`。空 `client_msg_id` 保持非幂等兼容语义。历史行没有请求 Hash 时也不会猜测或从已编辑内容回填，而是 fail closed。

---

## 5. 失败语义

不同失败点有不同的处理策略，这套语义的核心是：**存储失败重试，推送失败降级**。

| 失败点 | 处理方式 | 原因 |
| ------ | -------- | ---- |
| Logic 事务失败 | 返回 gRPC error，客户端重试 | 主事实未成立，必须重试 |
| MQ Publish 失败 | Outbox 补偿任务重投 | 事务已提交，事件必须最终投递 |
| Task 写 Inbox 失败 | Consumer NAK，NATS 重新投递 | 事件未进入可恢复视图，必须重试 |
| Task 推送失败 | 记录日志，不 NAK | Inbox 已写入，用户重连可补偿 |
| Gateway 推送到 WS 失败 | 静默忽略 | 同上 |
| 未知 payload 类型 | 直接 ACK，跳过 | 避免无意义反复重试卡死消费链路 |

"推送失败不 NAK"是一个关键设计决策。如果推送失败也触发重试，会导致同一条消息被反复推送给在线用户，产生重复通知。而 Inbox 已经承担了一致性兜底，推送只是优化路径，失败时降级到 Inbox 补偿是正确的行为。

---

## 6. 序列号一致性

会话序列号（`seq_id`）由 Redis 原子递增生成，保证单调递增。并发场景下可能出现空洞（某个 seq_id 对应的事务失败后，该号码不会被复用），但不会出现重复或回退。

Redis 键不存在时（服务重启后），Logic 用 `t_session.max_seq_id` 初始化计数器（`SetIfNotExists`），再递增，避免序列号从 1 开始与历史记录冲突。`t_session.max_seq_id` 在事务内通过 CAS 更新，防止并发写入导致回退。

---

## 7. 连接可靠性

Gateway 的 WebSocket 连接管理包含以下可靠性机制：

- **心跳检测**：每 30 秒发送 Ping，60 秒内未收到 Pong 则关闭连接
- **客户端重连**：前端 `WsClient` 以指数退避策略自动重连（1s → 30s）
- **重连后同步**：重连成功后立即触发 `InboxSyncManager.run()`，补偿离线期间的事件

### 7.1 服务级致命故障

Gateway 和 Logic 将 registry lease 丢失、Snowflake allocator keepalive 失败以及 gRPC/HTTP 服务异常退出视为进程级故障。后台协程通过有界 `Errors()` 通道报告第一个故障、撤销 readiness 并取消服务 context；`main` 同时等待操作系统信号和该错误通道。收到后台故障后进程以非零状态退出，defer 路径仍执行幂等、有界的 `Close()`，避免关键服务已经失效但进程继续存活。

---

## 8. 当前实现结构

| 文件 | 内容 |
| ---- | ---- |
| `logic/job/outbox.go` | Outbox 补偿任务 |
| `repo/message.go` | `SaveMessageWithOutbox`（事务）、`SaveInboxBatch`（幂等写入） |
| `task/consumer/consumer.go` | ACK/NAK 语义 |
| `task/dispatcher/dispatcher.go` | 失败降级策略 |
| `web/src/api/ws/client.ts` | 客户端重连与心跳 |
| `web/src/api/ws/outbox.ts` | 客户端发件箱与重试 |

---

## 9. 阅读建议

| 文档 | 内容 |
| ---- | ---- |
| `02-database.md` | Outbox 和 Inbox 表结构 |
| `20-message-flow.md` | 各失败点在主链路中的位置 |
| `12-task.md` | Task 的 ACK/NAK 语义 |

---

## 10. 小结

Resonance 的可靠性设计以 Outbox 为起点，以 Inbox 幂等写入为终点，在两端都建立了补偿机制。Outbox 保证事件从 Logic 到 Task 的最终投递，Inbox 唯一约束保证写扩散的幂等性，客户端 `client_msg_id` 保证发送端的幂等性。推送失败不触发重试，而是降级到 Inbox 补偿，这让系统在推送路径不可靠时仍然能维持最终一致。
