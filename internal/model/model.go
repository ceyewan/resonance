package model

import "time"

// Router 存储用户与网关实例的映射关系，通常存储在 Redis 中
type Router struct {
	Username  string `json:"username"`
	GatewayID string `json:"gateway_id"`
	RemoteIP  string `json:"remote_ip"`
	Timestamp int64  `json:"timestamp"`
}

// User 对应 t_user 表
type User struct {
	Username  string `gorm:"primaryKey;column:username;type:varchar(64);not null"`
	Nickname  string `gorm:"column:nickname;type:varchar(64)"`
	Password  string `gorm:"column:password;type:varchar(128);not null"`
	Avatar    string `gorm:"column:avatar;type:varchar(255)"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Session 对应 t_session 表
type Session struct {
	SessionID     string `gorm:"primaryKey;column:session_id;type:varchar(64);not null"`
	Type          int    `gorm:"column:type;type:smallint;not null"` // 1-单聊, 2-群聊
	Name          string `gorm:"column:name;type:varchar(128)"`
	OwnerUsername string `gorm:"column:owner_username;type:varchar(64)"`
	MaxSeqID      int64  `gorm:"column:max_seq_id;type:bigint;default:0"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// SessionMember 对应 t_session_member 表
type SessionMember struct {
	SessionID   string `gorm:"primaryKey;column:session_id;type:varchar(64);not null"`
	Username    string `gorm:"primaryKey;column:username;type:varchar(64);not null"`
	Role        int    `gorm:"column:role;type:smallint;default:0"` // 0-成员, 1-管理员
	LastReadSeq int64  `gorm:"column:last_read_seq;type:bigint;default:0"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MessageContent 对应 t_message_content 表
type MessageContent struct {
	MsgID          int64  `gorm:"primaryKey;column:msg_id;type:bigint;autoIncrement:false"`
	SessionID      string `gorm:"column:session_id;type:varchar(64);not null;index:idx_sess_seq,priority:1"`
	SenderUsername string `gorm:"column:sender_username;type:varchar(64);not null"`
	SeqID          int64  `gorm:"column:seq_id;type:bigint;not null;index:idx_sess_seq,priority:2"`
	Content        string `gorm:"column:content;type:text"`
	MsgType        string `gorm:"column:msg_type;type:varchar(32)"`
	CreatedAt      time.Time
}

// Inbox 对应 t_inbox 表
type Inbox struct {
	ID            int64  `gorm:"primaryKey;column:id;autoIncrement"`
	OwnerUsername string `gorm:"column:owner_username;type:varchar(64);not null;uniqueIndex:uniq_owner_sess_seq,priority:1;index:idx_owner_read,priority:1"`
	SessionID     string `gorm:"column:session_id;type:varchar(64);not null;uniqueIndex:uniq_owner_sess_seq,priority:2"`
	MsgID         int64  `gorm:"column:msg_id;type:bigint;not null"`
	SeqID         int64  `gorm:"column:seq_id;type:bigint;not null;uniqueIndex:uniq_owner_sess_seq,priority:3"`
	IsRead        int    `gorm:"column:is_read;type:smallint;default:0;index:idx_owner_read,priority:2"`
	CreatedAt     time.Time
}

// TableName overrides the default table name
func (User) TableName() string           { return "t_user" }
func (Session) TableName() string        { return "t_session" }
func (SessionMember) TableName() string  { return "t_session_member" }
func (MessageContent) TableName() string { return "t_message_content" }
func (Inbox) TableName() string          { return "t_inbox" }
