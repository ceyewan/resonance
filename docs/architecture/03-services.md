# 微服务设计:Logic / Gateway / Task

> 读之前请先读 `00-overview.md`(分层与职责)、`01-protocol.md`(协议)、`02-database.md`(数据)。

本文档描述三个服务的**代码组织**和**内部模块职责**。每个服务给出:职责边界、目录结构、关键模块、与协议/DB 的对应关系。

---

## 1. Logic 服务

### 1.1 职责边界

**负责**:
- 业务规则与权限判断(谁能发消息、谁能撤回、谁能入群)
- 生成 `ChatEvent`(分配 event_id、seq_id、timestamp)
- Outbox 事务写入(保证业务操作和 MQ 投递一致性)
- 发布 MQ(异步 + 兜底补偿)
- 同步 RPC 响应(给 Gateway 返回 event_id/seq_id 做 Ack)

**不负责**:
- 推送(Task 负责)
- 写 Inbox(Task 负责)
- WebSocket 连接管理(Gateway)
- 协议解析(Gateway)

### 1.2 目录结构

```
logic/
├── logic.go                    # 生命周期管理、组件组装
├── config/
│   └── config.go
├── server/
│   ├── grpc.go                 # gRPC Server + 拦截器(日志/恢复/鉴权)
│   └── interceptor_auth.go     # ★新增:从 metadata 解出 x-username 放 context
├── service/
│   ├── interfaces.go           # Service 接口定义
│   ├── auth.go                 # AuthService
│   ├── session.go              # SessionService
│   ├── chat.go                 # ChatService.SendEvent(统一事件入口)
│   ├── chat_handler.go         # ★新增:按 event payload 类型分派处理
│   ├── presence.go             # PresenceService
│   └── helpers.go              # 工具函数(PublishToMQ / BuildChatEvent 等)
├── event/                      # ★新增:ChatEvent 构建与处理模块
│   ├── builder.go              # 从 SendEventRequest 构建 ChatEvent
│   ├── persister.go            # 持久化(写主表 + 写 Outbox)
│   └── handler_*.go            # 不同 payload 类型的处理器
├── job/
│   └── outbox.go               # Outbox 补偿 Worker
├── observability/
│   ├── observability.go
│   └── config.go
└── README.md
```

**关键变化**:
- 新增 `event/` 目录,承载 ChatEvent 相关的核心业务逻辑。
- `service/chat.go` 从"处理消息"升级为"处理所有客户端发起的事件"。
- `server/interceptor_auth.go` 统一把身份从 metadata 抽出,service 层从 context 拿。

### 1.3 核心模块职责

#### `event/builder.go`

```go
// BuildChatEvent 根据 SendEventRequest 和当前用户构造完整的 ChatEvent
// 职责:分配 event_id(Snowflake)、seq_id(会话 CAS)、timestamp、会话元数据
func BuildChatEvent(
    ctx context.Context,
    req *logicv1.SendEventRequest,
    fromUsername string,   // 从 context 拿
    sessionRepo repo.SessionRepo,
    idgen idgen.Snowflake,
) (*commonv1.ChatEvent, error)
```

#### `event/persister.go`

```go
// PersistEvent 把 ChatEvent 持久化 + 投 MQ
// - 如果 payload 是 Message,写 t_message_content
// - 所有事件都写 t_message_outbox
// - 事务内完成
// - 事务外发 MQ(异步 + Outbox 补偿)
type EventPersister struct {
    msgRepo repo.MessageRepo
    mq      mq.Publisher
}

func (p *EventPersister) PersistAndPublish(ctx context.Context, ev *commonv1.ChatEvent, targetUsernames []string) error
```

关键点:
- **所有事件**(不只是消息)都经过 Outbox,保证投递可靠。
- 撤回/编辑事件需要在事务内**同时更新主表**(标记 recalled_at / 更新 content)。

#### `event/handler_*.go`

按 oneof payload 类型分文件:

```go
// event/handler_message.go
func HandleMessage(ctx context.Context, ev *commonv1.ChatEvent, deps Deps) error {
    // 1. 权限校验:from_username 是否会话成员
    // 2. persister.PersistAndPublish(ev, sessionMembers)
}

// event/handler_recall.go
func HandleRecall(ctx context.Context, ev *commonv1.ChatEvent, deps Deps) error {
    // 1. 权限校验:
    //    - target_event_id 存在 且 sender == from_username (只能撤自己的)
    //    - 单聊/群聊撤回时间窗口(可选:2 分钟内)
    // 2. 事务:
    //    - t_message_content SET recalled_at = NOW()
    //    - 写 Outbox
    // 3. 发 MQ
}

// event/handler_edit.go  同理
```

