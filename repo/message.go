package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ceyewan/resonance/model"
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
		logger = clog.Discard().WithNamespace("message_repo")
	}

	return &messageRepo{
		db:     database,
		logger: logger,
	}, nil
}

// SaveMessageContent 保存消息内容
func (r *messageRepo) SaveMessageContent(ctx context.Context, msg *model.MessageContent) error {
	if msg == nil {
		return fmt.Errorf("message cannot be nil")
	}
	if msg.SessionID == "" {
		return fmt.Errorf("session_id cannot be empty")
	}
	if msg.SenderUsername == "" {
		return fmt.Errorf("sender_username cannot be empty")
	}
	if msg.EventID == 0 {
		return fmt.Errorf("event_id cannot be zero")
	}

	gormDB := r.db.DB(ctx)
	if err := gormDB.Create(msg).Error; err != nil {
		r.logger.Error("保存消息失败",
			clog.String("session_id", msg.SessionID),
			clog.Int64("event_id", msg.EventID),
			clog.Error(err))
		return fmt.Errorf("failed to save message: %w", err)
	}

	r.logger.Debug("保存消息成功",
		clog.String("session_id", msg.SessionID),
		clog.Int64("event_id", msg.EventID),
		clog.Int64("seq_id", msg.SeqID))
	return nil
}

// SaveInboxBatch 批量写入信箱 (写扩散)
func (r *messageRepo) SaveInboxBatch(ctx context.Context, inboxes []*model.Inbox) error {
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
//
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
			return nil, fmt.Errorf("%w: session_id=%s", ErrMessageNotFound, sessionID)
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
	subquery := gormDB.Table(model.MessageContent{}.TableName()).
		Select("session_id, MAX(seq_id) as max_seq_id").
		Where("session_id IN ?", sessionIDs).
		Group("session_id")

	if err := gormDB.Model(&model.MessageContent{}).Where("(session_id, seq_id) IN (?)",
		gormDB.Select("session_id, max_seq_id").Table("(?) as t", subquery)).
		Find(&messages).Error; err != nil {
		r.logger.Error("批量获取最后一条消息失败",
			clog.Int("count", len(sessionIDs)),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get last messages: %w", err)
	}

	return messages, nil
}

// GetInboxDelta 按游标拉取用户增量消息
func (r *messageRepo) GetInboxDelta(ctx context.Context, username string, cursorID int64, limit int) ([]*model.Inbox, error) {
	if username == "" {
		return nil, fmt.Errorf("username cannot be empty")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	items := make([]*model.Inbox, 0)
	gormDB := r.db.DB(ctx)
	if err := gormDB.Where("owner_username = ? AND id > ?", username, cursorID).
		Order("id ASC").
		Limit(limit).
		Find(&items).Error; err != nil {
		r.logger.Error("拉取 inbox 增量失败",
			clog.String("username", username),
			clog.Int64("cursor_id", cursorID),
			clog.Int("limit", limit),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get inbox delta: %w", err)
	}

	return items, nil
}

// GetUnreadMessageCount 获取用户在会话内的未读"消息"数
// 只统计 event_type = InboxEventTypeMessage 的事件：Recall/Edit/ReadReceipt/SessionUpdate
// 属于同步事件流，进入 Inbox 但不计入未读角标，避免撤回/已读回执等污染 badge。
func (r *messageRepo) GetUnreadMessageCount(ctx context.Context, username, sessionID string) (int64, error) {
	if username == "" || sessionID == "" {
		return 0, fmt.Errorf("username and session_id cannot be empty")
	}

	type row struct {
		LastReadSeq int64
	}
	var sess row
	gormDB := r.db.DB(ctx)
	if err := gormDB.Table("t_session_member").
		Select("last_read_seq").
		Where("username = ? AND session_id = ?", username, sessionID).
		Take(&sess).Error; err != nil {
		return 0, fmt.Errorf("failed to get session member: %w", err)
	}

	var count int64
	if err := gormDB.Model(&model.Inbox{}).
		Where("owner_username = ? AND session_id = ? AND seq_id > ? AND event_type = ?",
			username, sessionID, sess.LastReadSeq, model.InboxEventTypeMessage).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed to count unread inbox: %w", err)
	}
	return count, nil
}

// GetMessageByEventID 按 event_id 精确查询消息
func (r *messageRepo) GetMessageByEventID(ctx context.Context, eventID int64) (*model.MessageContent, error) {
	if eventID == 0 {
		return nil, fmt.Errorf("event_id cannot be zero")
	}
	var msg model.MessageContent
	if err := r.db.DB(ctx).Where("event_id = ?", eventID).First(&msg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMessageNotFound
		}
		return nil, fmt.Errorf("get message by event_id: %w", err)
	}
	return &msg, nil
}

// GetMessageByIdempotencyKey 按 (session_id, sender_username, client_msg_id) 查询消息。
func (r *messageRepo) GetMessageByIdempotencyKey(ctx context.Context, sessionID, senderUsername, clientMsgID string) (*model.MessageContent, error) {
	if sessionID == "" || senderUsername == "" || clientMsgID == "" {
		return nil, fmt.Errorf("session_id, sender_username and client_msg_id cannot be empty")
	}

	var msg model.MessageContent
	if err := r.db.DB(ctx).Where(
		"session_id = ? AND sender_username = ? AND client_msg_id = ?",
		sessionID, senderUsername, clientMsgID,
	).Take(&msg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrMessageNotFound
		}
		return nil, fmt.Errorf("get message by idempotency key: %w", err)
	}
	return &msg, nil
}

