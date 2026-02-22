package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/db"
	"github.com/ceyewan/resonance/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
		// 幂等写入：唯一键冲突（owner_username, session_id, seq_id）时忽略
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&inboxes).Error; err != nil {
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
// 语义：
//   - beforeSeq == 0: 拉取该会话“最近”的 limit 条消息
//   - beforeSeq > 0: 拉取 seq_id < beforeSeq 的历史消息
// 返回顺序统一为 seq_id 升序，方便前端直接渲染。
func (r *messageRepo) GetHistoryMessages(ctx context.Context, sessionID string, beforeSeq int64, limit int) ([]*model.MessageContent, error) {
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

	query := gormDB.Where("session_id = ?", sessionID)
	if beforeSeq > 0 {
		query = query.Where("seq_id < ?", beforeSeq)
	}

	// 为了高效拿“最近 limit 条”，先倒序取，再在内存反转为升序输出。
	query = query.Order("seq_id DESC")

	if err := query.Limit(limit).Find(&messages).Error; err != nil {
		r.logger.Error("拉取历史消息失败",
			clog.String("session_id", sessionID),
			clog.Int64("before_seq", beforeSeq),
			clog.Int("limit", limit),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get history messages: %w", err)
	}

	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
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

// GetLastMessagesBatch 批量获取会话的最后一条消息（避免 N+1 查询）
func (r *messageRepo) GetLastMessagesBatch(ctx context.Context, sessionIDs []string) ([]*model.MessageContent, error) {
	if len(sessionIDs) == 0 {
		return []*model.MessageContent{}, nil
	}

	var messages []*model.MessageContent
	gormDB := r.db.DB(ctx)

	// 使用子查询获取每个会话的最后一条消息
	// 子查询：对每个 session_id，获取 seq_id 最大的消息
	subquery := gormDB.Select("session_id, MAX(seq_id) as max_seq_id").
		Where("session_id IN ?", sessionIDs).
		Group("session_id")

	if err := gormDB.Where("(session_id, seq_id) IN (?)",
		gormDB.Select("session_id, max_seq_id").Table("(?) as t", subquery)).
		Find(&messages).Error; err != nil {
		r.logger.Error("批量获取最后一条消息失败",
			clog.Int("count", len(sessionIDs)),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get last messages: %w", err)
	}

	return messages, nil
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

// GetInboxDelta 按游标拉取用户增量消息
func (r *messageRepo) GetInboxDelta(ctx context.Context, username string, cursorID int64, limit int) ([]*InboxDeltaItem, error) {
	if username == "" {
		return nil, fmt.Errorf("username cannot be empty")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	type inboxRow struct {
		InboxID        int64
		MsgID          int64
		SeqID          int64
		SessionID      string
		SenderUsername string
		Content        string
		MsgType        string
		CreatedAt      time.Time
	}

	rows := make([]*inboxRow, 0)
	gormDB := r.db.DB(ctx)
	if err := gormDB.Table("t_inbox i").
		Select(`
			i.id AS inbox_id,
			m.msg_id AS msg_id,
			m.seq_id AS seq_id,
			m.session_id AS session_id,
			m.sender_username AS sender_username,
			m.content AS content,
			m.msg_type AS msg_type,
			m.created_at AS created_at
		`).
		Joins("INNER JOIN t_message_content m ON m.msg_id = i.msg_id").
		Where("i.owner_username = ? AND i.id > ?", username, cursorID).
		Order("i.id ASC").
		Limit(limit).
		Scan(&rows).Error; err != nil {
		r.logger.Error("拉取 inbox 增量失败",
			clog.String("username", username),
			clog.Int64("cursor_id", cursorID),
			clog.Int("limit", limit),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get inbox delta: %w", err)
	}

	items := make([]*InboxDeltaItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, &InboxDeltaItem{
			InboxID:        row.InboxID,
			MsgID:          row.MsgID,
			SeqID:          row.SeqID,
			SessionID:      row.SessionID,
			SenderUsername: row.SenderUsername,
			Content:        row.Content,
			MsgType:        row.MsgType,
			CreatedAt:      row.CreatedAt,
		})
	}

	return items, nil
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
