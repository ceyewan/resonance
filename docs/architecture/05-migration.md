# 迁移落地计划

> 本文档把从**现状**到**目标架构**的改造拆成可独立验证的阶段。每阶段可以单独部署,失败可回滚,不需要"大爆炸"式重构。

---

## 1. 改造全景

| 阶段 | 核心产出 | 依赖 | 预计破坏性 |
|------|----------|------|------------|
| **Phase 0** | 设计评审与对齐 | 无 | 无(纯文档) |
| **Phase 1** | Proto 重构:引入 `common/v1.ChatEvent` | Phase 0 | 高(proto 破坏性变更,前后端同步改) |
| **Phase 2** | 身份走 metadata、错误走 gRPC status | Phase 1 | 中 |
| **Phase 3** | DB 改造:Inbox 重构 + 字段增补 | Phase 1 | 高(涉及数据迁移) |
| **Phase 4** | Task 单消费者 + 串行化 | Phase 3 | 中 |
| **Phase 5** | 消息撤回功能(验证事件驱动框架) | Phase 3 | 低(纯新增功能) |
| **Phase 6** | 多端已读同步(ReadReceipt 事件) | Phase 3 | 低 |
| **Phase 7** | AI Service V1(非流式) | Phase 3 | 低 |
| **Phase 8** | AI 流式(StreamChunk 协议 + AI 流式输出) | Phase 7 | 中 |

**原则**:
- Phase 1~4 是**骨架改造**,必须先做完。
- Phase 5~8 是**功能叠加**,顺序可灵活调整,但撤回/已读先于 AI 落地——它们是事件驱动框架的**自然试金石**。

---

## 2. Phase 0:设计评审

**目标**:把 `00~04` 文档过一遍,对所有"设计决策"节点做确认。

检查清单:
- [ ] `00-overview.md` 的 8 条设计原则是否有异议
- [ ] `01-protocol.md` 的 `ChatEvent` 结构,payload oneof 分支是否覆盖已知场景
- [ ] `02-database.md` 的 Inbox 重构方案是否可行(灰度 vs 停机迁移)
- [ ] `03-services.md` 的 `event/` 模块拆分是否清晰
- [ ] `04-flows.md` 的时序图是否和现有实现有冲突

**产出**:更新文档,或记录不改的理由。

---

## 3. Phase 1:Proto 重构

**目标**:引入 `common/v1/ChatEvent`,消除四处重复的消息定义。**不改业务逻辑**,只改协议和序列化。

### 3.1 任务拆分

1. **新增 `api/proto/common/v1/` 的 4 个文件**
   - `session.proto`(SessionType enum + SessionMeta)
   - `message.proto`(MessageType enum + Message)
   - `event.proto`(ChatEvent 主体 + 所有 payload 类型)
   - 保留 `types.proto` 和 `options.proto`

2. **重写 `api/proto/gateway/v1/packet.proto`**
   - WsPacket 的 oneof 扩展,把下行统一为 `ChatEvent`
   - `seq` 改名 `client_seq`
   - 流式相关 packet(StreamBegin/Chunk/End)预留定义(实现延迟到 Phase 8)

3. **重写 `api/proto/gateway/v1/api.proto`**
   - 删除所有 `access_token` body 字段
   - `GetHistoryMessages` 改名 `GetHistoryEvents`,返回 `ChatEvent`
   - `PullInboxDelta` 的 `InboxEvent` 改成承载 `ChatEvent`
   - 移除所有 `string error` 字段

4. **重写 `api/proto/gateway/v1/push.proto`**
   - `PushService` 增加 `PushStream`(先占位,Phase 8 实现)
   - `Push` 改名 `PushEvent`,使用 `ChatEvent`

5. **重写 `api/proto/logic/v1/chat.proto`**
   - `SendMessage` 改为 `SendEvent`,oneof 支持 Message/Recall/Edit(Recall/Edit 先定义,Phase 5 才用)

6. **`api/proto/logic/v1/session.proto`**
   - 删除 `gateway/v1/packet.proto` 的 import
   - 所有 Request 删除 `username` 字段
   - 响应里的消息类型统一用 `common.v1.ChatEvent`

7. **`api/proto/mq/v1/event.proto`**
   - `PushEvent` 改名 `MQEvent`,包装 `ChatEvent` + `target_usernames`
   - topic 改名 `resonance.chat.event.v1`

### 3.2 代码改动

改完 proto 后 `make gen`,根据编译错误逐个修复:

**Logic**:
- `service/chat.go` 的 `SendMessage` 改为 `SendEvent`,入参是 `ChatEvent.payload`
- 内部构造 `ChatEvent` 填入 Outbox 的 payload(原先存 `PushEvent` bytes,现在存 `MQEvent` bytes)
- `service/session.go` 的响应类型从旧结构改为 `ChatEvent`