// MarkMessageRecalled 按 event_id 标记撤回
func (r *messageRepo) MarkMessageRecalled(ctx context.Context, eventID int64, at time.Time) error {
	if eventID == 0 {
		return fmt.Errorf("event_id cannot be zero")
	}
	return r.db.DB(ctx).Model(&model.MessageContent{}).
		Where("event_id = ?", eventID).
		Update("recalled_at", at).Error
}

// UpdateMessageContent 按 event_id 更新消息内容
func (r *messageRepo) UpdateMessageContent(ctx context.Context, eventID int64, newContent string, at time.Time) error {
	if eventID == 0 {
		return fmt.Errorf("event_id cannot be zero")
	}
	return r.db.DB(ctx).Model(&model.MessageContent{}).
		Where("event_id = ?", eventID).
		Updates(map[string]any{
			"content":    newContent,
			"edited_at":  at,
			"edit_count": gorm.Expr("edit_count + ?", 1),
		}).Error
}

// RecallMessageWithOutbox 事务内标记撤回并写 Outbox
// 若 recalled_at 已被设置（消息已撤回），返回 ErrMessageAlreadyRecalled。
func (r *messageRepo) RecallMessageWithOutbox(ctx context.Context, eventID int64, recalledAt time.Time, outbox *model.MessageOutbox) error {
	if eventID == 0 || recalledAt.IsZero() || outbox == nil {
		return fmt.Errorf("event_id, recalled_at and outbox are required")
	}
	return r.db.Transaction(ctx, func(ctx context.Context, tx *gorm.DB) error {
		var target model.MessageContent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("event_id", "session_id", "recalled_at").
			Where("event_id = ?", eventID).Take(&target).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMessageNotFound
			}
			return fmt.Errorf("lock recalled message: %w", err)
		}
		if target.RecalledAt != nil {
			return ErrMessageAlreadyRecalled
		}
		result := tx.Model(&model.MessageContent{}).
			Where("event_id = ? AND recalled_at IS NULL", eventID).
			Update("recalled_at", recalledAt)
		if result.Error != nil {
			return fmt.Errorf("mark recalled: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrMessageAlreadyRecalled
		}
		if err := invalidateAgentConversation(tx, target.SessionID, recalledAt); err != nil {
			return err
		}
		if err := advanceSessionMaxSeqFromOutbox(tx, outbox); err != nil {
			return err
		}
		if err := tx.Create(outbox).Error; err != nil {
			return fmt.Errorf("save recall outbox: %w", err)
		}
		return nil
	})
}

// EditMessageWithOutbox 事务内编辑消息并写 Outbox。
// 若消息已撤回则返回 ErrMessageAlreadyRecalled；若消息不存在则返回 ErrMessageNotFound。
func (r *messageRepo) EditMessageWithOutbox(ctx context.Context, eventID int64, newContent string, editedAt time.Time, outbox *model.MessageOutbox) error {
	if eventID == 0 || editedAt.IsZero() || outbox == nil {
		return fmt.Errorf("event_id, edited_at and outbox are required")
	}
	return r.db.Transaction(ctx, func(ctx context.Context, tx *gorm.DB) error {
		var target model.MessageContent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("event_id", "session_id", "recalled_at").
			Where("event_id = ?", eventID).Take(&target).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMessageNotFound
			}
			return fmt.Errorf("lock edited message: %w", err)
		}
		if target.RecalledAt != nil {
			return ErrMessageAlreadyRecalled
		}
		result := tx.Model(&model.MessageContent{}).
			Where("event_id = ? AND recalled_at IS NULL", eventID).
			Updates(map[string]any{
				"content":    newContent,
				"edited_at":  editedAt,
				"edit_count": gorm.Expr("edit_count + ?", 1),
			})
		if result.Error != nil {
			return fmt.Errorf("edit message: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrMessageNotFound
		}
		if err := invalidateAgentConversation(tx, target.SessionID, editedAt); err != nil {
			return err
		}
		if err := advanceSessionMaxSeqFromOutbox(tx, outbox); err != nil {
			return err
		}
		if err := tx.Create(outbox).Error; err != nil {
			return fmt.Errorf("save edit outbox: %w", err)
		}
		return nil
	})
}

