# 协议设计:Proto 目录与 ChatEvent

> 读之前请先读 `00-overview.md` 的第 3 节(ChatEvent 抽象)和第 1 节(设计原则)。

本文档定义所有 Proto 文件的目录结构、消息定义与 RPC 接口,是实现阶段的**一手参考**。

---

## 1. 目录总览

```
api/proto/
├── common/v1/                     # 跨服务复用的基础类型
│   ├── types.proto                # User
│   ├── session.proto              # SessionMeta, SessionType (enum)
│   ├── message.proto              # Message, MessageType (enum)
│   ├── event.proto                # ★ ChatEvent (oneof) - 核心载体
│   ├── view.proto                 # SessionInfo / ContactInfo / InboxEvent 视图 DTO
│   └── options.proto              # protobuf 自定义选项(default_topic 等)
│
├── gateway/v1/                    # Web ↔ Gateway 对外协议
│   ├── packet.proto               # WsPacket(上行下行的 oneof)
│   ├── auth.proto                 # ConnectRPC: AuthService
│   ├── session.proto              # ConnectRPC: SessionService
│   └── push.proto                 # Task/AI → Gateway 的 gRPC(Push + StreamPush)
│
├── logic/v1/                      # Gateway → Logic 的内部协议
│   ├── auth.proto                 # AuthService
│   ├── chat.proto                 # ChatService: SendEvent(统一事件入口)
│   ├── session.proto              # SessionService(不再 import gateway)
│   └── presence.proto             # PresenceService
│
└── mq/v1/
    └── event.proto                # NATS 消息:MQEvent(包装 ChatEvent + trace)
```

**依赖方向**(绝不反向):

```
gateway/v1 ──▶ common/v1
logic/v1   ──▶ common/v1
mq/v1      ──▶ common/v1
```

`gateway/v1` 和 `logic/v1` **互不依赖**,它们的共同基础都是 `common/v1`。

**公共 DTO 分层规则**:
- `common/v1` 放跨层稳定视图对象,例如 `SessionInfo`、`ContactInfo`、`InboxEvent`
- `gateway/v1` / `logic/v1` 直接引用这些 DTO,不再镜像复制一份同构结构
- 只有明确带有边界差异的 command/request 才留在各自 service proto 内

---

## 2. common/v1 — 基础类型

### 2.1 `common/v1/types.proto`

```protobuf
syntax = "proto3";
package resonance.common.v1;
option go_package = "github.com/ceyewan/resonance/api/gen/go/common/v1;commonv1";

message User {
  string username = 1;
  string nickname = 2;
  string avatar_url = 3;
}
```

### 2.2 `common/v1/session.proto`

```protobuf
syntax = "proto3";
package resonance.common.v1;
option go_package = "github.com/ceyewan/resonance/api/gen/go/common/v1;commonv1";

enum SessionType {
  SESSION_TYPE_UNSPECIFIED = 0;
  SESSION_TYPE_DIRECT      = 1;  // 单聊
  SESSION_TYPE_GROUP       = 2;  // 群聊
  SESSION_TYPE_AI          = 3;  // AI 会话(预留)
}

// SessionMeta 是会话元数据,常嵌在推送/历史消息里避免前端额外查询
message SessionMeta {
  string      name        = 1;   // 单聊是对端昵称,群聊是群名
  SessionType type        = 2;
  string      avatar_url  = 3;
}
```

### 2.3 `common/v1/message.proto`

```protobuf
syntax = "proto3";
package resonance.common.v1;
option go_package = "github.com/ceyewan/resonance/api/gen/go/common/v1;commonv1";

enum MessageType {
  MESSAGE_TYPE_UNSPECIFIED = 0;
  MESSAGE_TYPE_TEXT        = 1;
  MESSAGE_TYPE_IMAGE       = 2;
  MESSAGE_TYPE_FILE        = 3;
  MESSAGE_TYPE_SYSTEM      = 4;  // 系统消息("XXX 加入了群聊")
  MESSAGE_TYPE_AI_STREAM   = 5;  // AI 流式消息的最终态
}

// Message 是 ChatEvent 里"普通消息"这一种 payload 的结构
// 只承载消息本体字段,不再混入会话快照/目标用户等跨层展示信息
message Message {
  MessageType type                  = 1;
  string      content               = 2;
  int64       reply_to_event_id     = 3;   // 引用回复,预留
  string      client_msg_id         = 4;   // 客户端幂等 ID
  repeated string mentioned_usernames = 5; // @提及,预留
}
```

