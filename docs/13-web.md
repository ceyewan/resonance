# Web 前端设计

> 本文档描述 Resonance Web 客户端的架构、状态管理、通信层和本地同步模型。阅读完本文后，应该能回答三个问题：前端如何通过 WebSocket 和 ConnectRPC 与后端交互；Dexie 在本地状态中扮演什么角色；以及重连后如何通过 Inbox 游标恢复一致状态。

---

## 1. 技术栈

| 类别 | 技术 | 用途 |
| ---- | ---- | ---- |
| 框架 | React 18 | UI 渲染与组件树 |
| 语言 | TypeScript | 类型安全 |
| 构建 | Vite | 本地开发与生产构建 |
| 路由 | TanStack Router | 类型安全的客户端路由 |
| 状态管理 | Zustand | 全局运行时状态（认证、连接、会话） |
| 本地存储 | Dexie (IndexedDB) | 持久化本地缓存（消息、会话、发件箱） |
| HTTP 通信 | ConnectRPC | 同步请求（登录、会话列表、历史消息） |
| 实时通信 | WebSocket | 实时事件接收与消息发送 |
| 协议 | Protobuf (buf) | 与后端共享类型定义 |

---

## 2. 模块结构

```text
web/src/
├── api/              # 通信层
│   ├── clients.ts    # ConnectRPC 客户端（authClient / sessionClient）
│   ├── transport.ts  # ConnectRPC transport 配置
│   └── ws/           # WebSocket 客户端与包分发
│       ├── client.ts     # WsClient：连接管理、心跳、重连
│       ├── dispatcher.ts # WsPacket 分发到对应 handler
│       └── outbox.ts     # 客户端发件箱（消息发送队列）
├── db/               # 本地持久化
│   ├── schema.ts     # Dexie 表结构定义
│   └── repo.ts       # 数据库读写操作
├── stores/           # Zustand 全局状态
│   ├── auth.ts       # 认证状态（token、当前用户）
│   └── connection.ts # 连接状态（WS 状态、Inbox 同步状态）
├── sync/             # 事件同步与本地状态更新
│   ├── applier.ts    # ChatEvent → 本地 DB 写入
│   ├── reconcile.ts  # WS 事件 / Inbox 事件统一入口
│   └── inbox.ts      # InboxSyncManager：游标拉取与应用
├── features/         # 业务功能模块
│   ├── auth/         # 登录、注册页面
│   ├── chat/         # 聊天界面、消息列表
│   ├── contact/      # 联系人列表
│   ├── session-detail/ # 会话详情
│   └── settings/     # 设置页面
├── hooks/            # 业务 hooks
└── gen/              # 生成的 proto 类型（不手动修改）
```

---

## 3. 通信层

### 3.1 ConnectRPC（同步请求）

同步请求通过 ConnectRPC 发起，使用 `api/clients.ts` 中的客户端：

```typescript
export const authClient = createClient(AuthService, transport);
export const sessionClient = createClient(SessionService, transport);
```

`transport` 配置了 API base URL（从环境变量 `RESONANCE_WEB_API_BASE_URL` 读取），并在每个请求的 header 中注入 `Authorization: Bearer <token>`。

ConnectRPC 主要用于：登录/注册、拉取会话列表、拉取历史消息、更新已读位点、`PullInboxDelta` 增量同步。

### 3.2 WebSocket（实时通信）

`WsClient` 封装了 WebSocket 连接的完整生命周期：

- 连接建立时通过 URL 查询参数传递 token（`?token=<jwt>`）
- 心跳：每 20 秒发送 `Pulse` 包，保持连接活跃
- 重连：断线后以指数退避策略自动重连（基础延迟 1s，最大 30s）
- 包格式：所有消息使用 Protobuf 二进制编码的 `WsPacket`

`WsPacket` 是 WebSocket 上的统一包格式，通过 `oneof payload` 承载不同类型的消息：

```text
WsPacket.payload
  ├── pulse        → 心跳
  ├── ack          → 服务端对发送请求的确认（含 event_id / seq_id）
  ├── chatRequest  → 客户端发送消息（上行）
  └── event        → 服务端推送的 ChatEvent（下行）
```

收到包后，`dispatchWsPacket` 根据 `payload.case` 分发到对应 handler。

---

## 4. 本地状态（Dexie）

Dexie 是前端本地状态的持久化层，基于 IndexedDB。它承担两个职责：离线缓存（用户关闭页面后重新打开时不需要重新拉取全量数据）和乐观更新（消息发送后立即在本地显示，等待服务端 Ack 确认）。

本地数据库有四张表：

| 表 | 主键 | 用途 |
| -- | ---- | ---- |
| `sessions` | `sessionId` | 会话列表、未读数、最后一条消息预览 |
| `events` | `[sessionId, seqId]` | 消息和事件（按会话+序列号索引） |
| `outbox` | `clientSeq` | 客户端发件箱（待确认的发送请求） |
| `meta` | `key` | 元数据（Inbox 游标等） |

`events` 表存储所有类型的 `ChatEvent`（消息、撤回、编辑、已读回执、会话更新），字段结构与 `ChatEvent` proto 对齐，使用字符串存储 bigint 类型的 ID 以避免 JavaScript 精度问题。

---

## 5. 事件同步模型

前端的事件同步有两条路径，最终都汇聚到同一个 `applier`：