// invalidateAgentConversation is part of the same transaction as Recall/Edit.
// It invalidates every non-terminal Run that could have observed the old
// history, then dirties the opaque Runtime Session. A Run whose final message
// already exists is not cancelled: that user-visible fact is immutable, but
// SessionInvalidatedAt prevents its stale Candidate from becoming the binding.
func invalidateAgentConversation(tx *gorm.DB, sessionID string, invalidatedAt time.Time) error {
	var session model.Session
	if err := tx.Select("session_id", "tenant_id", "kind").Where("session_id = ?", sessionID).Take(&session).Error; err != nil {
		return fmt.Errorf("load session for agent history invalidation: %w", err)
	}
	if session.Kind != model.SessionKindAI {
		return nil
	}
	if session.TenantID == "" {
		return fmt.Errorf("AI session tenant is missing")
	}
	statuses := append(append([]string{}, agentRunPendingStatuses...), agentRunCancellableStatuses...)
	finalFactMissing := `final_event_id = 0 AND NOT EXISTS (
		SELECT 1 FROM t_message_content AS final_message
		WHERE final_message.session_id = t_agent_run.conversation_id
		  AND t_agent_run.final_client_msg_id <> ''
		  AND final_message.client_msg_id = t_agent_run.final_client_msg_id
	)`
	cancelled := tx.Model(&model.AgentRun{}).
		Where("tenant_id = ? AND conversation_id = ? AND status IN ?", session.TenantID, sessionID, statuses).
		Where(finalFactMissing).
		Updates(map[string]any{
			"status":                 model.AgentRunStatusCancelled,
			"session_invalidated_at": invalidatedAt.UTC(),
			"lease_owner":            "",
			"lease_token":            "",
			"lease_expires_at":       nil,
			"completed_at":           invalidatedAt.UTC(),
			"last_error_code":        "history_invalidated",
			"last_error_summary":     "authoritative conversation history changed",
			"last_error_retryable":   false,
			"version":                gorm.Expr("version + 1"),
			"updated_at":             invalidatedAt.UTC(),
		})
	if cancelled.Error != nil {
		return fmt.Errorf("cancel runs using invalidated history: %w", cancelled.Error)
	}
	acknowledged := tx.Model(&model.AgentRun{}).
		Where("tenant_id = ? AND conversation_id = ? AND status IN ? AND session_invalidated_at IS NULL",
			session.TenantID, sessionID, statuses).
		Update("session_invalidated_at", invalidatedAt.UTC())
	if acknowledged.Error != nil {
		return fmt.Errorf("mark acknowledged runs using invalidated history: %w", acknowledged.Error)
	}
	binding := tx.Model(&model.AgentSessionBinding{}).
		Where("tenant_id = ? AND conversation_id = ? AND status IN ?", session.TenantID, sessionID,
			[]string{model.AgentSessionBindingStatusActive, model.AgentSessionBindingStatusDirty}).
		Updates(map[string]any{
			"status":     model.AgentSessionBindingStatusDirty,
			"version":    gorm.Expr("version + 1"),
			"updated_at": invalidatedAt.UTC(),
		})
	if binding.Error != nil {
		return fmt.Errorf("dirty session binding after history mutation: %w", binding.Error)
	}
	return nil
}

