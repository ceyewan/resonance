package model

import "time"

// MessageOutbox 对应 t_message_outbox 表，用于实现本地消息表模式 (Outbox Pattern)
type MessageOutbox struct {
	ID            int64     `gorm:"primaryKey;column:id;autoIncrement"`
	MsgID         int64     `gorm:"column:msg_id;type:bigint unsigned;not null;index:idx_msg_id"`
	Topic         string    `gorm:"column:topic;type:varchar(64);not null"`
	Payload       []byte    `gorm:"column:payload;type:blob;not null"`
	Status        int       `gorm:"column:status;type:tinyint;default:0;index:idx_status_next_retry,priority:1"` // 0-待发送, 1-已发送, 2-失败
	RetryCount    int       `gorm:"column:retry_count;type:int;default:0"`
	NextRetryTime time.Time `gorm:"column:next_retry_time;index:idx_status_next_retry,priority:2"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// TableName overrides the default table name
func (MessageOutbox) TableName() string { return "t_message_outbox" }

// Outbox Status Constants
const (
	OutboxStatusPending = 0
	OutboxStatusSent    = 1
	OutboxStatusFailed  = 2
)
