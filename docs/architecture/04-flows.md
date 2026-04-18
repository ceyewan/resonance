# 核心流程设计

> 本文档用时序图描述关键业务流程。每个流程都附带:成功路径、失败处理、协议/表的关键写入点。

---

## 1. 流程 1:发送普通消息

**入口**:用户在 Web 发了一条文本。

```
Web                 Gateway              Logic                 NATS            Task              Gateway'(其他在线用户)
 │                    │                    │                    │              │                    │
 │ WS: WsPacket       │                    │                    │              │                    │
 │  {ChatRequest,     │                    │                    │              │                    │
 │   client_seq=101}  │                    │                    │              │                    │
 │───────────────────▶│                    │                    │              │                    │
 │                    │ gRPC SendEvent     │                    │              │                    │
 │                    │  md: x-username=A  │                    │              │                    │
 │                    │───────────────────▶│                    │              │                    │
 │                    │                    │ 1. BuildChatEvent   │              │                    │
 │                    │                    │    (Snowflake,seq) │              │                    │
 │                    │                    │                    │              │                    │
 │                    │                    │ 2. 事务:            │              │                    │
 │                    │                    │   INSERT content   │              │                    │
 │                    │                    │   INSERT outbox    │              │                    │
 │                    │                    │   UPDATE max_seq   │              │                    │
 │                    │                    │                    │              │                    │
 │                    │                    │ 3. Publish MQEvent  │              │                    │
 │                    │                    │───────────────────▶│              │                    │
 │                    │                    │                    │ deliver      │                    │
 │                    │                    │                    │─────────────▶│                    │
 │                    │ SendEventResp       │                    │              │                    │
 │                    │  {event_id,seq_id}  │                    │              │                    │
 │                    │◀───────────────────│                    │              │                    │
 │ WS: Ack            │                    │                    │              │                    │
 │  {ref=101,         │                    │                    │              │                    │
 │   event_id,seq_id} │                    │              ┌─────▼─────┐        │                    │
 │◀───────────────────│                    │              │ Dispatcher │        │                    │
 │                    │                    │              │           │        │                    │
 │                    │                    │              │ ① 存储:    │        │                    │
 │                    │                    │              │   SaveInbox│        │                    │
 │                    │                    │              │   (批量写  │        │                    │
 │                    │                    │              │    所有成员)│        │                    │
 │                    │                    │              │           │        │                    │
 │                    │                    │              │ ② 推送:    │        │                    │
 │                    │                    │              │   PushEvent│        │                    │
 │                    │                    │              └─────┬─────┘        │                    │
 │                    │                    │                    │              │ gRPC PushEvent      │
 │                    │                    │                    │              │(ChatEvent + users) │
 │                    │                    │                    │              │───────────────────▶│
 │                    │                    │                    │              │                    │ WS: WsPacket
 │                    │                    │                    │              │                    │  {event: ChatEvent}
 │                    │                    │                    │              │                    │─────────▶ Web(B)
```

### 关键点

- **同步 Ack**:发送方的 WS Ack 是 Logic 响应后 Gateway 构造的,**不等 Task 完成**。收到 Ack 表示"服务端已持久化",Task 处理是异步的。
- **串行存储→推送**:Task 一定先写完 Inbox 才推送,保证接收方收到推送后重连能在 Inbox 找到消息。
- **发送方本人也在 target_usernames 里**:这样发送方的其他端(多设备)也能通过 Inbox 看到自己发的消息。

### 失败处理

| 失败点 | 表现 | 处理 |
|--------|------|------|
| Logic 事务失败 | SendEvent 返回 error | Gateway 回 WS error(或关闭连接让客户端重连重发) |
| MQ Publish 失败 | Logic 事务已提交,消息在 Outbox | Outbox Worker 定时补发 |
| Task 写 Inbox 失败 | NAK 重试 | 靠 NATS 重投 |
| Task 推送失败 | 用户未在线或 Gateway 连接断 | 不 NAK,用户重连时从 Inbox 拉 |
| Gateway 推送到接收方 WS 失败 | 接收方在线但连接不稳 | 靠接收方重连 + PullInboxDelta 兜底 |

---

## 2. 流程 2:撤回消息

**入口**:用户长按自己 2 分钟内发的消息,选择"撤回"。

