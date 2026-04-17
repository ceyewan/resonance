# 数据库设计:从消息表到事件流

> 读之前先理解 `01-protocol.md` 的 `ChatEvent` 抽象。本文档定义所有持久化表与 Redis 键。

---

## 1. 设计决策

### 1.1 `t_inbox` 升级为事件流,而非仅消息

**核心变更**:Inbox 存的不再是"消息索引",而是**用户事件流**。每行是该用户需要感知的一个事件,payload 为序列化的 `ChatEvent`。

| 项 | 旧设计 | 新设计 |
|----|--------|--------|
| 语义 | 用户消息信箱 | 用户事件流(消息/撤回/已读/...) |
| payload | (msg_id, seq_id) 索引指向 message_content | 直接内嵌 ChatEvent bytes |
| is_read | Inbox 存 | **移除**,由 `t_session_member.last_read_seq` 统一表达 |
| 游标 | inbox.id 自增 | 不变 |

**为什么移除 `is_read`**:
1. 已读是"某用户在某会话里读到哪个 seq_id"的**会话维度**概念,不是消息维度。
2. 会话有 100 条消息、用户读到第 80 条,要标记 Inbox 的 80 个 is_read=1 是无意义的 UPDATE 放大。
3. `t_session_member.last_read_seq` 已经存在,本来就是这个事实的正确载体。
4. 查未读数改为 `COUNT(*) FROM t_inbox WHERE owner_username=? AND session_id=? AND seq_id > last_read_seq`,**单次查询**。

### 1.2 `t_message_content` 保留,增加软删除/编辑字段

主表仍是"消息"专属(撤回/编辑事件不在主表,只在 Inbox)。增加字段支持撤回和编辑:
- `recalled_at`:标记已撤回,历史拉取时可决定是否过滤
- `edited_at` + `edit_count`:标记已编辑次数
- 编辑内容**不保留历史版本**(V1 简化;未来要历史版本就加 `t_message_edit_log`)

### 1.3 `t_message_outbox` 存 `ChatEvent` 字节

旧:`payload` 存的是 `PushEvent` 序列化。
新:`payload` 存 `MQEvent` 序列化(内含 ChatEvent)。字段不变,语义迁移。

### 1.4 新增 `t_ai_session_meta`(预留)

为 AI 聊天准备的会话级元数据表,存模型选择、system prompt、工具配置等。**V1 先不建**,在实施 AI 功能时再建。这里写出来是让表结构统一规划,避免到时候临时加。

---

## 2. 表结构清单

| 表 | 状态 | 变更 |
|----|------|------|
| `t_user` | 保持 | 无 |
| `t_session` | 保持 | 加一个字段 `session_kind`(为 AI 会话预留) |
| `t_session_member` | 保持 | 无 |
| `t_message_content` | **修改** | 加 `recalled_at`、`edited_at`、`edit_count` |
| `t_inbox` | **重构** | 加 `event_type`、`payload`;移除 `is_read`、`msg_id`;加 GIN/索引 |
| `t_message_outbox` | 保持 | 无(payload 语义从 PushEvent 变为 MQEvent,表结构不动) |
| `t_ai_session_meta` | **新增(延迟到 AI 阶段)** | AI 会话元数据 |

Redis 键保持不变。

---

## 3. 详细表定义

### 3.1 `t_user`(不变)

```go
type User struct {
    Username  string    `gorm:"primaryKey;column:username;type:varchar(64);not null"`
    Nickname  string    `gorm:"column:nickname;type:varchar(64)"`
    Password  string    `gorm:"column:password;type:varchar(128);not null"`
    Avatar    string    `gorm:"column:avatar;type:varchar(255)"`
    CreatedAt time.Time
    UpdatedAt time.Time
}
```

### 3.2 `t_session`(加 1 个字段)

```go
type Session struct {
    SessionID     string `gorm:"primaryKey;column:session_id;type:varchar(64);not null"`
    Type          int    `gorm:"column:type;type:smallint;not null"`            // 1-单聊, 2-群聊, 3-AI
    Kind          int    `gorm:"column:kind;type:smallint;default:0"`           // ★新增:0-普通, 1-AI(预留扩展)
    Name          string `gorm:"column:name;type:varchar(128)"`
    AvatarURL     string `gorm:"column:avatar_url;type:varchar(255)"`           // ★新增:群头像
    OwnerUsername string `gorm:"column:owner_username;type:varchar(64)"`
    MaxSeqID      int64  `gorm:"column:max_seq_id;type:bigint;default:0"`
    CreatedAt     time.Time
    UpdatedAt     time.Time
}
```