**Gateway**:
- `ws/dispatcher.go` 的 oneof 分发代码重写
- `api/*.go` 的 ConnectRPC handlers 的字段名调整
- `client/services.go` 的 Logic 调用签名调整
- `push/service.go` 的 PushEvent 接受 `ChatEvent`(PushStream 先占位,不实现)

**Task**:
- `dispatcher/dispatcher.go` 的 MQ 消费改为读 `MQEvent.event`(ChatEvent)
- 但**分发逻辑仍按旧方式**:所有 payload=Message 就走旧写 Inbox 流程。其他类型直接 log 并 ACK。
- Phase 4 再做完整分派。

**Web**:
- 所有用到旧类型(`PushMessage`、`ChatRequest`)的地方改为 `ChatEvent`
- 接入 TypeScript 生成的新类型

### 3.3 验证

- [ ] 原有"发消息 → 推送到对方"流程正常
- [ ] 历史拉取、会话列表、登录注册正常
- [ ] Inbox Delta 拉取正常
- [ ] 前端能看到消息、断线重连能补齐

### 3.4 回滚

Proto 是破坏性变更,无法简单回滚。Phase 1 前打 tag,如有严重问题 revert。

---

## 4. Phase 2:身份与错误规范化

**目标**:删除 body 里的死字段,错误用 gRPC status。

### 4.1 任务

1. **Gateway → Logic 注入 metadata**
   - `gateway/client/services.go` 统一封装,所有调用 Logic 时自动注入 `x-username`
   - `gateway/middleware/auth.go` 确认从 JWT 解析 username 并放 gin context

2. **Logic 增加 interceptor**
   - `logic/server/interceptor_auth.go` 新建,从 gRPC metadata 解出 `x-username` 放入 context
   - Service 层用 `MustUsernameFromCtx(ctx)` helper 取用

3. **前端清理**
   - `web/src/api/*.ts` 所有请求不再塞 `access_token` body,只设 `Authorization` Header

4. **错误规范**
   - 所有 Response 的 `error` 字段协议里已删(Phase 1 完成)
   - Service 层所有失败用 `status.Errorf(codes.X, ...)`
   - 前端错误处理用 ConnectRPC 标准机制,不查 body 里的 error 字段

### 4.2 验证

- [ ] 身份伪造测试:前端手改 body 里的 username(如果残留)无效
- [ ] 错误测试:发不存在的会话消息,客户端收到清晰的 `InvalidArgument` / `NotFound` 错误

---

## 5. Phase 3:DB 改造

**目标**:`t_inbox` 重构为事件流;`t_message_content` 加字段;`t_session` 加字段。

### 5.1 策略选择

根据项目当前用户量选:

**A. 停机迁移**(推荐,如果用户量小):
1. 停服
2. 执行 `02-database.md` 的 SQL
3. 清空 `t_inbox`(V1 的 Inbox 数据不迁移,用户重连后从主表重构一次最近的事件流)
4. 启动新版

**B. 灰度迁移**(大用户量):
1. 新建 `t_inbox_v2`
2. 双写:代码 check flag,既写旧也写新
3. 跑脚本把旧 Inbox 数据按 `event_id` JOIN `message_content` 重构成 ChatEvent bytes,写入 v2
4. 读切换到 v2
5. 停双写,删旧表,改名

### 5.2 model 和 repo 改动

1. **`model/model.go`**
   - 按 `02-database.md` 更新所有 struct tag
   - `MsgID` 字段改名 `EventID`
   - `Inbox` 重构(加 `EventType` + `Payload`,去 `IsRead` + `MsgID`)
   - `MessageContent` 加 `RecalledAt` / `EditedAt` / `EditCount` / `ReplyToEventID` / `ClientMsgID`

2. **`repo/` 接口**
   - `MessageRepo`:
     - `SaveMessage` → `SaveMessageContent`
     - 新增 `MarkMessageRecalled`
     - 新增 `UpdateMessageContent`
     - `SaveInbox` → `SaveInboxBatch`(参数改为事件)
     - `GetInboxDelta` 返回类型从 `InboxDeltaItem` 改为 `*model.Inbox`(service 层反序列化 payload)
     - 新增 `GetUnreadCount`
   - 把原先 Inbox 相关的 `IsRead` / `GetUnreadMessages` 删除

3. **repo 实现**
   - 所有 `msg_id` 列名改成 `event_id`
   - Inbox 的写入改成塞 `Payload`(ChatEvent marshal 后的 bytes)
   - 未读数计算改成 JOIN session_member