```
Web                 Gateway              Logic                 NATS         Task
 │                    │                    │                    │           │
 │ WS: ChatRequest?  不走 WS              │                    │           │
 │ 用 HTTP RPC 撤回(也可以走 WS,但撤回是偶发,走 RPC 更简单)│           │
 │                    │                    │                    │           │
 │ HTTP: SendEvent   │                    │                    │           │
 │  {payload=Recall,  │                    │                    │           │
 │   target_event=X}  │                    │                    │           │
 │───────────────────▶│                    │                    │           │
 │                    │ gRPC SendEvent     │                    │           │
 │                    │───────────────────▶│                    │           │
 │                    │                    │ 1. 权限校验:       │           │
 │                    │                    │   - X 是否存在    │           │
 │                    │                    │   - X.sender == A │           │
 │                    │                    │   - 时间窗 < 2min  │           │
 │                    │                    │                    │           │
 │                    │                    │ 2. 事务:            │           │
 │                    │                    │   UPDATE content   │           │
 │                    │                    │     SET recalled_at│           │
 │                    │                    │   INSERT outbox    │           │
 │                    │                    │     (ChatEvent{    │           │
 │                    │                    │       Recall})     │           │
 │                    │                    │                    │           │
 │                    │                    │ 3. Publish MQ       │           │
 │                    │                    │───────────────────▶│           │
 │                    │ SendEventResp       │                    │ deliver   │
 │                    │◀───────────────────│                    │──────────▶│
 │ HTTP 200           │                    │                    │           │
 │◀───────────────────│                    │              ┌─────▼────────┐  │
 │                    │                    │              │ handleRecall │  │
 │                    │                    │              │             │  │
 │                    │                    │              │ ① MarkMsg   │  │
 │                    │                    │              │    Recalled │  │
 │                    │                    │              │   (冗余,Logic│  │
 │                    │                    │              │    已经改过)│  │
 │                    │                    │              │             │  │
 │                    │                    │              │ ② SaveInbox  │  │
 │                    │                    │              │   (所有成员  │  │
 │                    │                    │              │    收到撤回 │  │
 │                    │                    │              │    事件)    │  │
 │                    │                    │              │             │  │
 │                    │                    │              │ ③ Push       │  │
 │                    │                    │              └──────────────┘  │
 │                    │                    │                    │           │
 │                    │                    │                    │    (推送到所有在线成员)
```

### 关键点

- **撤回通过 HTTP RPC 发起**,不走 WS(偶发操作,走 RPC 简单)。但后端协议是同一个 `SendEvent`。
- **主表更新 + 事件入 Inbox 二选一?——两者都要**:
  - 主表改 `recalled_at`:**历史拉取**时客户端根据此字段决定显示"此消息已撤回"
  - Inbox 写一条 Recall 事件:**实时推送**时让在线客户端知道"刚刚有人撤回了"
- **幂等**:Task handler 对同一个 event_id 可能重复消费(MQ at-least-once),主表 UPDATE 是幂等的,Inbox 唯一约束 `(owner, session, seq)` 防止重复写入。

---

## 3. 流程 3:多端已读同步

**场景**:用户 A 在 Web 端读到会话 S 的 seq=88。A 的手机客户端(假设未来有)应该看到未读数变为 0。