说明:
- `to_username` 已移除:消息归属由 `session_id + session members` 决定,不再保留旧单聊残留字段
- `session_meta` 已移除:会话展示信息归 `SessionInfo / SessionUpdate` 或其他 envelope/view 对象承担

### 2.4 `common/v1/event.proto` — 核心载体

```protobuf
syntax = "proto3";
package resonance.common.v1;

import "common/v1/message.proto";
import "common/v1/session.proto";

option go_package = "github.com/ceyewan/resonance/api/gen/go/common/v1;commonv1";

// ChatEvent 是会话中发生的任何用户可感知事件的统一载体
// - 所有持久化事件都序列化成 ChatEvent 后存入 t_inbox.payload
// - Task -> Gateway -> Web 的推送链路统一传递 ChatEvent
// - PullInboxDelta 返回 ChatEvent 流
message ChatEvent {
  int64  event_id       = 1;   // Snowflake 全局唯一(取代旧 msg_id)
  int64  seq_id         = 2;   // 会话内逻辑时钟,严格递增
  string session_id     = 3;
  string from_username  = 4;   // 事件发起者
  int64  timestamp_ms   = 5;   // Unix 毫秒

  oneof payload {
    Message        message         = 10;  // 普通消息
    MessageRecall  recall          = 11;  // 撤回
    MessageEdit    edit            = 12;  // 编辑
    ReadReceipt    read_receipt    = 13;  // 已读位点变化
    SessionUpdate  session_update  = 14;  // 会话元信息变更
    // 未来扩展位 15~:Reaction / Mention / Pinned / ...
  }
}

// --- 各 payload 类型 -------------------------------------------------

message MessageRecall {
  int64 target_event_id = 1;   // 被撤回的消息 event_id
}

message MessageEdit {
  int64  target_event_id = 1;
  string new_content     = 2;
}

message ReadReceipt {
  int64 read_upto_seq_id = 1;  // 已读到哪个 seq_id(会话内)
}

message SessionUpdate {
  SessionUpdateKind kind             = 1;
  string            new_name         = 2;   // kind=NAME 时有效
  string            new_avatar_url   = 3;   // kind=AVATAR 时有效
  repeated string   affected_members = 4;   // kind=MEMBER_* 时的用户列表
}

enum SessionUpdateKind {
  SESSION_UPDATE_KIND_UNSPECIFIED = 0;
  SESSION_UPDATE_KIND_NAME        = 1;
  SESSION_UPDATE_KIND_AVATAR      = 2;
  SESSION_UPDATE_KIND_MEMBER_ADD  = 3;
  SESSION_UPDATE_KIND_MEMBER_KICK = 4;
}
```

**字段编号规则**(强约定):
- `1~9`:ChatEvent 本身的通用字段,不轻易动。
- `10~`:oneof 的分支,新增事件类型按顺序分配,已用的编号永不重用。

---

## 3. gateway/v1 — 对外与对内推送

### 3.1 `gateway/v1/packet.proto` — WebSocket 协议

