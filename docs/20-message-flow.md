# 消息主链路

> 本文档描述 Resonance 中普通消息从发送到送达的完整时序。阅读完本文后，应该能回答三个问题：发送方收到 Ack 时系统处于什么状态；Task 为什么必须先写 Inbox 再推送；以及各个失败点分别如何处理。

---

## 1. 链路概述

消息主链路是当前系统中最完整、最稳定的业务闭环。它横跨 Gateway、Logic、NATS、Task 四个运行时组件，涉及 PostgreSQL 的事务写入、Redis 的序列号生成和在线路由查询、以及 WebSocket 的实时下发。理解这条链路，是理解整个系统可靠性设计的最短路径。

链路的核心语义只有一句话：发送方收到 Ack 代表主事实已经在 Logic 持久化，而不是接收方已经收到推送。推送是在线优化路径，Inbox 才是最终一致的兜底。

---

## 2. 完整时序图

```text
Web(A)              Gateway             Logic               NATS            Task            Gateway'
  │                    │                   │                   │               │                │
  │ 1. WS: ChatRequest │                   │                   │               │                │
  │   {session_id,     │                   │                   │               │                │
  │    message payload}│                   │                   │               │                │
  │───────────────────▶│                   │                   │               │                │
  │                    │ 2. gRPC SendEvent  │                   │               │                │
  │                    │   md: x-username=A│                   │               │                │
  │                    │──────────────────▶│                   │               │                │
  │                    │                   │ 3. 查会话成员      │               │                │
  │                    │                   │ 4. 校验发送权限    │               │                │
  │                    │                   │ 5. Snowflake 生成  │               │                │
  │                    │                   │    event_id        │               │                │
  │                    │                   │ 6. Redis 原子递增  │               │                │
  │                    │                   │    生成 seq_id     │               │                │
  │                    │                   │ 7. 事务：          │               │                │
  │                    │                   │   INSERT content   │               │                │
  │                    │                   │   CAS UPDATE seq   │               │                │
  │                    │                   │   INSERT outbox    │               │                │
  │                    │                   │ 8. 返回 Ack        │               │                │
  │                    │◀──────────────────│                   │               │                │
  │ 9. WS: Ack         │                   │                   │               │                │
  │   {event_id,seq_id}│                   │                   │               │                │
  │◀───────────────────│                   │                   │               │                │
  │                    │                   │ 10. 异步 Publish   │               │                │
  │                    │                   │    MQEvent → NATS  │               │                │
  │                    │                   │──────────────────▶│               │                │
  │                    │                   │                   │ 11. 投递事件   │                │
  │                    │                   │                   │──────────────▶│                │
  │                    │                   │                   │               │ 12. 批量写      │
  │                    │                   │                   │               │    t_inbox      │
  │                    │                   │                   │               │    (所有成员)   │
  │                    │                   │                   │               │ 13. 查 Redis    │
  │                    │                   │                   │               │    在线路由     │
  │                    │                   │                   │               │ 14. 按 Gateway  │
  │                    │                   │                   │               │    分组推送     │
  │                    │                   │                   │               │───────────────▶│
  │                    │                   │                   │               │                │ 15. WS 下发
  │                    │                   │                   │               │                │   ChatEvent
  │                    │                   │                   │               │                │──────────▶ Web(B)
```

---

## 3. 各阶段说明

### 3.1 Gateway 接入（步骤 1-2）

客户端通过 WebSocket 发送 `ChatRequest`，Gateway 的 WS dispatcher 解析包格式，从连接上下文中取出已认证的用户名，构造 gRPC `SendEvent` 请求，通过 `x-username` metadata 把身份传给 Logic。Gateway 不做任何业务判断，只负责协议转换和身份传递。

### 3.2 Logic 业务处理（步骤 3-8）

Logic 是这条链路中唯一做业务判断的地方。它先查询会话成员列表，校验发送者是否在会话中；通过后，用 Snowflake 生成全局唯一的 `event_id`，再通过 Redis 原子递增生成会话内单调递增的 `seq_id`。

序列号生成有一个细节：如果 Redis 中该会话的计数器键不存在（例如服务重启后），Logic 会先用 `t_session.max_seq_id` 初始化计数器，再递增，避免序列号从 1 开始与历史记录冲突。