#### `service/chat.go`

```go
func (s *ChatService) SendEvent(ctx context.Context, req *logicv1.SendEventRequest) (*logicv1.SendEventResponse, error) {
    fromUsername := MustUsernameFromCtx(ctx)

    // 1. 构建 ChatEvent(分配 id/seq/timestamp)
    ev, err := event.BuildChatEvent(ctx, req, fromUsername, s.sessionRepo, s.idgen)
    if err != nil { return nil, err }

    // 2. 按 payload 类型分派
    switch p := ev.Payload.(type) {
    case *commonv1.ChatEvent_Message:
        err = event.HandleMessage(ctx, ev, s.deps)
    case *commonv1.ChatEvent_Recall:
        err = event.HandleRecall(ctx, ev, s.deps)
    case *commonv1.ChatEvent_Edit:
        err = event.HandleEdit(ctx, ev, s.deps)
    default:
        return nil, status.Errorf(codes.InvalidArgument, "unsupported event payload")
    }
    if err != nil { return nil, err }

    // 3. 返回 Ack
    return &logicv1.SendEventResponse{
        EventId: ev.EventId,
        SeqId: ev.SeqId,
        TimestampMs: ev.TimestampMs,
    }, nil
}
```

#### `service/session.go` 改动

```go
// UpdateReadPosition 现在要做两件事
func (s *SessionService) UpdateReadPosition(ctx context.Context, req *logicv1.UpdateReadPositionRequest) (*logicv1.UpdateReadPositionResponse, error) {
    username := MustUsernameFromCtx(ctx)

    // 1. 更新 session_member.last_read_seq(现有逻辑)
    err := s.sessionRepo.UpdateLastReadSeq(ctx, req.SessionId, username, req.ReadUptoSeq)
    if err != nil { return nil, err }

    // 2. ★新增:生成 ReadReceipt 事件,投 MQ
    //    目的是让同一用户的其他端知道这里已读了
    //    target_usernames = [username](只给自己所有端投)
    readEv := event.BuildReadReceiptEvent(ctx, req.SessionId, username, req.ReadUptoSeq)
    _ = s.persister.PersistAndPublish(ctx, readEv, []string{username})
    // 注意:失败不阻塞主流程,只记日志

    // 3. 返回更新后未读数
    unread, _ := s.msgRepo.GetUnreadCount(ctx, username, req.SessionId)
    return &logicv1.UpdateReadPositionResponse{UnreadCount: unread}, nil
}
```

---

## 2. Gateway 服务

### 2.1 职责边界

**负责**:
- HTTP/ConnectRPC 路由与鉴权中间件
- WebSocket 连接升级、读写循环、心跳
- 协议编解码(protobuf)
- 转发 RPC 到 Logic(把 JWT 解出的 username 放 metadata)
- 接收 Task/AI 的 gRPC Push,推送到 WebSocket
- 在线状态批量上报到 Logic

**不负责**:
- 业务决策
- 任何持久化

### 2.2 目录结构

```
gateway/
├── gateway.go
├── config/
├── server/
│   ├── http.go                 # ConnectRPC Server
│   └── grpc.go                 # gRPC Server(PushService)
├── api/                        # ConnectRPC handlers
│   ├── auth.go
│   ├── session.go              # Session/History/Inbox/Read/Contact
│   └── routes.go
├── middleware/
│   ├── auth.go                 # JWT 解析,把 username 塞 context
│   ├── cors.go
│   ├── logger.go
│   ├── recovery.go
│   ├── trace.go
│   └── ratelimit.go
├── ws/
│   ├── upgrader.go
│   └── dispatcher.go           # ★重构:按 WsPacket oneof 分派,不只是 Chat/Pulse/Ack
├── connection/                 # 不变
├── push/
│   ├── service.go              # ★重构:PushEvent + PushStream 两个 RPC
│   └── stream_buffer.go        # ★新增:流式 chunk 的短暂缓存与下发
├── protocol/
│   └── codec.go
├── client/
│   ├── client.go
│   ├── services.go             # 调用 Logic 的封装,自动注入 x-username metadata
│   └── batcher.go
├── observability/
└── README.md
```