### 5.3 Service 层改动

1. **`logic/service/helpers.go`**
   - 新增 `BuildInboxItems(ev *ChatEvent, targets []string) []*model.Inbox`(复用于 Task 和 Outbox 补偿场景)

2. **`logic/service/session.go`** `GetSessionList`
   - `last_message` 字段改为 `last_event`(类型 `ChatEvent`)
   - 从 `t_message_content` 查到的消息**重新构造成 `ChatEvent`** 再返回

3. **`logic/service/session.go`** `PullInboxDelta`
   - 查 `t_inbox`,直接把 `Payload` 反序列化为 `ChatEvent` 返回

4. **Task**
   - `dispatcher` 里的 Inbox 写入改成塞 Payload bytes(此时 Task 已经收到 MQEvent,里面有 ChatEvent,直接 Marshal)

### 5.4 验证

- [ ] 新用户发消息、历史拉取、断线补偿正常
- [ ] 未读数计算正确
- [ ] Inbox 索引正确(看执行计划)

---

## 6. Phase 4:Task 单消费者 + 串行化

**目标**:消除 Push/Storage 时序问题,MQ 消费负载 ×2 问题。

### 6.1 任务

1. **配置**:`task/config/config.go` 合并 `StorageConsumer` + `PushConsumer` 为单一 `Consumer` 配置,只保留一个 queue_group。
2. **Dispatcher 重构**:按 `03-services.md` 的 `dispatcher/` 目录拆分,按 payload 类型分派 handler。
3. **Handler 结构**:每个 handler 先做存储/状态变更(失败返回 error 让 Consumer NAK),成功后调用 Pusher(失败只记指标,不 NAK)。
4. **Pusher 保持**:现有 `pusher/` 模块不动,只是调用方式从 `DispatchPush` 变成 handler 内部调用。

### 6.2 验证

- [ ] 发消息后,在 DB Inbox 里能查到 → 然后 WS 收到推送(时序保证)
- [ ] 模拟 DB 挂一瞬:消息会重试到成功,不会推送了但没入库
- [ ] 模拟 Gateway 不可达:Inbox 入库成功,推送失败,重连后从 Inbox 拉取

---

## 7. Phase 5:消息撤回

**目标**:验证事件驱动框架对"非消息"事件的处理。

### 7.1 任务

1. **协议**:`ChatEvent.MessageRecall` 已在 Phase 1 定义,无需改动。
2. **Logic**
   - `event/handler_recall.go` 新建,实现权限校验 + 主表更新 + Outbox 写入
   - `service/chat.go` 的 `SendEvent` 分派逻辑扩展 Recall 分支
3. **Task**
   - `dispatcher/handler_recall.go` 新建,写 Inbox 事件
4. **前端**
   - `ChatEvent.payload.case === "recall"` 时,找到 `target_event_id` 的消息标记为"已撤回"
   - 历史拉取时,`message.recalled_at` 不为空也显示"此消息已撤回"
5. **UI**
   - 长按/右键自己 2 分钟内的消息 → 出"撤回"菜单 → 调用 `SendEvent({recall: {target_event_id}})`

### 7.2 验证

- [ ] 撤回自己的消息:会话双方都看到"已撤回"
- [ ] 尝试撤回别人的消息:服务端返回 PermissionDenied
- [ ] 尝试撤回 3 分钟前的消息:返回 FailedPrecondition(时间窗口)

---

## 8. Phase 6:多端已读同步

**目标**:实现 `ReadReceipt` 事件,为未来多端登录和已读回执铺路。

### 8.1 任务

1. **Logic**
   - `service/session.go` 的 `UpdateReadPosition` 加一步:构造 `ChatEvent{ReadReceipt}` + 写 Outbox + 发 MQ
2. **Task**
   - `dispatcher/handler_read.go` 新建:写 Inbox(只给自己)+ 推送(过滤掉发起端)
3. **前端**
   - 收到 `ChatEvent.payload.case === "read_receipt"` 时更新本地未读数
4. **(可选)** 推送时如何识别"发起端"不给推:
   - 简单方案:给所有自己的在线端都推,前端自己判断当前 tab 是不是刚才那个(通过前端记录最近操作时间戳)

### 8.2 验证

- [ ] 两个 Tab 都开同一个会话 → 一个 Tab 点已读 → 另一个 Tab 未读数自动归零

---

## 9. Phase 7:AI Service V1(非流式)

**目标**:走通 AI 聊天端到端,不做流式。

### 9.1 任务