```protobuf
syntax = "proto3";
package resonance.gateway.v1;

import "common/v1/event.proto";
import "common/v1/message.proto";

option go_package = "github.com/ceyewan/resonance/api/gen/go/gateway/v1;gatewayv1";

// WsPacket 是 WebSocket 上所有消息的封装
message WsPacket {
  string client_seq = 1;   // 客户端本地临时序号,用于 ACK 关联(原 seq 改名)

  oneof payload {
    // --- 连接层 ---
    Pulse        pulse          = 10;
    Ack          ack            = 11;

    // --- 上行(Web → Gateway → Logic) ---
    ChatRequest  chat_request   = 20;   // 客户端发消息

    // --- 下行:持久化事件(经 Inbox,保证可靠) ---
    resonance.common.v1.ChatEvent event = 30;

    // --- 下行:短暂事件(不经 Inbox,AI 流式专用) ---
    StreamBegin  stream_begin   = 40;
    StreamChunk  stream_chunk   = 41;
    StreamEnd    stream_end     = 42;
    TypingSignal typing         = 43;   // "对方正在输入"
  }
}

message Pulse {}

message Ack {
  string ref_client_seq = 1;   // 对应客户端的 WsPacket.client_seq
  int64  event_id       = 2;   // 服务端分配的 event_id
  int64  seq_id         = 3;
  string session_id     = 4;
  // 错误走 WebSocket 关闭或独立 error packet,不在 Ack 里混入 error string
}

// ChatRequest 是客户端上行的消息请求(只在 WS 上使用)
message ChatRequest {
  string session_id    = 1;
  resonance.common.v1.Message message = 2;   // 复用 common.Message
  // from_username 由 Gateway 从 JWT 填充,客户端不需要传
  // timestamp 由服务端填充
}

// --- AI 流式短暂事件 ---

message StreamBegin {
  int64  parent_event_id = 1;   // 最终消息的 event_id(开始时就分配好)
  string session_id      = 2;
  string from_username   = 3;   // AI Bot 的 username
}

message StreamChunk {
  int64  parent_event_id = 1;
  int32  sequence        = 2;   // chunk 顺序
  string delta           = 3;   // 增量文本
}

message StreamEnd {
  int64  parent_event_id = 1;
  StreamFinishReason reason = 2;
  // 不带内容 - 前端此时已从 Inbox 或待到的 ChatEvent 拿到完整消息
}

enum StreamFinishReason {
  STREAM_FINISH_REASON_UNSPECIFIED = 0;
  STREAM_FINISH_REASON_STOP        = 1;  // 正常结束
  STREAM_FINISH_REASON_LENGTH      = 2;  // 达到 token 上限
  STREAM_FINISH_REASON_TOOL_CALL   = 3;  // 模型请求调用工具(中间态)
  STREAM_FINISH_REASON_ERROR       = 4;  // 异常中断
}

message TypingSignal {
  string session_id   = 1;
  string from_username = 2;
  bool   is_typing    = 3;
}
```

**注意点**:
- `WsPacket.seq` 改名 `client_seq`,避免和 `ChatEvent.seq_id` 混淆。
- 下行推送统一用 `ChatEvent`,不再有各自的 `PushMessage` 结构。
- AI 流式的三种 packet 只在 WS 上出现,永远不进数据库。

### 3.2 `gateway/v1/auth.proto` + `gateway/v1/session.proto` — ConnectRPC(Web 调用)

