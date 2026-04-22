# 数据模型与持久化设计

> 本文档描述 Resonance 的数据库表结构、Redis 键设计、索引策略和事务边界。阅读完本文后，应该能回答三个问题：Inbox 为什么存储完整的 ChatEvent 而不是消息索引；Outbox 的事务边界为什么必须和主事实写入绑定在一起；以及系统如何通过 `last_read_seq` 替代 Inbox 级别的已读标记。

---

## 1. 设计原则

Resonance 的数据模型围绕两个核心目标组织。第一个目标是让主事实（消息内容、会话关系、成员状态）有唯一权威来源，所有读取和写入都通过 Logic 的事务边界完成。第二个目标是让 Inbox 成为用户事件流的可靠兜底，而不仅仅是消息索引，这样系统才能在推送失败时仍然保持最终一致。

这两个目标共同决定了几个关键设计选择：Inbox 存储完整的 `ChatEvent` 序列化字节而不是消息 ID 引用；已读状态由 `t_session_member.last_read_seq` 统一表达而不是在 Inbox 行上打标记；Outbox 和主事实写入必须在同一个数据库事务中完成。

---

## 2. 表结构总览

当前系统共有六张持久化表，职责如下：

| 表名 | 职责 |
| ---- | ---- |
| `t_user` | 用户账号与基本信息 |
| `t_session` | 会话元数据（单聊/群聊） |
| `t_session_member` | 会话成员关系与已读位点 |
| `t_message_content` | 消息主事实（内容、撤回、编辑状态） |
| `t_inbox` | 用户事件流（写扩散目标，存完整 ChatEvent） |
| `t_message_outbox` | 可靠投递记录（Outbox 模式） |

表结构的唯一真相来源是 `model/model.go`，通过 `go run main.go -module init` 调用 GORM AutoMigrate 自动创建和更新。

---

## 3. 详细表定义

### 3.1 `t_user`

```go
type User struct {
    Username  string    // PK, varchar(64)
    Nickname  string    // varchar(64)
    Password  string    // varchar(128), bcrypt hash
    Avatar    string    // varchar(255)
    CreatedAt time.Time
    UpdatedAt time.Time
}
```

用户名是系统内唯一身份标识，也是跨服务传递身份的载体（gRPC metadata `x-username`）。密码字段存储 bcrypt 哈希，不存明文。

### 3.2 `t_session`

```go
type Session struct {
    SessionID     string // PK, varchar(64)
    Type          int    // 1-单聊, 2-群聊
    Kind          int    // 0-普通, 预留扩展
    Name          string // 群聊名称
    AvatarURL     string // 群头像
    OwnerUsername string // 创建者
    MaxSeqID      int64  // 当前最大序列号
    CreatedAt     time.Time
    UpdatedAt     time.Time
}
```

`MaxSeqID` 是会话序列号的持久化锚点。Logic 在事务内通过 CAS 更新它，Redis 中的原子计数器以它为初始值。当 Redis 键不存在时，Logic 会先用 `MaxSeqID` 初始化计数器，再递增，避免序列号从 1 开始导致冲突。

### 3.3 `t_session_member`

```go
type SessionMember struct {
    SessionID   string // PK(session_id, username)
    Username    string // PK, index: idx_member_username
    Role        int    // 0-成员, 1-管理员
    LastReadSeq int64  // 已读位点（权威来源）
    CreatedAt   time.Time
    UpdatedAt   time.Time
}
```

`LastReadSeq` 是系统中已读状态的唯一权威来源。未读数的计算方式是：查询该用户在该会话中 `seq_id > last_read_seq` 的 Inbox 记录数，而不是在 Inbox 行上维护 `is_read` 字段。这样避免了"用户读到第 80 条消息时需要批量更新 80 条 Inbox 记录"的写放大问题。

`idx_member_username` 索引支持反查"某用户加入了哪些会话"，用于会话列表和联系人查询。

### 3.4 `t_message_content`

