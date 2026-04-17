# 架构总览:从消息驱动到事件驱动

> 本文档是 Resonance 架构设计的纲领性文档,其他 `01~05` 按本文的分层展开。阅读顺序建议按编号。

---

## 0. 写这套文档的原因

当前架构**基本可用**,Logic/Gateway/Task 的三层职责划分是合理的,Outbox 模式也用对了。但面向未来扩展(AI 聊天 + 流式、消息撤回、已读同步、消息编辑)时,发现几个**结构性问题**会持续增加维护成本:

1. **同一条消息在协议里定义了四遍**(ChatRequest / SendMessageRequest / PushEvent / PushMessage),加一个字段要改 4 个地方。
2. **协议只有"消息"这一种载体**,撤回/已读/编辑/流式都无处安放。
3. **Inbox 只承载"消息"**,用户的多端已读同步、撤回通知没有统一通道。
4. **分层依赖方向错了**:`logic/v1/session.proto` 反过来引用 `gateway/v1/packet.proto`。
5. **协议里的死字段**:所有 `access_token` 在 body 里,但服务端不使用(鉴权走中间件)。

这些问题现在改,成本是 1;做完 AI + 撤回 + 编辑后再改,成本是 10。所以在功能扩展前,先**把协议与数据模型的骨架立好**。

---

## 1. 设计原则

按优先级排列,冲突时上位原则优先。

### P1. 事件驱动,而非消息驱动

**消息只是事件的一种**。在 IM 系统里,用户能感知到的变更都是"作用在会话上的事件":发消息、撤回、编辑、已读、入群、改群名、AI 流式回复。应该有一个统一的 `ChatEvent` 载体,`oneof` 承载不同类型的 payload。

### P2. 协议抽象沉淀到 `common/v1`

跨服务复用的消息结构(User、Session、Message、ChatEvent)一律放 `common/v1`。**上层引用下层,绝不反向**。Logic 不应该 import Gateway 的 proto。

### P3. 持久化事件 vs 短暂事件分离

- **持久化事件**:走 `Outbox → MQ → Task → Inbox` 完整链路,保证重连可拉。例:消息、撤回、已读位点、会话更新。
- **短暂事件**:不持久化,直接 `AI Service → Gateway → Web`。例:AI 流式 token、"正在输入"状态。

这条原则决定了 AI 流式的架构:每个 token 不进 Inbox,只有最终完整消息进。

### P4. 身份与鉴权不进业务 body

`access_token` 从所有 RPC body 中移除,统一走 HTTP Header(Web → Gateway)和 gRPC metadata(Gateway → Logic)。业务 body 只留业务字段。

### P5. 错误处理用 gRPC 原生机制

删除所有响应里的 `string error` 字段。失败用 `status.Errorf(codes.X, ...)`,客户端用标准 gRPC error handling。成功响应不带错误字段。

### P6. 强类型枚举替代 string/int

`MessageType`、`SessionType`、`EventType` 等概念都用 `enum`,不用散装的 string("text"/"image") 或 int(1/2)。

### P7. 为扩展留出 `oneof`,不为扩展乱加字段

`ChatEvent.payload` 用 oneof,新增事件类型只加分支,不动现有字段编号。宁可多一个空 message 类型,不在已有 message 里堆字段。

### P8. 面向业务接口,隐藏基础组件

保持现有规范。Logic 不对外暴露 `connector.RedisConnector` 这类底层类型,只通过 `repo.*Repo` 接口交互。

---

## 2. 架构分层