1. **`t_user` 增加 Bot 用户**:init 阶段插入一个 `username=bot_default` 的 user
2. **`t_session` kind 字段**:创建 AI 会话时 `kind=1`
3. **新建服务** `ai/`
   - `ai.go` 生命周期
   - `consumer/` 订阅 MQ,过滤 `session.kind=1` 的事件(需要 join 一次 session 表判断,或者 MQEvent 里带上 session.kind)
   - `engine/` 封装模型调用(先 hardcode 一个 Provider)
   - `sender/` 调用 Logic 的 `SendEvent`(用 bot 身份)
4. **Logic 允许 bot 身份调用**:AI Service 作为"特殊 Gateway",带 `x-username: bot_xxx` 调 Logic。Logic 侧不需要改动,只要 Bot 是 `session_member` 就有权发消息。
5. **前端**
   - "新建 AI 会话"按钮 → 创建 `SessionType=AI` 的会话
   - 其他 UI 和普通会话完全一致

### 9.2 验证

- [ ] 创建 AI 会话
- [ ] 发消息 → 等几秒 → 收到 AI 回复

---

## 10. Phase 8:AI 流式

**目标**:AI 逐字输出,用户体验像 ChatGPT。

### 10.1 任务

1. **协议**:Phase 1 已定义 `StreamBegin/Chunk/End` 和 `PushStream` RPC,这里实现它们
2. **Gateway**
   - `push/service.go` 的 `PushStream` 实现,封装到 WsPacket 下发
   - `ws/dispatcher.go` 不需要改(客户端不上行流式)
3. **AI Service**
   - 支持 Provider 的 streaming API(OpenAI / Anthropic SDK 都原生支持)
   - `engine/stream.go` 实现:
     - 流开始:分配 `parent_event_id`(Snowflake),调 `PushStream{StreamBegin}`
     - 每个 token/chunk:`PushStream{StreamChunk}`
     - 结束:`Logic.SendEvent(带 event_id)` + `PushStream{StreamEnd}`
4. **前端**
   - `ws/dispatcher` 处理 StreamBegin/Chunk/End
   - UI 层维护"流式消息缓冲区":按 parent_event_id 聚合 chunks,StreamEnd 后等最终 ChatEvent 做替换

### 10.2 验证

- [ ] AI 回复时能看到逐字吐字
- [ ] 断网中断 → 重连 → 从 Inbox 拉到完整最终消息
- [ ] 模型超时 → StreamEnd(ERROR)+ 系统消息提示

---

## 11. 风险与缓解

| 风险 | 可能性 | 影响 | 缓解 |
|------|--------|------|------|
| Proto 破坏性变更前后端不同步 | 中 | 高 | Phase 1 前端后端同时发布,开发前对齐 |
| Inbox 迁移失败数据错乱 | 低 | 高 | 停机前备份,V1 可选择不迁移历史(影响很小) |
| 单消费者性能不够 | 低 | 中 | 通过 worker_count 调大并发 + 指标监控 |
| AI Service 性能/费用失控 | 中 | 中 | 前置限流(Redis 计数)+ 上下文长度控制 |

---

## 12. 关键检查点

每个 Phase 结束后,回到 `04-flows.md` 对照时序图验证:

| Phase | 核心流程 |
|-------|----------|
| 1 | 流程 1(发消息) — 协议层面走通 |
| 3 | 流程 1 + 流程 4(离线补偿) — DB 层面走通 |
| 4 | 流程 1 — 串行化,看 Inbox → Push 顺序 |
| 5 | 流程 2(撤回) |
| 6 | 流程 3(多端已读) |
| 7 | 流程 5(AI) — 不含流式部分 |
| 8 | 流程 5(AI) — 包含流式 |

---

## 13. 文档更新

每完成一个 Phase,更新对应章节:

- 改完 Phase 1~2:`logic/README.md`、`gateway/README.md` 的协议说明
- 改完 Phase 3:`model/README.md`、`repo/README.md` 的表结构和接口
- 改完 Phase 4:`task/README.md` 的消费者模式
- 改完 Phase 7~8:新建 `ai/README.md`

另外维护一份 `docs/architecture/CHANGELOG.md` 记录每个 Phase 的实际完成内容和偏差点,方便日后追溯。

---

## 14. 快速开始(如果今天就动手)

推荐顺序:

1. **周一**:Phase 0 评审,有疑问的地方找我确认
2. **周二~周三**:Phase 1 Proto 改造 + 前后端编译修复
3. **周四**:Phase 2 身份与错误
4. **周五~下周一**:Phase 3 DB 改造
5. **下周二**:Phase 4 Task 单消费者
6. **之后**:按需做 Phase 5~8

Phase 1~4 是一个完整的"骨架改造 Sprint",完成后项目就进入了"可持续扩展"状态。