说明:
- `Type` 表示会话分类(单聊/群聊/AI),**语义上决定了成员数量规则**。
- `Kind` 进一步标记**特殊行为**,为 AI 区分"普通 AI 会话"vs"带工具的 AI 会话"等预留。V1 可以不用,字段先占位。
- `AvatarURL` 补齐群头像字段,当前协议里 SessionMeta 有 avatar_url 但表里没存。

### 3.3 `t_session_member`(不变)

```go
type SessionMember struct {
    SessionID   string `gorm:"primaryKey;column:session_id;type:varchar(64);not null"`
    Username    string `gorm:"primaryKey;column:username;type:varchar(64);not null;index:idx_member_username"`
    Role        int    `gorm:"column:role;type:smallint;default:0"`           // 0-成员, 1-管理员
    LastReadSeq int64  `gorm:"column:last_read_seq;type:bigint;default:0"`    // 已读位点(权威来源)
    CreatedAt   time.Time
    UpdatedAt   time.Time
}
```

`LastReadSeq` 是**已读的唯一事实来源**。所有计算未读、判断新消息都基于它。

### 3.4 `t_message_content`(加 3 个字段)

```go
type MessageContent struct {
    EventID        int64      `gorm:"primaryKey;column:event_id;type:bigint;autoIncrement:false"`  // 原 msg_id,改名
    SessionID      string     `gorm:"column:session_id;type:varchar(64);not null;index:idx_sess_seq,priority:1"`
    SenderUsername string     `gorm:"column:sender_username;type:varchar(64);not null"`
    SeqID          int64      `gorm:"column:seq_id;type:bigint;not null;index:idx_sess_seq,priority:2"`
    Content        string     `gorm:"column:content;type:text"`
    MsgType        int        `gorm:"column:msg_type;type:smallint"`                // ★string→int,对齐 MessageType enum
    ReplyToEventID *int64     `gorm:"column:reply_to_event_id;type:bigint"`         // ★新增:引用回复
    ClientMsgID    string     `gorm:"column:client_msg_id;type:varchar(64);index:idx_client_msg_id"` // ★新增:客户端幂等

    RecalledAt     *time.Time `gorm:"column:recalled_at"`                           // ★新增:软删除
    EditedAt       *time.Time `gorm:"column:edited_at"`                             // ★新增:编辑时间
    EditCount      int        `gorm:"column:edit_count;default:0"`                  // ★新增

    CreatedAt      time.Time
}
```

字段说明:
- `EventID`:原 `MsgID` 改名,和 proto 对齐。全局 Snowflake。
- `MsgType`:从 `varchar(32)` 改为 `smallint`,和 `MessageType` enum 对齐。
- `ClientMsgID`:客户端生成的临时 ID,用于幂等去重(同一客户端重发同一消息)。
- `RecalledAt`/`EditedAt`:事件发生的时间;为 NULL 表示未发生。历史拉取时业务决定是否过滤撤回的消息。
- **不存编辑历史**,V1 简单处理。未来用 `t_message_edit_log` 单独存。

**索引**:
- PK: `event_id`
- `idx_sess_seq`: `(session_id, seq_id)` — 会话历史拉取
- `idx_client_msg_id`: `client_msg_id` — 客户端幂等查询(查"这个临时 ID 已经入库没")

### 3.5 `t_inbox`(**核心重构**)

```go
type Inbox struct {
    ID            int64  `gorm:"primaryKey;column:id;autoIncrement"`
    OwnerUsername string `gorm:"column:owner_username;type:varchar(64);not null;uniqueIndex:uniq_owner_sess_seq,priority:1;index:idx_owner_id,priority:1"`
    SessionID     string `gorm:"column:session_id;type:varchar(64);not null;uniqueIndex:uniq_owner_sess_seq,priority:2;index:idx_owner_sess,priority:2"`
    SeqID         int64  `gorm:"column:seq_id;type:bigint;not null;uniqueIndex:uniq_owner_sess_seq,priority:3"`

    EventID       int64  `gorm:"column:event_id;type:bigint;not null"`                                       // ChatEvent.event_id
    EventType     int    `gorm:"column:event_type;type:smallint;not null"`                                   // ★1-Message 2-Recall 3-Edit 4-ReadReceipt 5-SessionUpdate
    Payload       []byte `gorm:"column:payload;type:bytea;not null"`                                         // ★序列化的 ChatEvent

    CreatedAt     time.Time `gorm:"index:idx_owner_id,priority:2"`
}
```

字段说明:
- `ID`:自增游标,`PullInboxDelta` 以 `id > cursor` 拉增量。
- `(OwnerUsername, SessionID, SeqID)`:唯一约束,**防止写扩散重复**(同一事件给同一用户写两次)。
- `EventType`:小整数分类,**用于索引和快速过滤**(比如"只拉消息,不要已读事件")。映射:
  ```
  1 = Message
  2 = MessageRecall
  3 = MessageEdit
  4 = ReadReceipt
  5 = SessionUpdate
  ```