```
┌──────────────────────────────────────────────────────────────────┐
│                           Web (React)                            │
│  ConnectRPC(HTTP) 发起请求  +  WebSocket 接收事件推送             │
└────────────────┬──────────────────────────────┬──────────────────┘
                 │ HTTP/WS                      │ WS
                 ▼                              ▼
┌──────────────────────────────────────────────────────────────────┐
│                          Gateway (协议层)                         │
│  • ConnectRPC Handlers (鉴权中间件 → 转发 gRPC 到 Logic)         │
│  • WebSocket Dispatcher (上行 ChatRequest / 下行 ChatEvent)      │
│  • gRPC PushService (接收 Task 持久化事件 + AI 短暂事件)          │
│  • StatusBatcher (在线状态批量上报)                              │
└────────────┬────────────────────────────┬────────────────────────┘
             │ gRPC                       ▲ gRPC Push
             ▼                            │
┌──────────────────────────────┐  ┌──────────────────────────────┐
│       Logic (业务层)          │  │        Task (异步层)          │
│  • 鉴权与业务规则             │  │  • MQ Consumer               │
│  • 生成 ChatEvent            │  │  • Dispatcher (按 event type)│
│  • Outbox 事务写入           │  │    ├─ 写 Inbox (写扩散)       │
│  • 发布 MQ                    │  │    ├─ 更新主表 (撤回/编辑)    │
│  • Outbox Worker 兜底补偿     │  │    └─ 更新会话状态 (已读)     │
└──────┬───────────────────┬───┘  │  • Pusher (路由 → Gateway)   │
       │ DB                │ MQ   └────────────────┬─────────────┘
       ▼                   ▼                      ▲
┌─────────────┐   ┌─────────────┐                 │
│ PostgreSQL  │   │    NATS     │─────────────────┘
│   + Redis   │   └─────────────┘
└─────────────┘
```

### 服务职责一句话定义

| 服务 | 一句话职责 | 不负责 |
|------|-----------|--------|
| **Gateway** | 协议转换 + 连接管理 | 业务规则、持久化 |
| **Logic** | 业务规则 + 事件生成 + 事务一致性 | 推送、写扩散 |
| **Task** | 事件落地(Inbox/状态表) + 在线推送 | 业务判断、鉴权 |
| **AI Service**(未来) | 调用大模型 + 工具 + 生成回复事件 | 会话管理、消息存储 |

### 边界规则

1. **Gateway 不持有业务状态**,只做协议转换和连接管理。所有"是否允许"、"写哪里"都问 Logic。
2. **Logic 不直接推送**,生成事件后投递到 MQ,由 Task 消费。
3. **Task 不做业务判断**,只执行"把事件落地 + 推送到路由"。
4. **AI Service 是特殊的 Logic 客户端**,它通过调用 Logic.SendEvent 来回复消息,不直接写库。

---

## 3. 核心抽象:ChatEvent

```
ChatEvent = 会话中发生的任何用户可感知的事情
         = { event_id, seq_id, session_id, from_username, timestamp, oneof payload }

oneof payload:
  ├─ Message          普通消息(文本/图片/文件)
  ├─ MessageRecall    撤回
  ├─ MessageEdit      编辑
  ├─ ReadReceipt      已读位点变化
  ├─ SessionUpdate    会话元数据变更(改群名、换头像、加成员)
  └─ (未来扩展)        Reaction、Mention、...
```

**为什么这么设计**:
- **一个 Inbox 一种结构**:`t_inbox.payload` 存 ChatEvent 的 bytes,前端用一个订阅通道拿到所有事件。
- **一个推送路径**:Task → Gateway → Web 永远推送 `ChatEvent`,不关心是消息还是撤回。
- **一个拉取接口**:`PullInboxDelta` 返回 `repeated ChatEvent`,前端按 `payload` 类型分发渲染。
- **新功能零协议成本**:加一个事件类型只需要 oneof 加分支 + 后端处理逻辑。

**不放进 ChatEvent 的东西**:
- AI 流式 token(`StreamChunk`):短暂事件,走独立路径。
- 心跳 `Pulse`、确认 `Ack`:属于连接层,不是会话事件。
- 在线状态变更:属于用户维度,不是会话维度。

---

## 4. 未来扩展的具体映射