```go
type MessageContent struct {
    EventID        int64      // PK, Snowflake ID
    SessionID      string     // index: idx_sess_seq(1)
    SenderUsername string
    SeqID          int64      // index: idx_sess_seq(2)
    Content        string     // text
    MsgType        int        // 消息类型枚举
    ReplyToEventID int64      // 引用回复
    ClientMsgID    string     // 客户端幂等 ID, index: idx_client_msg_id
    RecalledAt     *time.Time // 撤回时间，NULL 表示未撤回
    EditedAt       *time.Time // 最后编辑时间
    EditCount      int        // 编辑次数
    CreatedAt      time.Time
}
```

这张表是消息的主事实存储。撤回和编辑通过 `RecalledAt`/`EditedAt` 字段软标记，历史拉取时客户端根据这些字段决定渲染方式。编辑内容不保留历史版本（V1 简化处理）。

`ClientMsgID` 用于客户端幂等去重：客户端重发同一条消息时，Logic 可以通过这个字段检测到重复并返回已有的 `event_id`，而不是写入两条记录。

**索引设计：**

- `PK(event_id)`：按事件 ID 精确查询
- `idx_sess_seq(session_id, seq_id)`：会话历史拉取，支持 `seq_id` 游标分页
- `idx_client_msg_id(client_msg_id)`：客户端幂等查询

### 3.5 `t_inbox`

```go
type Inbox struct {
    ID            int64  // PK, 自增，用作游标
    OwnerUsername string // uniqueIndex: uniq_owner_sess_seq(1), index: idx_owner_id(1), idx_owner_sess(1)
    SessionID     string // uniqueIndex: uniq_owner_sess_seq(2), index: idx_owner_sess(2)
    SeqID         int64  // uniqueIndex: uniq_owner_sess_seq(3)
    EventID       int64  // ChatEvent.event_id
    EventType     int    // 1-Message 2-Recall 3-Edit 4-ReadReceipt 5-SessionUpdate
    Payload       []byte // 序列化的 ChatEvent（protobuf bytes）
    CreatedAt     time.Time
}
```

Inbox 是系统中最重要的一张表，也是整个可靠性设计的核心兜底。每一行代表"某个用户需要感知的一次会话事件"，`Payload` 字段存储完整的 `ChatEvent` 序列化字节，客户端拉取后直接反序列化分发，不需要再 JOIN 其他表。

`ID` 是自增主键，作为增量拉取的游标。客户端通过 `PullInboxDelta(cursor_id, limit)` 拉取 `id > cursor_id` 的记录，实现离线补偿和多端同步。

`uniq_owner_sess_seq(owner_username, session_id, seq_id)` 唯一约束防止写扩散重复：同一事件给同一用户写两次时，第二次会被幂等忽略（`ON CONFLICT DO NOTHING`）。

**索引设计：**

- `PK(id)`：游标扫描
- `uniq_owner_sess_seq`：防重幂等
- `idx_owner_id(owner_username, id)`：`PullInboxDelta` 游标扫描，最常用查询路径
- `idx_owner_sess(owner_username, session_id)`：按会话查询（未来清理会话时使用）

### 3.6 `t_message_outbox`

```go
type MessageOutbox struct {
    ID            int64     // PK, 自增
    EventID       int64     // index: idx_event_id
    Topic         string    // NATS subject
    Payload       []byte    // 序列化的 MQEvent（protobuf bytes）
    Status        int       // 0-待发送, 1-已发送, 2-失败; index: idx_status_next_retry(1)
    RetryCount    int
    NextRetryTime time.Time // index: idx_status_next_retry(2)
    CreatedAt     time.Time
    UpdatedAt     time.Time
}
```

Outbox 是可靠投递的保障机制。Logic 在事务内同时写入主事实和 Outbox，事务提交后再异步发布 MQ。如果 MQ 发布失败，`logic/job/outbox.go` 的定时补偿任务会扫描 `status=0 AND next_retry_time <= NOW()` 的记录并重新投递，直到成功或超过最大重试次数。