func validateAgentFinalMessageCommit(tx *gorm.DB, message *model.MessageContent) error {
	if !strings.HasPrefix(message.ClientMsgID, "agent:") {
		return nil
	}
	const prefix, suffix = "agent:", ":final"
	if !strings.HasSuffix(message.ClientMsgID, suffix) {
		return ErrAgentFinalMessageNotCommittable
	}
	runID := strings.TrimSuffix(strings.TrimPrefix(message.ClientMsgID, prefix), suffix)
	if !validBoundedString(runID, 52) || message.SessionID == "" || message.SenderUsername == "" {
		return ErrAgentFinalMessageNotCommittable
	}

	var session model.Session
	if err := tx.Select("session_id", "tenant_id", "kind", "profile_id", "profile_version").
		Where("session_id = ?", message.SessionID).Take(&session).Error; err != nil {
		return ErrAgentFinalMessageNotCommittable
	}
	if session.Kind != model.SessionKindAI || session.TenantID == "" {
		return ErrAgentFinalMessageNotCommittable
	}
	run, err := lockAgentRun(tx, session.TenantID, runID)
	if err != nil {
		return ErrAgentFinalMessageNotCommittable
	}
	if run.Status != model.AgentRunStatusCommitting || run.SessionInvalidatedAt != nil ||
		run.ConversationID != message.SessionID || run.ProfileID != session.ProfileID ||
		run.ProfileVersion != session.ProfileVersion || run.FinalClientMsgID != message.ClientMsgID ||
		run.FrozenFinalText == "" || run.FrozenFinalText != message.Content {
		return ErrAgentFinalMessageNotCommittable
	}

	var sender model.User
	if err := tx.Select("username", "kind").Where("username = ?", message.SenderUsername).Take(&sender).Error; err != nil ||
		sender.Kind != model.UserKindAgentBot {
		return ErrAgentFinalMessageNotCommittable
	}
	var memberCount int64
	if err := tx.Model(&model.SessionMember{}).
		Where("session_id = ? AND username = ?", message.SessionID, message.SenderUsername).
		Count(&memberCount).Error; err != nil || memberCount != 1 {
		return ErrAgentFinalMessageNotCommittable
	}

	var binding model.AgentSessionBinding
	bindingErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND conversation_id = ?", session.TenantID, message.SessionID).
		Take(&binding).Error
	if run.BaseSessionGeneration == 0 {
		if bindingErr == nil || !errors.Is(bindingErr, gorm.ErrRecordNotFound) {
			return ErrAgentFinalMessageNotCommittable
		}
		return nil
	}
	if bindingErr != nil || binding.Generation != run.BaseSessionGeneration ||
		(binding.Status != model.AgentSessionBindingStatusActive && binding.Status != model.AgentSessionBindingStatusDirty) {
		return ErrAgentFinalMessageNotCommittable
	}
	return nil
}

// SaveMessageWithOutbox 事务内幂等保存消息并记录本地消息表。
// 非空 ClientMsgID 使用 (session, sender, client_msg_id) 唯一键；同键重试只返回第一次结果。
func (r *messageRepo) SaveMessageWithOutbox(ctx context.Context, msg *model.MessageContent, outbox *model.MessageOutbox) (*MessageSaveResult, error) {
	if msg == nil || outbox == nil {
		return nil, fmt.Errorf("message and outbox cannot be nil")
	}
	if msg.ClientMsgID != "" && msg.IdempotencyHash == "" {
		return nil, fmt.Errorf("idempotency_hash is required when client_msg_id is set")
	}

	var saved *MessageSaveResult
	err := r.db.Transaction(ctx, func(ctx context.Context, tx *gorm.DB) error {
		if err := validateAgentFinalMessageCommit(tx, msg); err != nil {
			return err
		}
		// 1. 保存消息内容。非空 ClientMsgID 只允许部分唯一索引上的冲突降级为查询。
		var createResult *gorm.DB
		if msg.ClientMsgID != "" {
			createResult = tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{
					{Name: "session_id"},
					{Name: "sender_username"},
					{Name: "client_msg_id"},
				},
				TargetWhere: clause.Where{Exprs: []clause.Expression{
					clause.Expr{SQL: "client_msg_id <> ''"},
				}},
				DoNothing: true,
			}).Create(msg)
		} else {
			createResult = tx.Create(msg)
		}
		if createResult.Error != nil {
			return fmt.Errorf("failed to save message: %w", createResult.Error)
		}
		if createResult.RowsAffected == 0 {
			var existing model.MessageContent
			if err := tx.Where(
				"session_id = ? AND sender_username = ? AND client_msg_id = ?",
				msg.SessionID, msg.SenderUsername, msg.ClientMsgID,
			).Take(&existing).Error; err != nil {
				return fmt.Errorf("load idempotent message: %w", err)
			}
			if existing.IdempotencyHash == "" || existing.IdempotencyHash != msg.IdempotencyHash {
				return ErrMessageIdempotencyConflict
			}
			saved = &MessageSaveResult{Message: &existing, Created: false}
			return nil
		}
		if createResult.RowsAffected != 1 {
			return fmt.Errorf("unexpected inserted message count: %d", createResult.RowsAffected)
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

		saved = &MessageSaveResult{Message: msg, Outbox: outbox, Created: true}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if saved == nil || saved.Message == nil {
		return nil, fmt.Errorf("message transaction returned no result")
	}
	return saved, nil
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
	if err := gormDB.Model(&model.MessageOutbox{}).Where("id = ?", id).Updates(map[string]any{
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