```protobuf
syntax = "proto3";
package resonance.gateway.v1;

import "common/v1/types.proto";
import "common/v1/event.proto";
import "common/v1/session.proto";
import "common/v1/view.proto";

option go_package = "github.com/ceyewan/resonance/api/gen/go/gateway/v1;gatewayv1";

// auth.proto
service AuthService {
  rpc Login(LoginRequest) returns (LoginResponse);
  rpc Register(RegisterRequest) returns (RegisterResponse);
  rpc Logout(LogoutRequest) returns (LogoutResponse);
}

message LoginRequest    { string username = 1; string password = 2; }
message LoginResponse   { string access_token = 1; resonance.common.v1.User user = 2; }

message RegisterRequest  { string username = 1; string password = 2; string nickname = 3; }
message RegisterResponse { string access_token = 1; resonance.common.v1.User user = 2; }

message LogoutRequest   {}    // 身份从 Authorization Header 取,body 不需要
message LogoutResponse  { bool success = 1; }

// session.proto
service SessionService {
  rpc GetSessionList(GetSessionListRequest) returns (GetSessionListResponse);
  rpc CreateSession(CreateSessionRequest) returns (CreateSessionResponse);
  rpc GetHistoryEvents(GetHistoryEventsRequest) returns (GetHistoryEventsResponse);
  rpc PullInboxDelta(PullInboxDeltaRequest) returns (PullInboxDeltaResponse);
  rpc UpdateReadPosition(UpdateReadPositionRequest) returns (UpdateReadPositionResponse);
  rpc GetContactList(GetContactListRequest) returns (GetContactListResponse);
  rpc SearchUser(SearchUserRequest) returns (SearchUserResponse);
}

message GetSessionListRequest  {}
message GetSessionListResponse { repeated resonance.common.v1.SessionInfo sessions = 1; }

message CreateSessionRequest {
  resonance.common.v1.SessionType type = 1;
  repeated string members              = 2;   // 单聊=[对方],群聊=[member1, member2, ...]
  string name                          = 3;   // 仅群聊有效
}
message CreateSessionResponse {
  string session_id      = 1;
  bool   already_existed = 2;   // 单聊幂等:如果已存在则为 true
}

// 历史事件拉取(按会话)
message GetHistoryEventsRequest {
  string session_id   = 1;
  int64  before_seq   = 2;   // 0=拉最近一页;>0=拉 seq_id < before_seq 的
  int32  limit        = 3;
}
message GetHistoryEventsResponse {
  repeated resonance.common.v1.ChatEvent events = 1;
  bool has_more = 2;
}

// 全局增量拉取(按用户 Inbox 游标)
message PullInboxDeltaRequest {
  int64 cursor_id = 1;
  int32 limit     = 2;
}
message InboxEvent {
  int64 inbox_id = 1;
  resonance.common.v1.ChatEvent event = 2;
}
message PullInboxDeltaResponse {
  repeated resonance.common.v1.InboxEvent events = 1;
  int64 next_cursor_id          = 2;
  bool  has_more                = 3;
}

message UpdateReadPositionRequest {
  string session_id      = 1;
  int64  read_upto_seq   = 2;
}
message UpdateReadPositionResponse {
  int64 unread_count = 1;
}

message GetContactListRequest  {}
message GetContactListResponse { repeated resonance.common.v1.ContactInfo contacts = 1; }

message SearchUserRequest  { string query = 1; }
message SearchUserResponse { repeated resonance.common.v1.ContactInfo users = 1; }
```

**关键变更**:
- 所有请求里的 `access_token` **全部删除**。鉴权由 Gateway 的中间件从 `Authorization` Header 解析。
- `GetHistoryMessages` 改名 `GetHistoryEvents`,返回 `ChatEvent` 而不是 `PushMessage`。
- `PullInboxDelta` 返回的 `InboxDeltaItem` 承载 `ChatEvent`,能表达撤回/已读等事件。
- 所有 Response 不再有 `string error` 字段,失败走 gRPC status。

### 3.3 `gateway/v1/push.proto` — Task/AI → Gateway

```protobuf
syntax = "proto3";
package resonance.gateway.v1;

import "common/v1/event.proto";
import "gateway/v1/packet.proto";

option go_package = "github.com/ceyewan/resonance/api/gen/go/gateway/v1;gatewayv1";

// PushService - 供 Task 和未来的 AI Service 推送到 Gateway
service PushService {
  // PushEvent - 持久化事件推送(消息/撤回/已读/...)
  rpc PushEvent(PushEventRequest) returns (PushEventResponse);

  // PushStream - AI 流式短暂事件推送(不持久化)
  rpc PushStream(PushStreamRequest) returns (PushStreamResponse);
}

// --- 持久化事件推送 ---

message PushEventRequest {
  repeated string to_usernames = 1;    // 本 Gateway 上需要投递的用户
  resonance.common.v1.ChatEvent event = 2;
}
message PushEventResponse {
  int64 event_id                 = 1;
  repeated string failed_usernames = 2;  // 未在线或推送失败的用户
}

// --- 流式短暂事件推送 ---

message PushStreamRequest {
  repeated string to_usernames = 1;
  oneof payload {
    StreamBegin  begin  = 10;
    StreamChunk  chunk  = 11;
    StreamEnd    end    = 12;
    TypingSignal typing = 13;
  }
}
message PushStreamResponse {
  repeated string failed_usernames = 1;
}
```

---

## 4. logic/v1 — Gateway → Logic 内部协议

### 4.1 `logic/v1/chat.proto` — 统一事件入口