```
Web(A1 - Web)        Gateway             Logic                 NATS         Task            Web(A2 - 手机)
 │                    │                    │                    │           │                │
 │ HTTP UpdateRead    │                    │                    │           │                │
 │  {session=S,read=88}│                   │                    │           │                │
 │───────────────────▶│                    │                    │           │                │
 │                    │ gRPC UpdateRead     │                    │           │                │
 │                    │───────────────────▶│                    │           │                │
 │                    │                    │ 1. UPDATE           │           │                │
 │                    │                    │   session_member   │           │                │
 │                    │                    │   SET last_read=88 │           │                │
 │                    │                    │                    │           │                │
 │                    │                    │ 2. 构建 ChatEvent   │           │                │
 │                    │                    │   {ReadReceipt=88} │           │                │
 │                    │                    │   targets=[A]     │           │                │
 │                    │                    │                    │           │                │
 │                    │                    │ 3. 事务:           │           │                │
 │                    │                    │   INSERT outbox    │           │                │
 │                    │                    │                    │           │                │
 │                    │                    │ 4. Publish MQ       │           │                │
 │                    │                    │───────────────────▶│ deliver   │                │
 │                    │ UpdateReadResp      │                    │──────────▶│                │
 │                    │  {unread=0}         │                    │           │                │
 │                    │◀───────────────────│                    │           │                │
 │◀───────────────────│                    │                    │    ┌──────▼──────┐         │
 │                    │                    │                    │    │handleRead-  │         │
 │                    │                    │                    │    │ Receipt     │         │
 │                    │                    │                    │    │             │         │
 │                    │                    │                    │    │ SaveInbox   │         │
 │                    │                    │                    │    │ (只写 A 的) │         │
 │                    │                    │                    │    │             │         │
 │                    │                    │                    │    │ Push to A2  │         │
 │                    │                    │                    │    │ (不给 A1,   │         │
 │                    │                    │                    │    │  A1 发起方) │         │
 │                    │                    │                    │    └──────┬──────┘         │
 │                    │                    │                    │           │ WS ChatEvent   │
 │                    │                    │                    │           │{ReadReceipt=88}│
 │                    │                    │                    │           │───────────────▶│
                                                                                             │
                                                                           A2 根据此事件更新本地未读计数
```

### 关键点

- **UpdateReadPosition 的主事务**(改 `last_read_seq`)是同步返回的,**响应里的 unread=0 就是基于它算的**。
- **MQ 发布 ReadReceipt** 是次要流程,失败不影响主响应(在 Logic 里 catch 错误只记日志)。
- **target_usernames 只包含自己**,不扩散到其他成员。其他成员不需要知道"A 读到了哪"(除非要做"已读回执小蓝勾"那样的功能,V1 不考虑)。
- Task 的 handleReadReceipt **不推给事件的发起方**(避免 A1 自己又收到自己发的已读)。需要 Push 层按 `from_username` 过滤。

### 未来扩展:已读回执(对方看到"已读")

如果要做"B 看到 A 已读"(单聊已读标记、群聊已读数):

- `target_usernames` 扩展到会话所有成员
- 其他成员的客户端收到 `ReadReceipt` 后,在 A 最后一条已读消息上显示"已读"标记
- 无需改协议,只需改 Logic 里构造 MQEvent 时的 targets

---

## 4. 流程 4:离线补偿(PullInboxDelta)

**场景**:用户断网 10 分钟,期间收到 5 条消息 + 1 条撤回。重连后拉增量。

```
Web                 Gateway             Logic              Inbox 表
 │                    │                    │                    │
 │ WS 重连成功         │                    │                    │
 │ 本地有 cursor=100   │                    │                    │
 │                    │                    │                    │
 │ HTTP PullInboxDelta│                    │                    │
 │  {cursor_id=100,    │                    │                    │
 │   limit=500}       │                    │                    │
 │───────────────────▶│                    │                    │
 │                    │ gRPC PullInboxDelta │                    │
 │                    │───────────────────▶│                    │
 │                    │                    │ SELECT * FROM      │
 │                    │                    │ t_inbox            │
 │                    │                    │ WHERE owner=A      │
 │                    │                    │   AND id > 100     │
 │                    │                    │ ORDER BY id        │
 │                    │                    │ LIMIT 500          │
 │                    │                    │───────────────────▶│
 │                    │                    │◀───────────────────│
 │                    │                    │ (6 条记录,含 5 条 │
 │                    │                    │  Message + 1 条    │
 │                    │                    │  Recall)           │
 │                    │ PullInboxDeltaResp │                    │
 │                    │  {items: [         │                    │
 │                    │    {inbox_id=101,  │                    │
 │                    │     event=...},... │                    │
 │                    │   ],               │                    │
 │                    │   next_cursor=106, │                    │
 │                    │   has_more=false}  │                    │
 │                    │◀───────────────────│                    │
 │◀───────────────────│                    │                    │
 │                    │                    │                    │
 │ 客户端按 event.payload 类型分发渲染:    │                    │
 │  - Message: 插入消息列表                │                    │
 │  - Recall: 标记目标消息为已撤回         │                    │
 │ 本地 cursor=106                         │                    │
```