| 功能 | 如何接入 |
|------|----------|
| 消息撤回 | `ChatEvent.payload = MessageRecall`,经 Outbox/MQ/Inbox 全链路,Task 侧在主表标记 `recalled_at` |
| 消息编辑 | `ChatEvent.payload = MessageEdit`,同上 |
| 多端已读同步 | `UpdateReadPosition` 同时写 Inbox 一条 `ReadReceipt` 事件,其他在线端通过推送感知 |
| AI 普通聊天 | AI Service 订阅 MQ 过滤 AI 会话 → 调用模型 → `Logic.SendEvent` 作为 Bot 用户回复,走普通事件链路 |
| AI 流式 | AI Service 分配 event_id → 连续 `StreamChunk` 直推 Gateway(不入 Inbox)→ 完成后一次 `SendEvent` 写最终消息 |
| @ 提及 | `Message.mentioned_usernames`;`ChatEvent.payload = Mention`(可选)触发特殊提醒 |
| 表情回应 | `ChatEvent.payload = Reaction`,按事件流自然同步 |
| 群成员变更 | `ChatEvent.payload = SessionUpdate`,所有成员 Inbox 都收到 |

**核心观察**:上述所有功能的协议改动,都是 `ChatEvent.payload` 的 oneof 加分支,**不需要改 WebSocket 协议、Push RPC 接口、Inbox 表结构**。

---

## 5. 性能与可靠性的关键决策

### 5.1 Task 双消费者合并为单消费者,先存储后推送

当前双消费者(Storage + Push 并行)会导致"Push 先于 Inbox 到达"的时序问题,且 MQ 消费 ×2。改为串行:

```
Task.Consumer 收到 PushEvent
  ↓
DispatchStorage(写 Inbox / 更新主表 / ...)      ← 失败 NAK 重试
  ↓ 成功
DispatchPush(查路由 → 推 Gateway)                ← 失败不 NAK,靠 Inbox 兜底
  ↓
ACK
```

**理由**:Inbox 是可靠兜底,Push 只是在线优化。顺序反过来会导致在线用户收到推送但断线后拉不到,不一致。

### 5.2 `t_message_content` 保留,承担主表职责

Outbox 模式下,`message_content` 是业务主表(历史查询、引用回复),`message_outbox` 是投递任务,两者职责不同,不合并。

### 5.3 `t_inbox` 重构为事件流

当前 Inbox 只存 (msg_id, seq_id, is_read),只能表达"消息"。改造为:

- `event_type`:小整数,区分事件类型
- `payload`:bytes,存 ChatEvent 序列化内容
- `is_read` 字段迁移:已读位点统一由 `t_session_member.last_read_seq` 承担,Inbox 不再存
- 保留 (owner_username, session_id, seq_id) 唯一约束,保留 owner_username 游标索引

详见 `02-database.md`。

### 5.4 身份从 body 改走 metadata

Gateway 已经通过 JWT 中间件解出了当前用户。往 Logic 调 gRPC 时,把 `username` 放进 metadata,Logic 侧 interceptor 解出放到 context。业务 body 只留"被操作对象"字段。

---

## 6. 文档索引

| 文档 | 内容 |
|------|------|
| `00-overview.md`(本文档) | 架构总览、设计原则、核心抽象、扩展映射 |
| [`01-protocol.md`](./01-protocol.md) | Proto 目录结构、ChatEvent 详细定义、各层 RPC |
| [`02-database.md`](./02-database.md) | 表结构设计、字段语义、索引、迁移 SQL |
| [`03-services.md`](./03-services.md) | 三个服务的代码组织与模块职责 |
| [`04-flows.md`](./04-flows.md) | 发消息 / 撤回 / 已读 / 离线 / AI 流式 五个核心流程 |
| [`05-migration.md`](./05-migration.md) | 从现状到目标的分阶段改造计划 |

---

## 7. 阅读建议

- **只想快速看方向**:读完本文 + 瞄一眼 `01-protocol.md` 的 ChatEvent 定义即可。
- **要开始动手改**:按 `05-migration.md` 的阶段顺序,每改一个阶段回来对照对应章节。
- **不确定某个细节为什么这样设计**:每个设计决策都尽量写了"为什么",如果没有或觉得说不通,就是讨论点,提出来重定。