### 2.3 核心模块职责

#### `middleware/auth.go`

```go
// HTTP 和 WS 共用:解析 Authorization Header 的 JWT,username 放 context
func JWTAuth(authenticator auth.Authenticator) gin.HandlerFunc
```

#### `client/services.go`

**关键**:所有调用 Logic 的地方,必须通过这里封装,保证 `x-username` metadata 被注入。

```go
func (c *LogicClient) SendEvent(ctx context.Context, req *logicv1.SendEventRequest) (*logicv1.SendEventResponse, error) {
    username := MustUsernameFromCtx(ctx)
    md := metadata.Pairs("x-username", username)
    ctx = metadata.NewOutgoingContext(ctx, md)
    return c.chatClient.SendEvent(ctx, req)
}
```

#### `ws/dispatcher.go`(重构)

旧:只处理 Chat / Pulse / Ack。新:按 `WsPacket.payload` oneof 分派:

```go
func (d *Dispatcher) Handle(ctx context.Context, conn *Conn, pkt *gatewayv1.WsPacket) error {
    switch p := pkt.Payload.(type) {
    case *gatewayv1.WsPacket_Pulse:
        return d.handlePulse(ctx, conn)
    case *gatewayv1.WsPacket_Ack:
        return d.handleAck(ctx, conn, p.Ack)
    case *gatewayv1.WsPacket_ChatRequest:
        return d.handleChatRequest(ctx, conn, p.ChatRequest, pkt.ClientSeq)
    default:
        // 下行事件不应该从客户端来
        return errors.New("unexpected packet type from client")
    }
}
```

#### `push/service.go`(重构)

```go
type PushService struct {
    connMgr   *connection.Manager
    streamBuf *StreamBuffer
}

// 持久化事件推送(Task 调用)
func (s *PushService) PushEvent(ctx context.Context, req *gatewayv1.PushEventRequest) (*gatewayv1.PushEventResponse, error) {
    pkt := &gatewayv1.WsPacket{
        Payload: &gatewayv1.WsPacket_Event{Event: req.Event},
    }
    failed := s.connMgr.SendToUsers(req.ToUsernames, pkt)
    return &gatewayv1.PushEventResponse{
        EventId: req.Event.EventId,
        FailedUsernames: failed,
    }, nil
}

// 流式短暂事件推送(AI Service 调用)
func (s *PushService) PushStream(ctx context.Context, req *gatewayv1.PushStreamRequest) (*gatewayv1.PushStreamResponse, error) {
    pkt := buildStreamPacket(req)
    failed := s.connMgr.SendToUsers(req.ToUsernames, pkt)
    return &gatewayv1.PushStreamResponse{FailedUsernames: failed}, nil
}
```

---

## 3. Task 服务

### 3.1 职责边界

**负责**:
- 消费 MQ(NATS)
- 按 `ChatEvent.payload` 类型**分发处理**(写 Inbox、更新主表、更新会话状态)
- 查询用户路由
- 推送到对应 Gateway

**不负责**:
- 任何业务决策(权限、时间窗口等都由 Logic 保证)
- 生成 event_id/seq_id(Logic 已分配)

### 3.2 目录结构

```
task/
├── task.go
├── config/
├── consumer/
│   └── consumer.go             # 通用 MQ Consumer,注入 handler(保持)
├── dispatcher/
│   ├── dispatcher.go           # ★重构:单入口 handler,按 payload 分派
│   ├── handler_message.go      # 处理 Message 事件
│   ├── handler_recall.go       # 处理 Recall 事件
│   ├── handler_edit.go
│   ├── handler_read.go
│   └── handler_session.go
├── pusher/                     # 不变
│   ├── manager.go
│   ├── client.go
│   └── interface.go
├── observability/
└── README.md
```

**重要变化**:**从双消费者改为单消费者**(`00-overview.md` 5.1 节)。

### 3.3 核心模块职责

#### `dispatcher/dispatcher.go`

