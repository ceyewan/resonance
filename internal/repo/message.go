package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/db"
	"github.com/ceyewan/resonance/internal/model"
	"gorm.io/gorm"
)

// MessageRepoOption 配置 MessageRepo 的选项
type MessageRepoOption func(*messageRepoOptions)

type messageRepoOptions struct {
	logger clog.Logger
}

// WithMessageRepoLogger 设置日志记录器
func WithMessageRepoLogger(logger clog.Logger) MessageRepoOption {
	return func(o *messageRepoOptions) {
		o.logger = logger
	}
}

// messageRepo 实现 MessageRepo 接口
type messageRepo struct {
	db     db.DB
	logger clog.Logger
}

// NewMessageRepo 创建 MessageRepo 实例
func NewMessageRepo(database db.DB, opts ...MessageRepoOption) (MessageRepo, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}

	options := &messageRepoOptions{}
	for _, opt := range opts {
		opt(options)
	}

	// 提供默认 logger
	var logger clog.Logger
	if options.logger != nil {
		logger = options.logger.WithNamespace("message_repo")
	} else {
		var err error
		logger, err = clog.New(&clog.Config{
			Level:  "info",
			Format: "json",
			Output: "/dev/null",
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create default logger: %w", err)
		}
		logger = logger.WithNamespace("message_repo")
	}

	// 自动迁移表结构
	// 注意：生产环境建议使用专门的 migration 工具管理 schema，此处仅为简化开发
	if err := database.DB(context.Background()).AutoMigrate(&model.MessageOutbox{}); err != nil {
		return nil, fmt.Errorf("failed to migrate outbox table: %w", err)
	}

	return &messageRepo{
		db:     database,
		logger: logger,
	}, nil
}

// SaveMessage 保存消息内容
func (r *messageRepo) SaveMessage(ctx context.Context, msg *model.MessageContent) error {
	if msg == nil {
		return fmt.Errorf("message cannot be nil")
	}
	if msg.SessionID == "" {
		return fmt.Errorf("session_id cannot be empty")
	}
	if msg.SenderUsername == "" {
		return fmt.Errorf("sender_username cannot be empty")
	}
	if msg.MsgID == 0 {
		return fmt.Errorf("msg_id cannot be zero")
	}

	gormDB := r.db.DB(ctx)
	if err := gormDB.Create(msg).Error; err != nil {
		r.logger.Error("保存消息失败",
			clog.String("session_id", msg.SessionID),
			clog.Int64("msg_id", msg.MsgID),
			clog.Error(err))
		return fmt.Errorf("failed to save message: %w", err)
	}

	r.logger.Debug("保存消息成功",
		clog.String("session_id", msg.SessionID),
		clog.Int64("msg_id", msg.MsgID),
		clog.Int64("seq_id", msg.SeqID))
	return nil
}

// SaveInbox 批量写入信箱 (写扩散)
func (r *messageRepo) SaveInbox(ctx context.Context, inboxes []*model.Inbox) error {
	if len(inboxes) == 0 {
		return nil
	}

	// 使用事务批量写入
	err := r.db.Transaction(ctx, func(ctx context.Context, tx *gorm.DB) error {
		if err := tx.Create(&inboxes).Error; err != nil {
			return fmt.Errorf("failed to save inboxes: %w", err)
		}
		return nil
	})

	if err != nil {
		r.logger.Error("批量写入信箱失败",
			clog.Int("count", len(inboxes)),
			clog.Error(err))
		return err
	}

	r.logger.Debug("批量写入信箱成功", clog.Int("count", len(inboxes)))
	return nil
}

// GetHistoryMessages 拉取历史消息
func (r *messageRepo) GetHistoryMessages(ctx context.Context, sessionID string, startSeq int64, limit int) ([]*model.MessageContent, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id cannot be empty")
	}
	if limit <= 0 {
		limit = 50 // 默认拉取50条
	}
	if limit > 1000 {
		limit = 1000 // 最大拉取1000条
	}

	var messages []*model.MessageContent
	gormDB := r.db.DB(ctx)

	query := gormDB.Where("session_id = ?", sessionID).
		Order("seq_id ASC") // 按序列号升序

	// 如果指定了起始序列号，则从该位置开始查询
	if startSeq > 0 {
		query = query.Where("seq_id >= ?", startSeq)
	}

	if err := query.Limit(limit).Find(&messages).Error; err != nil {
		r.logger.Error("拉取历史消息失败",
			clog.String("session_id", sessionID),
			clog.Int64("start_seq", startSeq),
			clog.Int("limit", limit),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get history messages: %w", err)
	}

	return messages, nil
}