- `Payload`:序列化的 `ChatEvent`(protobuf bytes)。前端拉取后反序列化分发。
- **`IsRead` 字段移除**:未读数通过 `t_session_member.last_read_seq` 计算。

**索引**:
- PK: `id`
- `uniq_owner_sess_seq`: `(owner_username, session_id, seq_id)` 唯一,防重
- `idx_owner_id`: `(owner_username, id)` — `PullInboxDelta` 游标扫描
- `idx_owner_sess`: `(owner_username, session_id)` — 按会话查询(未来删除会话时清理)

**容量预估**:单条 Inbox 记录 ≈ 100 字节(固定列)+ ChatEvent bytes(文本消息约 150 字节)≈ 250 字节。1 万用户日均 100 条消息 → 日增约 250 MB,年增约 90 GB。未来需要**冷热分离**(超过 N 天的归档),V1 不处理。

### 3.6 `t_message_outbox`(不变)

```go
type MessageOutbox struct {
    ID            int64     `gorm:"primaryKey;column:id;autoIncrement"`
    EventID       int64     `gorm:"column:event_id;type:bigint;not null;index:idx_event_id"`  // 原 msg_id 改名
    Topic         string    `gorm:"column:topic;type:varchar(64);not null"`
    Payload       []byte    `gorm:"column:payload;type:bytea;not null"`                       // 存序列化的 MQEvent
    Status        int       `gorm:"column:status;type:smallint;default:0;index:idx_status_next_retry,priority:1"`
    RetryCount    int       `gorm:"column:retry_count;type:int;default:0"`
    NextRetryTime time.Time `gorm:"column:next_retry_time;index:idx_status_next_retry,priority:2"`
    CreatedAt     time.Time
    UpdatedAt     time.Time
}
```

字段不变,只把 `MsgID` 改名 `EventID`。`Payload` 现在存 `MQEvent` 的 protobuf bytes。

### 3.7 `t_ai_session_meta`(V1 不建,预留设计)

```go
type AISessionMeta struct {
    SessionID   string `gorm:"primaryKey;column:session_id;type:varchar(64);not null"`
    BotUsername string `gorm:"column:bot_username;type:varchar(64);not null"`         // AI 作为的 Bot 用户名
    ModelName   string `gorm:"column:model_name;type:varchar(64)"`                    // claude-opus-4-7 / gpt-5...
    SystemPrompt string `gorm:"column:system_prompt;type:text"`
    ToolsConfig  []byte `gorm:"column:tools_config;type:jsonb"`                       // MCP Server 和工具白名单
    ContextLimit int    `gorm:"column:context_limit;default:20"`                      // 取近 N 条作为上下文
    CreatedAt    time.Time
    UpdatedAt    time.Time
}
```

---

## 4. Redis 键设计

保持现有设计:

| Key 模式 | 类型 | 用途 | TTL |
|----------|------|------|-----|
| `route:user:{username}` | String/Hash | 用户 → Gateway 映射 | 随连接 |
| `online:set` | Set | 当前在线用户集合(可选) | 无 |

未来加 AI 相关:

| Key 模式 | 类型 | 用途 | TTL |
|----------|------|------|-----|
| `ai:ctx:{session_id}` | List | AI 会话近 N 轮对话缓存(避免每次查 DB) | 1h |
| `ai:rate:{username}` | Counter | AI 调用限流 | 按窗口 |

---

## 5. 迁移 SQL(从现状到目标)

按 `05-migration.md` 的 Phase 1 执行。由 `go run main.go -module init` 的 AutoMigrate 处理大部分改动;字段语义迁移需要手写 SQL。

### 5.1 `t_session`

```sql
ALTER TABLE t_session ADD COLUMN kind SMALLINT DEFAULT 0;
ALTER TABLE t_session ADD COLUMN avatar_url VARCHAR(255);
```

### 5.2 `t_message_content`

```sql
-- 改名 msg_id -> event_id
ALTER TABLE t_message_content RENAME COLUMN msg_id TO event_id;

-- 新增字段
ALTER TABLE t_message_content ADD COLUMN reply_to_event_id BIGINT;
ALTER TABLE t_message_content ADD COLUMN client_msg_id VARCHAR(64);
ALTER TABLE t_message_content ADD COLUMN recalled_at TIMESTAMP;
ALTER TABLE t_message_content ADD COLUMN edited_at TIMESTAMP;
ALTER TABLE t_message_content ADD COLUMN edit_count INT DEFAULT 0;

-- msg_type: string -> smallint(需要数据迁移)
ALTER TABLE t_message_content ADD COLUMN msg_type_new SMALLINT;
UPDATE t_message_content SET msg_type_new = CASE msg_type
  WHEN 'text'   THEN 1
  WHEN 'image'  THEN 2
  WHEN 'file'   THEN 3
  WHEN 'system' THEN 4
  ELSE 1 END;
ALTER TABLE t_message_content DROP COLUMN msg_type;
ALTER TABLE t_message_content RENAME COLUMN msg_type_new TO msg_type;

-- 新索引
CREATE INDEX idx_client_msg_id ON t_message_content(client_msg_id);
```