```go
// 单入口:先存储,后推送,串行
func (d *Dispatcher) Handle(ctx context.Context, mqEvent *mqv1.MQEvent) error {
    ev := mqEvent.Event

    // 1. 按事件类型做持久化/状态变更
    switch ev.Payload.(type) {
    case *commonv1.ChatEvent_Message:
        if err := d.handleMessage(ctx, ev, mqEvent.TargetUsernames); err != nil {
            return err   // NAK 重试
        }
    case *commonv1.ChatEvent_Recall:
        if err := d.handleRecall(ctx, ev, mqEvent.TargetUsernames); err != nil {
            return err
        }
    case *commonv1.ChatEvent_ReadReceipt:
        if err := d.handleReadReceipt(ctx, ev, mqEvent.TargetUsernames); err != nil {
            return err
        }
    // ...
    }

    // 2. 推送给在线用户(失败不 NAK,靠 Inbox 兜底)
    d.pushToGateways(ctx, ev, mqEvent.TargetUsernames)
    return nil
}
```

#### `dispatcher/handler_message.go`

```go
func (d *Dispatcher) handleMessage(ctx context.Context, ev *commonv1.ChatEvent, targets []string) error {
    // 所有成员的 Inbox 都写一条
    inboxes := make([]*model.Inbox, 0, len(targets))
    payload, _ := proto.Marshal(ev)
    for _, user := range targets {
        inboxes = append(inboxes, &model.Inbox{
            OwnerUsername: user,
            SessionID:     ev.SessionId,
            SeqID:         ev.SeqId,
            EventID:       ev.EventId,
            EventType:     int(EventTypeMessage),
            Payload:       payload,
        })
    }
    return d.msgRepo.SaveInboxBatch(ctx, inboxes)
}
```

#### `dispatcher/handler_recall.go`

```go
func (d *Dispatcher) handleRecall(ctx context.Context, ev *commonv1.ChatEvent, targets []string) error {
    recall := ev.GetRecall()

    // 1. 更新主表:标记 recalled_at
    if err := d.msgRepo.MarkMessageRecalled(ctx, recall.TargetEventId, time.Now()); err != nil {
        return err
    }

    // 2. 给所有成员 Inbox 写一条"撤回事件"(让客户端感知到撤回)
    inboxes := buildInboxesForEvent(ev, targets, EventTypeRecall)
    return d.msgRepo.SaveInboxBatch(ctx, inboxes)
}
```

#### `dispatcher/handler_read.go`

```go
func (d *Dispatcher) handleReadReceipt(ctx context.Context, ev *commonv1.ChatEvent, targets []string) error {
    // 已读事件:session_member.last_read_seq 已由 Logic 更新了
    // Task 只需要把事件写进 Inbox,让该用户的其他端感知
    // targets 通常只包含 from_username 自己
    inboxes := buildInboxesForEvent(ev, targets, EventTypeReadReceipt)
    return d.msgRepo.SaveInboxBatch(ctx, inboxes)
}
```

#### `pusher/`

不变。接收 `ChatEvent`,调用 Gateway `PushEvent` RPC。

---

## 4. 服务间契约总结

| 调用方 | 被调用方 | 接口 | 身份传递 |
|--------|----------|------|----------|
| Web | Gateway | ConnectRPC(HTTP) | `Authorization: Bearer xxx` Header |
| Web | Gateway | WebSocket | WS 握手时 token 参数 |
| Gateway | Logic | gRPC | `x-username` metadata |
| Logic | NATS | MQEvent | trace_headers |
| NATS | Task | MQEvent | trace_headers 解出 |
| Task | Gateway | gRPC PushEvent | 无需身份(内部信任) |
| AI Service | Gateway | gRPC PushStream | 无需身份(内部信任) |
| AI Service | Logic | gRPC SendEvent | `x-username: bot_xxx` metadata |

---

## 5. 各服务的 README 更新指引

实施完成后,对应 README 需要更新以下内容:

**`logic/README.md`**:
- 新增 `event/` 模块的说明
- `ChatService.SendEvent` 统一事件入口的说明
- 各 payload 类型的处理流程(handler_*)

**`gateway/README.md`**:
- WsPacket 新的 oneof 结构
- `PushService` 两个 RPC 的区分(PushEvent 持久化 / PushStream 短暂)
- Stream 缓冲与下发机制

**`task/README.md`**:
- 从双消费者改为单消费者的说明
- Dispatcher 按事件类型分发的流程图
- 各 handler 的职责

下一步看 `04-flows.md`,用时序图把上面各模块的联动看一遍。
