package model

import (
	"time"
)

// ============================================================================
// 非持久化模型（Redis）
// ============================================================================

// Router 存储用户与网关实例的映射关系，通常存储在 Redis 中
type Router struct {
	Username  string `json:"username"`
	GatewayID string `json:"gateway_id"`
	RemoteIP  string `json:"remote_ip"`
	Timestamp int64  `json:"timestamp"`
}

// ============================================================================
// 持久化模型（PostgreSQL）
// 以下结构体的 GORM tag 是数据库表结构的唯一真相来源 (Single Source of Truth)。
// 表结构通过 `go run main.go -module init` 调用 GORM AutoMigrate 自动创建/更新。
// ============================================================================

// User 用户表
// 索引：PK(username)
type User struct {
	Username  string `gorm:"primaryKey;column:username;type:varchar(64);not null"`
	Nickname  string `gorm:"column:nickname;type:varchar(64)"`
	Password  string `gorm:"column:password;type:varchar(128);not null"`
	Avatar    string `gorm:"column:avatar;type:varchar(255)"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Session 会话表（单聊/群聊）
// 索引：PK(session_id)
type Session struct {
	SessionID     string `gorm:"primaryKey;column:session_id;type:varchar(64);not null"`
	Type          int    `gorm:"column:type;type:smallint;not null"` // 1-单聊, 2-群聊
	Kind          int    `gorm:"column:kind;type:smallint;default:0"`
	Name          string `gorm:"column:name;type:varchar(128)"`
	AvatarURL     string `gorm:"column:avatar_url;type:varchar(255)"`
	OwnerUsername string `gorm:"column:owner_username;type:varchar(64)"`
	MaxSeqID      int64  `gorm:"column:max_seq_id;type:bigint;default:0"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// SessionMember 会话成员表
// 索引：PK(session_id, username) + idx_member_username(username)
//   - PK 复合主键：按会话查成员列表 / 快速判断某用户是否在某会话中
//   - idx_member_username：反查某用户加入的所有会话（联系人列表、会话列表）
type SessionMember struct {
	SessionID   string `gorm:"primaryKey;column:session_id;type:varchar(64);not null"`
	Username    string `gorm:"primaryKey;column:username;type:varchar(64);not null;index:idx_member_username"`
	Role        int    `gorm:"column:role;type:smallint;default:0"` // 0-成员, 1-管理员
	LastReadSeq int64  `gorm:"column:last_read_seq;type:bigint;default:0"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MessageContent 消息内容表
// 索引：PK(event_id) + idx_sess_seq(session_id, seq_id)
//   - idx_sess_seq：按会话拉取历史消息，支持 seq_id 游标分页
//     典型查询: WHERE session_id = ? AND seq_id > ? ORDER BY seq_id LIMIT ?
type MessageContent struct {
	EventID        int64      `gorm:"primaryKey;column:event_id;type:bigint;autoIncrement:false"`
	SessionID      string     `gorm:"column:session_id;type:varchar(64);not null;index:idx_sess_seq,priority:1"`
	SenderUsername string     `gorm:"column:sender_username;type:varchar(64);not null"`
	SeqID          int64      `gorm:"column:seq_id;type:bigint;not null;index:idx_sess_seq,priority:2"`
	Content        string     `gorm:"column:content;type:text"`
	MsgType        int        `gorm:"column:msg_type;type:smallint;not null;default:1"`
	ReplyToEventID int64      `gorm:"column:reply_to_event_id;type:bigint"`
	ClientMsgID    string     `gorm:"column:client_msg_id;type:varchar(64);index:idx_client_msg_id"`
	RecalledAt     *time.Time `gorm:"column:recalled_at"`
	EditedAt       *time.Time `gorm:"column:edited_at"`
	EditCount      int        `gorm:"column:edit_count;type:int;default:0"`
	CreatedAt      time.Time
}

// Inbox 用户信箱表（写扩散）
// 索引：PK(id) + uniq_owner_sess_seq(owner_username, session_id, seq_id) + idx_owner_id(owner_username, id)
type Inbox struct {
	ID            int64  `gorm:"primaryKey;column:id;autoIncrement"`
	OwnerUsername string `gorm:"column:owner_username;type:varchar(64);not null;uniqueIndex:uniq_owner_sess_seq,priority:1;index:idx_owner_id,priority:1;index:idx_owner_sess,priority:1"`
	SessionID     string `gorm:"column:session_id;type:varchar(64);not null;uniqueIndex:uniq_owner_sess_seq,priority:2;index:idx_owner_sess,priority:2"`
	SeqID         int64  `gorm:"column:seq_id;type:bigint;not null;uniqueIndex:uniq_owner_sess_seq,priority:3"`
	EventID       int64  `gorm:"column:event_id;type:bigint;not null"`
	EventType     int    `gorm:"column:event_type;type:smallint;not null"`
	Payload       []byte `gorm:"column:payload;type:bytea;not null"`
	CreatedAt     time.Time
}

// MessageOutbox 本地消息表（Outbox Pattern，可靠投递）
// 索引：PK(id) + idx_event_id(event_id) + idx_status_next_retry(status, next_retry_time)
//   - idx_event_id：按事件 ID 查询投递状态（发送确认、幂等检查）
//   - idx_status_next_retry：定时任务轮询待重试的消息
//     典型查询: WHERE status = 0 AND next_retry_time <= NOW() ORDER BY next_retry_time LIMIT ?
type MessageOutbox struct {
	ID            int64     `gorm:"primaryKey;column:id;autoIncrement"`
	EventID       int64     `gorm:"column:event_id;type:bigint;not null;index:idx_event_id"`
	Topic         string    `gorm:"column:topic;type:varchar(64);not null"`
	Payload       []byte    `gorm:"column:payload;type:bytea;not null"`
	Status        int       `gorm:"column:status;type:smallint;default:0;index:idx_status_next_retry,priority:1"` // 0-待发送, 1-已发送, 2-失败
	RetryCount    int       `gorm:"column:retry_count;type:int;default:0"`
	NextRetryTime time.Time `gorm:"column:next_retry_time;index:idx_status_next_retry,priority:2"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ============================================================================
// 表名映射
// ============================================================================

func (User) TableName() string           { return "t_user" }
func (Session) TableName() string        { return "t_session" }
func (SessionMember) TableName() string  { return "t_session_member" }
func (MessageContent) TableName() string { return "t_message_content" }
func (Inbox) TableName() string          { return "t_inbox" }
func (MessageOutbox) TableName() string  { return "t_message_outbox" }

// ============================================================================
// 常量
// ============================================================================

// Outbox 状态
const (
	OutboxStatusPending = 0
	OutboxStatusSent    = 1
	OutboxStatusFailed  = 2
)

// InboxEventType 枚举与 common.v1.ChatEvent.payload 对齐
const (
	InboxEventTypeMessage       = 1
	InboxEventTypeMessageRecall = 2
	InboxEventTypeMessageEdit   = 3
	InboxEventTypeReadReceipt   = 4
	InboxEventTypeSessionUpdate = 5
)

// AllModels 返回所有需要 AutoMigrate 的模型列表
func AllModels() []any {
	return []any{
		&User{},
		&Session{},
		&SessionMember{},
		&MessageContent{},
		&Inbox{},
		&MessageOutbox{},
	}
}