`idx_status_next_retry(status, next_retry_time)` 是补偿任务的核心查询索引，支持高效扫描待重试记录。

---

## 4. Redis 键设计

Redis 在系统中承担两类职责：在线路由存储和序列号辅助状态。

| Key 模式 | 类型 | 用途 | 生命周期 |
| -------- | ---- | ---- | -------- |
| `route:user:{username}` | String/Hash | 用户 → Gateway 实例映射 | 随 WebSocket 连接建立/断开 |
| `resonance:logic:worker` | String | Logic 实例 WorkerID 分配 | 持久 |
| `resonance:gateway:worker` | String | Gateway 实例 WorkerID 分配 | 持久 |
| `{session_id}` (sequencer) | String | 会话序列号原子计数器 | 持久，首次使用时初始化 |

在线路由键存储用户当前连接的 Gateway 实例 ID，Task 在写扩散后通过这个键查询目标用户在哪个 Gateway 节点上，再发起 gRPC Push 调用。

---

## 5. 事务边界

系统中有两处关键事务边界，理解它们对理解整个可靠性设计至关重要。

### 5.1 Logic 主事实写入事务

`repo.SaveMessageWithOutbox` 在单个数据库事务内完成三件事：写入 `t_message_content`、用 CAS 更新 `t_session.max_seq_id`、写入 `t_message_outbox`。事务提交后，Logic 再异步发布 MQ。

这个设计保证了"主事实已成立"和"投递记录已存在"是原子的。即使 MQ 发布失败，Outbox 补偿任务也能保证事件最终进入异步链路。

```text
事务内：
  INSERT t_message_content
  UPDATE t_session SET max_seq_id = ? WHERE max_seq_id < ?  (CAS)
  INSERT t_message_outbox

事务外（异步）：
  Publish MQEvent → NATS
  失败时由 outbox job 补偿
```

### 5.2 Task 写扩散事务

`repo.SaveInboxBatch` 在单个事务内批量写入多个用户的 Inbox 记录，使用 `ON CONFLICT DO NOTHING` 保证幂等。写扩散成功后，Task 才发起在线推送。

这个顺序保证了"先写 Inbox，后推送"的语义：即使推送失败，用户重连后仍然可以通过 `PullInboxDelta` 拉取到同样的事件。

---

## 6. 数据流与表的对应关系

```text
Logic.SendEvent
  ├── 事务写 t_message_content（主事实）
  ├── 事务写 t_message_outbox（投递保障）
  └── 异步发布 MQEvent → NATS

Task.Handle
  ├── 批量写 t_inbox（写扩散，每个目标用户一行）
  └── 查 Redis route:user:{username} → 推送到对应 Gateway
```

---

## 7. 当前实现结构

数据模型的主要实现落点包括：

- `model/model.go`：所有表结构的唯一真相来源，包含 GORM tag 和常量定义
- `repo/message.go`：消息、Inbox、Outbox 的读写操作
- `repo/session.go`：会话、成员、已读位点的读写操作
- `repo/router.go`：在线路由的 Redis 读写
- `bootstrap/bootstrap.go`：AutoMigrate 与种子数据初始化

---

## 8. 阅读建议

| 文档 | 内容 |
| ---- | ---- |
| `01-protocol.md` | ChatEvent 结构与 Inbox Payload 的关系 |
| `11-logic.md` | 事务边界与 Outbox 写入的业务上下文 |
| `12-task.md` | 写扩散执行与 Inbox 语义 |
| `21-write-fanout.md` | 写扩散模型与一致性策略的进一步展开 |

---

## 9. 小结

Resonance 的数据模型以"主事实在 Logic 事务内成立，事件流在 Inbox 中可靠兜底"为核心。`t_message_content` 存储消息权威状态，`t_inbox` 存储每个用户需要感知的完整事件序列，`t_message_outbox` 保障事件从 Logic 到 Task 的可靠传递。只要这三张表的写入顺序和事务边界保持正确，系统就能在任意节点失败时仍然维持最终一致。