```protobuf
syntax = "proto3";
package resonance.logic.v1;

import "common/v1/event.proto";
import "common/v1/message.proto";

option go_package = "github.com/ceyewan/resonance/api/gen/go/logic/v1;logicv1";

service ChatService {
  // SendEvent - 统一的事件提交入口
  // 客户端只传 payload 和 session_id,event_id/seq_id/timestamp 由 Logic 生成
  rpc SendEvent(SendEventRequest) returns (SendEventResponse);
}

// SendEventRequest - 客户端请求发起的所有事件都走这里
// from_username 通过 gRPC metadata("x-username") 传递,body 里不带
message SendEventRequest {
  string session_id = 1;

  // 客户端只能发起这几种事件,其他(ReadReceipt/SessionUpdate)由专用 RPC 处理
  oneof payload {
    resonance.common.v1.Message       message = 10;
    resonance.common.v1.MessageRecall recall  = 11;
    resonance.common.v1.MessageEdit   edit    = 12;
  }
}

message SendEventResponse {
  int64 event_id = 1;
  int64 seq_id   = 2;
  int64 timestamp_ms = 3;
}
```

**设计说明**:
- 把原 `SendMessage` 泛化为 `SendEvent`。未来撤回、编辑复用同一入口,Logic 侧在 oneof 分派。
- **已读位点**不走 `SendEvent`,因为它不是"聊天内容",走独立的 `SessionService.UpdateReadPosition`——但 Logic 内部会把已读变化**也**打包成 `ChatEvent { ReadReceipt }` 进 Outbox,实现多端同步。
- **身份走 metadata**,Logic 通过 interceptor 解出 `x-username` 塞 context。

### 4.2 `logic/v1/session.proto`

```protobuf
syntax = "proto3";
package resonance.logic.v1;

import "common/v1/types.proto";
import "common/v1/event.proto";
import "common/v1/session.proto";

option go_package = "github.com/ceyewan/resonance/api/gen/go/logic/v1;logicv1";

service SessionService {
  rpc GetSessionList(GetSessionListRequest) returns (GetSessionListResponse);
  rpc CreateSession(CreateSessionRequest) returns (CreateSessionResponse);
  rpc GetHistoryEvents(GetHistoryEventsRequest) returns (GetHistoryEventsResponse);
  rpc PullInboxDelta(PullInboxDeltaRequest) returns (PullInboxDeltaResponse);
  rpc UpdateReadPosition(UpdateReadPositionRequest) returns (UpdateReadPositionResponse);
  rpc GetContactList(GetContactListRequest) returns (GetContactListResponse);
  rpc SearchUser(SearchUserRequest) returns (SearchUserResponse);
}

// 所有 Request 都不再带 username —— 从 gRPC metadata 取
// 以下消息体与 gateway/v1/session.proto 里对应,共用 common/v1/view.proto 中的视图 DTO

message GetSessionListRequest  {}
message GetSessionListResponse { repeated resonance.common.v1.SessionInfo sessions = 1; }

message CreateSessionRequest {
  resonance.common.v1.SessionType type = 1;
  repeated string members              = 2;
  string name                          = 3;
}
message CreateSessionResponse {
  string session_id      = 1;
  bool   already_existed = 2;
}

message GetHistoryEventsRequest {
  string session_id = 1;
  int64  before_seq = 2;
  int32  limit      = 3;
}
message GetHistoryEventsResponse {
  repeated resonance.common.v1.ChatEvent events = 1;
  bool has_more = 2;
}

message PullInboxDeltaRequest {
  int64 cursor_id = 1;
  int32 limit     = 2;
}
message InboxEvent {
  int64 inbox_id = 1;
  resonance.common.v1.ChatEvent event = 2;
}
message PullInboxDeltaResponse {
  repeated resonance.common.v1.InboxEvent events = 1;
  int64 next_cursor_id          = 2;
  bool  has_more                = 3;
}

message UpdateReadPositionRequest {
  string session_id    = 1;
  int64  read_upto_seq = 2;
}
message UpdateReadPositionResponse {
  int64 unread_count = 1;
}

message GetContactListRequest  {}
message GetContactListResponse { repeated resonance.common.v1.ContactInfo contacts = 1; }

message SearchUserRequest  { string query = 1; }
message SearchUserResponse { repeated resonance.common.v1.ContactInfo users = 1; }
```

### 4.3 `logic/v1/auth.proto`