### 关键点

- **Inbox 是唯一事实**:无论消息、撤回、已读,全在 Inbox 按时间顺序。客户端收到后按 `payload.case` 分发处理逻辑。
- **cursor 只看 Inbox.id**:单调递增,幂等,天然支持断点续传。
- **has_more 机制**:limit 不够时客户端继续拉,直到 has_more=false。

---

## 5. 流程 5:AI 聊天 + 流式响应(未来)

**场景**:用户和 AI Bot 对话,AI 逐字吐出回复。

```
Web(用户A)      Gateway          Logic          NATS         Task         AI Service     Gateway
  │              │                │              │            │               │              │
  │ 发消息给 Bot  │                │              │            │               │              │
  │────────────▶│ SendEvent      │              │            │               │              │
  │              │───────────────▶│              │            │               │              │
  │              │                │ 正常流程:     │            │               │              │
  │              │                │ 写库 + 发 MQ │            │               │              │
  │              │                │───────────▶│            │               │              │
  │◀── Ack ──────│◀───────────────│            │── deliver ─▶│               │              │
  │              │                │            │            │               │              │
  │              │                │            │            │ 写 A 的 Inbox  │              │
  │              │                │            │            │ (让 A 在其他端 │              │
  │              │                │            │            │  也能看到)     │              │
  │              │                │            │            │               │              │
  │              │                │            │            │            ┌──▼─────┐        │
  │              │                │            │            │            │AI订阅   │        │
  │              │                │            │            │            │MQ过滤   │        │
  │              │                │            │            │            │AI会话   │        │
  │              │                │            │            │            └──┬─────┘        │
  │              │                │            │            │               │              │
  │              │                │            │            │               │ 1.分配 event_id│
  │              │                │            │            │               │   for 最终回复 │
  │              │                │            │            │               │                │
  │              │                │            │            │               │ 2.调用模型开始 │
  │              │                │            │            │               │   流式输出     │
  │              │                │            │            │               │                │
  │              │                │            │            │               │ PushStream     │
  │              │                │            │            │               │  {StreamBegin, │
  │              │                │            │            │               │   users=[A]}  │
  │              │                │            │            │               │──────────────▶│
  │◀─── WS: StreamBegin {parent_event_id=X} ──────────────────────────────────────────────│
  │              │                │            │            │               │                │
  │              │                │            │            │               │ (loop, 每个 token)
  │              │                │            │            │               │ PushStream     │
  │              │                │            │            │               │  {StreamChunk, │
  │              │                │            │            │               │   seq=i,delta} │
  │              │                │            │            │               │──────────────▶│
  │◀─── WS: StreamChunk x N (前端边收边拼接显示) ─────────────────────────────────────────│
  │              │                │            │            │               │                │
  │              │                │            │            │               │ 3.模型完成     │
  │              │                │            │            │               │                │
  │              │                │            │            │               │ 4.SendEvent    │
  │              │                │            │            │               │   (以 Bot 身份)│
  │              │                │            │            │               │   {event_id=X, │
  │              │                │            │            │               │    Message=    │
  │              │                │            │            │               │    完整内容}   │
  │              │                │            │            │               │───────────────▶Logic
  │              │                │            │            │               │                │
  │              │                │            │            │               │   (走正常写库+ │
  │              │                │            │            │               │    MQ链路)     │
  │              │                │            │            │               │                │
  │              │                │            │            │               │ 5.PushStream   │
  │              │                │            │            │               │   {StreamEnd}  │
  │              │                │            │            │               │──────────────▶│
  │◀─── WS: StreamEnd {parent_event_id=X} ──────────────────────────────────────────────────│
  │              │                │            │            │               │                │
  │ 最终 Task 走常规流程,A 的 Inbox 里多一条 Message 事件    │               │                │
  │◀─── WS: ChatEvent{Message, event_id=X, 完整内容} ──────────────────────────────────────│
  │              │                │            │            │               │                │
  │ 前端用这条最终消息**替换**流式拼接的临时内容,以 Inbox 为准 │              │                │
```

### 关键设计点