### 5.3 `t_inbox`(**需要重建,不能原地改**)

旧 Inbox 无 payload 字段,历史数据需要通过 `event_id → t_message_content` JOIN 重构 ChatEvent。

**推荐策略**:灰度迁移
1. 新建 `t_inbox_v2` 带新结构,双写(旧代码写 v1,新代码写 v2)一段时间
2. 通过脚本将 `t_inbox` 存量数据转为 `t_inbox_v2`(JOIN content 重建 payload)
3. 读切换到 v2,验证一段时间
4. 删除 v1,v2 改名 `t_inbox`

**如果允许停机**(项目初期用户少):
```sql
DROP TABLE t_inbox;
-- 重新 AutoMigrate 生成新结构
-- 重新生成历史 Inbox(可选,也可以不迁移历史,从空开始)
```

### 5.4 `t_message_outbox`

```sql
ALTER TABLE t_message_outbox RENAME COLUMN msg_id TO event_id;
-- payload 字段内容由新代码写入,旧代码数据可以保留或清空(Outbox 通常短期数据)
```

---

## 6. 与协议的映射表

确认表结构和协议一致:

| 表字段 | 协议字段 |
|--------|----------|
| `t_message_content.event_id` | `ChatEvent.event_id`(仅 payload=Message 时写入) |
| `t_message_content.seq_id` | `ChatEvent.seq_id` |
| `t_message_content.msg_type` | `Message.type`(MessageType enum) |
| `t_message_content.recalled_at` | 由 `ChatEvent.payload=MessageRecall` 触发更新 |
| `t_message_content.edited_at` | 由 `ChatEvent.payload=MessageEdit` 触发更新 |
| `t_inbox.payload` | 完整的 `ChatEvent` protobuf bytes |
| `t_inbox.event_type` | `ChatEvent.payload` 的 oneof case 映射小整数 |
| `t_session_member.last_read_seq` | `ReadReceipt.read_upto_seq_id` 触发更新 |
| `t_message_outbox.payload` | 完整的 `MQEvent` protobuf bytes |

---

## 7. Repo 接口调整(提前给出签名)

详细实现交由 `03-services.md`。这里先列出 repo 层的接口变化:

```go
// ---- 现有 MessageRepo 需要调整 ----
type MessageRepo interface {
    // 原 SaveMessage → 改名
    SaveMessageContent(ctx context.Context, msg *model.MessageContent) error

    // ★新增:按 event_id 标记撤回
    MarkMessageRecalled(ctx context.Context, eventID int64, at time.Time) error

    // ★新增:按 event_id 更新内容(编辑)
    UpdateMessageContent(ctx context.Context, eventID int64, newContent string, at time.Time) error

    // 历史拉取:返回 ChatEvent bytes(或先返回 MessageContent,再由 service 组装)
    GetHistoryEvents(ctx context.Context, sessionID string, beforeSeq int64, limit int) ([]*model.MessageContent, error)

    // Inbox 操作改签名
    SaveInboxBatch(ctx context.Context, items []*model.Inbox) error   // 批量写事件到 Inbox

    // 游标拉取:返回 Inbox 原始记录(含 payload bytes)
    GetInboxDelta(ctx context.Context, username string, cursorID int64, limit int) ([]*model.Inbox, error)

    // Outbox 接口不变,只是字段名 MsgID→EventID

    // ---- 已读 ----
    // (原 SessionRepo.UpdateLastReadSeq 保留)
    // 新增:按会话计算未读数
    GetUnreadCount(ctx context.Context, username, sessionID string) (int64, error)
}

// ---- SessionRepo 无重大调整 ----
// UpdateLastReadSeq 仍在 SessionRepo
```

---

## 8. 关键约束总结

1. `t_inbox` 的 `(owner_username, session_id, seq_id)` 唯一,**防重扩散**的核心。
2. `t_message_content.event_id` 全局唯一(Snowflake)。
3. `t_message_content.seq_id` 仅在会话内唯一递增,不是全局。
4. 已读权威来源:`t_session_member.last_read_seq` 单一数据源。
5. `t_inbox.payload` 是完整 ChatEvent,**Inbox 自洽**,不依赖 message_content JOIN 即可返回事件流(除非需要解析 payload 做业务判断)。

下一步看 `03-services.md`,了解三个服务内部代码组织和模块职责。