```text
WebSocket 推送
  └── reconcileWsEvent(ChatEvent)
        └── applyEvent(ChatEvent)
              └── 写入 Dexie（更新 events / sessions 表）

Inbox 增量拉取
  └── InboxSyncManager.run()
        └── PullInboxDelta(cursor_id, limit)
              └── reconcileInboxEvent(InboxEvent)
                    └── applyEvent(ChatEvent)
                          └── 写入 Dexie
```

`applier.ts` 是本地状态更新的核心，它把一个 `ChatEvent` 翻译成对 Dexie 的写操作：

- `message`：写入 `events` 表，更新 `sessions` 表的最后消息预览和未读数
- `recall`：标记 `events` 表中对应记录的 `recalled = true`
- `edit`：更新 `events` 表中对应记录的内容
- `readReceipt`：更新 `sessions` 表的 `readUptoSeqByUser`
- `sessionUpdate`：更新 `sessions` 表的名称/头像等元数据

### 5.1 重连同步

WebSocket 重连后，前端会触发 `InboxSyncManager.run()`，从本地存储的游标（`meta` 表中的 `inbox_cursor`）开始拉取增量事件。这个机制保证了离线期间产生的所有事件都能被补偿，不依赖 WebSocket 推送的可靠性。

游标更新策略：每批拉取完成后，将本批最大的 `id` 写入 `meta.inbox_cursor`。如果拉取结果数量等于 `limit`（默认 200），则继续拉取下一批，直到拿到空结果为止。

---

## 6. 客户端发件箱（Outbox）

消息发送使用客户端发件箱模式，实现乐观更新：

```text
用户点击发送
  ├── 生成 clientMsgId（本地唯一 ID）
  ├── 写入 outbox 表（status = "sending"）
  ├── 在消息列表中立即显示（乐观渲染）
  └── 通过 WS 发送 ChatRequest

收到服务端 Ack
  ├── 更新 outbox 记录（status = "acked"，写入 event_id / seq_id）
  └── 更新 events 表（用 event_id 替换临时 clientMsgId）

发送失败
  └── 更新 outbox 记录（status = "failed"，可重试）
```

`clientMsgId` 同时用于服务端幂等去重，最长 64 字节且应视为不透明值。客户端重试必须复用同一个 ID 和完全相同的消息字段；Logic 会返回第一次的 `event_id` / `seq_id` / 时间戳，不产生第二条 Message 或 Outbox。同一个 ID 携带不同 payload 会收到 `AlreadyExists`，客户端必须生成新 ID，而不是继续重试。

---

## 7. 状态管理

Zustand 管理两类全局运行时状态，不持久化到 IndexedDB：

**`useAuthStore`**：认证状态，包含 `accessToken`、`currentUser`、`bootstrapping` 标志。应用启动时从 `localStorage` 恢复 token，验证有效性后进入已认证状态。

**`useConnectionStore`**：连接状态，包含 WS 连接状态（`idle / connecting / open / offline`）和 Inbox 同步状态（`inboxSyncing`、`lastInboxSyncError`）。UI 根据这些状态显示连接指示器和同步进度。

**`useAgentStreamStore`**：只保存 Agent 的临时文本气泡。它按 `session_id + stream_id` 隔离，校验 `run_id` 和严格递增的 `uint64 sequence`，对重复、乱序、容量超限和 TTL 超时 fail bounded。Stream End 只关闭临时状态；最终 ChatEvent 成功写入 IndexedDB 后，再用 `final_client_msg_id` 对账删除临时气泡。该 Store 不持久化 thinking、Tool 原始参数或 Tool Result。

---

## 8. 当前实现结构

| 路径 | 内容 |
| ---- | ---- |
| `web/src/api/ws/client.ts` | WsClient 连接管理、心跳、重连 |
| `web/src/api/ws/dispatcher.ts` | WsPacket 分发 |
| `web/src/api/ws/outbox.ts` | 客户端发件箱 |
| `web/src/db/schema.ts` | Dexie 表结构 |
| `web/src/sync/applier.ts` | ChatEvent → 本地 DB |
| `web/src/sync/inbox.ts` | InboxSyncManager |
| `web/src/stores/` | Zustand 全局状态 |
| `web/src/stores/agentStream.ts` | 有界 Agent 临时流状态与序号检查 |
| `web/src/sync/agentStream.ts` | StreamBegin/Chunk/End 与最终 ChatEvent 对账 |
| `web/src/features/chat/AgentStreamBubble.tsx` | 纯文本临时气泡 |
| `web/src/features/` | 业务功能模块 |

---

## 9. 阅读建议

| 文档 | 内容 |
| ---- | ---- |
| `01-protocol.md` | ChatEvent 结构与 WsPacket 的关系 |
| `21-write-fanout.md` | Inbox 游标与增量拉取的服务端设计 |
| `03-auth-and-security.md` | token 传递与 WS 鉴权约定 |

---

## 10. 小结

Web 前端以 Dexie 为本地状态中心，以 `applier` 为事件处理核心，通过 WebSocket 推送和 Inbox 增量拉取两条路径保持与服务端的一致性。重连后自动触发 Inbox 同步，保证离线期间的事件不会丢失。客户端发件箱实现乐观更新，让消息发送体验流畅，同时通过 `clientMsgId` 与服务端幂等机制配合，避免重复消息。