// GetLastMessage 获取会话的最后一条消息
func (r *messageRepo) GetLastMessage(ctx context.Context, sessionID string) (*model.MessageContent, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id cannot be empty")
	}

	var message model.MessageContent
	gormDB := r.db.DB(ctx)

	// 按序列号降序获取最后一条消息
	if err := gormDB.Where("session_id = ?", sessionID).
		Order("seq_id DESC").
		First(&message).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("no message found in session: %s", sessionID)
		}
		r.logger.Error("获取最后一条消息失败",
			clog.String("session_id", sessionID),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get last message: %w", err)
	}

	return &message, nil
}

// GetUnreadMessages 获取用户未读消息 (从小群信箱)
func (r *messageRepo) GetUnreadMessages(ctx context.Context, username string, limit int) ([]*model.Inbox, error) {
	if username == "" {
		return nil, fmt.Errorf("username cannot be empty")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	var inboxes []*model.Inbox
	gormDB := r.db.DB(ctx)

	// 查询用户的未读消息
	if err := gormDB.Where("owner_username = ? AND is_read = 0", username).
		Order("created_at DESC").
		Limit(limit).
		Find(&inboxes).Error; err != nil {
		r.logger.Error("获取未读消息失败",
			clog.String("username", username),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get unread messages: %w", err)
	}

	return inboxes, nil
}

// SaveMessageWithOutbox 事务内保存消息并记录本地消息表
func (r *messageRepo) SaveMessageWithOutbox(ctx context.Context, msg *model.MessageContent, outbox *model.MessageOutbox) error {
	if msg == nil || outbox == nil {
		return fmt.Errorf("message and outbox cannot be nil")
	}

	return r.db.Transaction(ctx, func(ctx context.Context, tx *gorm.DB) error {
		// 1. 保存消息内容
		if err := tx.Create(msg).Error; err != nil {
			return fmt.Errorf("failed to save message: %w", err)
		}

		// 2. 更新会话 MaxSeqID (使用 CAS 乐观锁防止回退)
		result := tx.Model(&model.Session{}).
			Where("session_id = ? AND max_seq_id < ?", msg.SessionID, msg.SeqID).
			Update("max_seq_id", msg.SeqID)
		if result.Error != nil {
			return fmt.Errorf("failed to update session max_seq_id: %w", result.Error)
		}

		// 3. 保存到本地消息表
		if err := tx.Create(outbox).Error; err != nil {
			return fmt.Errorf("failed to save outbox: %w", err)
		}

		return nil
	})
}

// UpdateOutboxStatus 更新本地消息表状态
func (r *messageRepo) UpdateOutboxStatus(ctx context.Context, id int64, status int) error {
	gormDB := r.db.DB(ctx)
	if err := gormDB.Model(&model.MessageOutbox{}).Where("id = ?", id).Update("status", status).Error; err != nil {
		return fmt.Errorf("failed to update outbox status: %w", err)
	}
	return nil
}

// UpdateOutboxRetry 更新本地消息表重试信息
func (r *messageRepo) UpdateOutboxRetry(ctx context.Context, id int64, nextRetry time.Time, count int) error {
	gormDB := r.db.DB(ctx)
	if err := gormDB.Model(&model.MessageOutbox{}).Where("id = ?", id).Updates(map[string]interface{}{
		"next_retry_time": nextRetry,
		"retry_count":     count,
	}).Error; err != nil {
		return fmt.Errorf("failed to update outbox retry: %w", err)
	}
	return nil
}

// GetPendingOutboxMessages 获取待发送的本地消息
func (r *messageRepo) GetPendingOutboxMessages(ctx context.Context, limit int) ([]*model.MessageOutbox, error) {
	var messages []*model.MessageOutbox
	gormDB := r.db.DB(ctx)

	if err := gormDB.Where("status = ? AND next_retry_time <= ?", model.OutboxStatusPending, time.Now()).
		Limit(limit).
		Find(&messages).Error; err != nil {
		return nil, fmt.Errorf("failed to get pending outbox messages: %w", err)
	}

	return messages, nil
}

// Close 释放资源
func (r *messageRepo) Close() error {
	r.logger.Info("关闭 MessageRepo")
	// db 实例由外部管理，这里不需要关闭
	return nil
}