事务内同时完成三件事：写入 `t_message_content`、用 CAS 更新 `t_session.max_seq_id`（防止并发回退）、写入 `t_message_outbox`。事务提交后立即返回 Ack，不等待异步链路完成。

### 3.3 同步 Ack（步骤 9）

发送方收到 Ack 时，`event_id` 和 `seq_id` 已经确定，主事实已经持久化。这是系统对发送方的承诺：消息已经被接受，不会丢失。后续的异步扩散和推送是系统内部的事情，与发送方的 Ack 语义无关。

### 3.4 异步投递（步骤 10-11）

Logic 在事务提交后异步发布 `MQEvent` 到 NATS。如果发布失败，`logic/job/outbox.go` 的定时补偿任务会扫描 `t_message_outbox` 中状态为待发送的记录并重新投递。这保证了事件最终一定会进入 Task 的消费链路。

### 3.5 Task 写扩散（步骤 12）

Task 消费到 `MQEvent` 后，首先批量写入所有目标用户的 Inbox。`MQEvent` 中的 `target_usernames` 包含会话所有成员（包括发送者自己，用于多端同步）。写入使用 `ON CONFLICT DO NOTHING`，保证 MQ 重投时的幂等性。

写扩散失败会触发 Consumer NAK，NATS 会重新投递消息。这是整条链路中唯一会触发重试的失败点，因为此时事件还没有进入任何用户的可恢复视图。

### 3.6 在线推送（步骤 13-15）

写扩散成功后，Task 查询 Redis 中各目标用户的在线路由，按 Gateway 实例分组，向每个 Gateway 发起 gRPC Push 调用。Gateway 收到 Push 后，在本地连接管理器中查找目标用户的 WebSocket 连接，找到则下发事件，找不到则静默忽略。

推送失败不触发 NAK，因为 Inbox 已经承担了一致性兜底。用户重连后可以通过 `PullInboxDelta` 拉取到同样的事件。

---

## 4. 失败处理

| 失败点 | 处理方式 | 原因 |
| ------ | -------- | ---- |
| Logic 事务失败 | 返回 gRPC error，Gateway 回 WS error | 主事实未成立，发送方需要重试 |
| MQ Publish 失败 | Outbox 补偿任务定时重投 | 事务已提交，事件必须最终进入异步链路 |
| Task 写 Inbox 失败 | Consumer NAK，NATS 重新投递 | 事件尚未进入可恢复视图，必须重试 |
| Task 推送失败 | 记录日志，不 NAK | Inbox 已写入，用户重连可补偿 |
| Gateway 推送到 WS 失败 | 静默忽略 | 同上，Inbox 兜底 |

---

## 5. 关键设计约束

**先写 Inbox，后推送。** 这不是实现细节，而是整个系统可靠性的核心约束。如果顺序颠倒，在线用户可能先收到推送，但断线重连后在 Inbox 中找不到对应记录，导致状态不一致。

**发送方也在 target_usernames 中。** `MQEvent.target_usernames` 包含会话所有成员，包括发送者自己。这样发送者的其他设备（多端场景）也能通过 Inbox 同步到自己发出的消息。

**seq_id 单调递增，不保证连续。** 序列号由 Redis 原子递增生成，并发场景下可能出现空洞（某个 seq_id 对应的事件写入失败后，该号码不会被复用）。客户端应该按 seq_id 排序渲染，而不是假设连续。

---

## 6. 阅读建议

| 文档 | 内容 |
| ---- | ---- |
| `02-database.md` | 各表结构与索引设计 |
| `11-logic.md` | Logic 事务边界与 Outbox 模式 |
| `12-task.md` | Task 消费语义与失败处理 |
| `21-write-fanout.md` | 写扩散模型与 Inbox 一致性策略 |

---

## 7. 小结

消息主链路的核心是两个清晰的阶段：Logic 的同步事务阶段负责让主事实成立并返回 Ack，Task 的异步消费阶段负责把事件扩散到所有接收者的 Inbox 并尝试在线推送。这两个阶段通过 NATS 和 Outbox 解耦，任何一个阶段的失败都有对应的补偿机制，整个链路在任意节点失败时都能维持最终一致。