```protobuf
syntax = "proto3";
package resonance.logic.v1;

import "common/v1/types.proto";
option go_package = "github.com/ceyewan/resonance/api/gen/go/logic/v1;logicv1";

service AuthService {
  rpc Login(LoginRequest) returns (LoginResponse);
  rpc Register(RegisterRequest) returns (RegisterResponse);
  rpc ValidateToken(ValidateTokenRequest) returns (ValidateTokenResponse);
}

message LoginRequest    { string username = 1; string password = 2; }
message LoginResponse   { string access_token = 1; resonance.common.v1.User user = 2; }

message RegisterRequest  { string username = 1; string password = 2; string nickname = 3; }
message RegisterResponse { string access_token = 1; resonance.common.v1.User user = 2; }

message ValidateTokenRequest  { string access_token = 1; }
message ValidateTokenResponse { resonance.common.v1.User user = 1; }
// 注意:valid=false 时直接返回 gRPC Unauthenticated 错误,不要用 bool valid 字段
```

### 4.4 `logic/v1/presence.proto` — 基本不变

保持现状即可。批量上下线事件结构合理。去掉 `SyncStatusResponse.error`(用 gRPC status)。

---

## 5. mq/v1 — NATS 消息

### `mq/v1/event.proto`

```protobuf
syntax = "proto3";
package resonance.mq.v1;

import "common/v1/event.proto";
import "common/v1/options.proto";

option go_package = "github.com/ceyewan/resonance/api/gen/go/mq/v1;mqv1";

// MQEvent 是 Logic → Task 的 NATS 消息
// 包装 ChatEvent + trace + 推送目标列表(由 Logic 计算好)
message MQEvent {
  option (resonance.common.v1.default_topic) = "resonance.chat.event.v1";

  resonance.common.v1.ChatEvent event = 1;

  // 该事件需要投递给哪些用户(会话成员列表,Logic 侧计算好避免 Task 再查)
  repeated string target_usernames = 2;

  // 分布式追踪上下文
  map<string, string> trace_headers = 10;
}
```

**Topic 命名变化**:
- 旧:`resonance.push.event.v1`
- 新:`resonance.chat.event.v1`

**原因**:从"推送事件"改名为"聊天事件",语义更准(MQEvent 同时承担持久化和推送两个职责)。

---

## 6. 字段编号约定(强制)

| 范围 | 用途 |
|------|------|
| `1~9` | 消息/事件的固有核心字段(id、seq、session、from、timestamp) |
| `10~19` | `ChatEvent.payload` 的 oneof 分支 |
| `10+` | 其他消息的业务字段 |
| **禁止** | 重复使用已经分配过又删除的字段编号 |

每次修改 proto 时,在该 message 顶部加注释标注"**下一个可用编号:N**"。

---

## 7. 生成与类型映射

保持现有 `make gen` 流程。前端 TypeScript 生成代码路径不变。注意:
- 所有生成的 `ChatEvent`、`Message`、`SessionType` 类型都在 `common/v1` 包下,TypeScript 侧导出到 `web/src/api/common/v1/`。
- `oneof` 在 TypeScript 里是 `payload: { case: "message"; value: Message } | { case: "recall"; value: MessageRecall } | ...`,前端分发逻辑按 `event.payload.case` switch。

---

## 8. 与现有协议的差异速查

| 旧 | 新 | 备注 |
|----|----|------|
| `PushMessage` / `ChatRequest` / `SendMessageRequest` / `PushEvent`(4 个重复) | 统一用 `ChatEvent`(含 oneof payload) | 最核心的变更 |
| `string type`("text") | `MessageType` enum | 类型安全 |
| `int32 type`(1=单聊) | `SessionType` enum | 同上 |
| `string access_token`(body) | `Authorization` Header + gRPC metadata | 安全性 + 简洁 |
| `string error`(响应里) | gRPC status code | 标准化错误 |
| `msg_id` | `event_id` | 语义扩展为事件 |
| `logic -> import gateway/v1` | 全都 import `common/v1` | 修分层 |
| `WsPacket.seq` | `WsPacket.client_seq` | 避免歧义 |
| `SessionService.GetHistoryMessages` | `SessionService.GetHistoryEvents` | 返回事件 |
| `resonance.push.event.v1`(topic) | `resonance.chat.event.v1` | 语义矫正 |

下一步看 `02-database.md`,了解这套协议对应的数据表应该长什么样。