1. **event_id 提前分配**:AI Service 在流开始前就分配好最终消息的 event_id(Snowflake),流式 chunk 都带这个 parent_event_id,客户端能把它们关联起来。
2. **流式 chunk 不走 MQ,不入 Inbox**:避免给数据库造成压力。断线期间的 chunk 丢了也无所谓,重连后从 Inbox 能拿到最终消息。
3. **最终消息走正常链路**:AI 生成完毕后,以 Bot 身份调 `Logic.SendEvent` 写一条 Message,**复用所有现有流程**(权限、幂等、写扩散、推送)。
4. **前端的缓冲逻辑**:
   - 收到 `StreamBegin(event_id=X)`:在 UI 新建一条 placeholder 消息,id=X,内容为空
   - 收到 `StreamChunk(parent=X, delta=...)`:追加 delta 到 placeholder
   - 收到 `StreamEnd(parent=X)`:停止拼接,等待最终 ChatEvent
   - 收到 `ChatEvent{Message, event_id=X}`:用它替换 placeholder 的内容(保证最终一致)
5. **路由与权限**:
   - Bot 是真实的 User(`t_user` 里有一行),会话是普通会话,`session.kind=1` 标记为 AI 会话
   - AI Service 作为 MQ 消费者过滤 `kind=1` 的会话事件
   - AI Service 通过 Logic 的 `SendEvent` 回复,需要一个内部的 x-username="bot_xxx"

### 失败处理

| 失败点 | 表现 | 处理 |
|--------|------|------|
| AI 调用模型失败 | 前端只看到 Begin,没有 Chunk | AI Service 发 StreamEnd(reason=ERROR)+ 通过 Logic 发一条系统消息告知"AI 异常" |
| 中途断网 | Web 丢失 chunk | 重连后 PullInboxDelta 拿到最终 Message,自然覆盖 |
| 最终 SendEvent 失败 | 已推了 chunk 但没持久化 | AI Service 重试 SendEvent;超时后发 StreamEnd(ERROR) |

---

## 6. 流程对照表

各流程的协议/表写入点速查:

| 流程 | 触发 RPC | Logic 主表变更 | Logic Outbox | Task Inbox 写入 | Task 推送目标 |
|------|----------|---------------|--------------|-----------------|----------------|
| 发消息 | `SendEvent{Message}` | INSERT message_content, UPDATE session.max_seq | ChatEvent{Message} | 全体成员 | 全体在线成员 |
| 撤回 | `SendEvent{Recall}` | UPDATE message_content.recalled_at | ChatEvent{Recall} | 全体成员 | 全体在线成员 |
| 编辑 | `SendEvent{Edit}` | UPDATE message_content.content/edited_at | ChatEvent{Edit} | 全体成员 | 全体在线成员 |
| 已读(多端同步) | `UpdateReadPosition` | UPDATE session_member.last_read_seq | ChatEvent{ReadReceipt} | 仅自己 | 自己除当前发起端外 |
| 会话更名 | (专用 RPC) | UPDATE session.name | ChatEvent{SessionUpdate} | 全体成员 | 全体在线成员 |
| AI 流式 chunk | (AI Service 直推 Gateway) | 无 | 无 | 无 | 目标用户 |
| AI 最终消息 | AI Service → `SendEvent{Message}` | 同"发消息" | 同"发消息" | 同"发消息" | 同"发消息" |

---

## 7. 正确性的关键约束

1. **Inbox 与 last_read_seq 保持同步**:客户端显示未读数 = `Inbox 里 session=S 的最大 seq_id` 和 `last_read_seq` 的差值。
2. **Outbox 与主表变更同事务**:Logic 内任何业务变更 + 事件都要在一个 DB 事务里,失败全滚。
3. **seq_id 严格递增**:用 CAS 保证(`WHERE max_seq_id = ?`),避免并发发消息时 seq 重复。
4. **event_id 全局唯一**:Snowflake 保证。
5. **Inbox `(owner, session, seq)` 唯一**:保证 MQ at-least-once 下写扩散的幂等。
6. **历史拉取要区分事件类型**:`GetHistoryEvents` 返回的是 `t_message_content` 里的消息(加 recalled_at/edited_at 字段让前端感知状态),**不返回**撤回/已读这类元事件(那些只通过 Inbox 流获取)。

下一步看 `05-migration.md`,了解如何从现状逐步演进到这套设计。
