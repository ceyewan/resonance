package repo

import (
	"context"
	"fmt"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/db"
	"github.com/ceyewan/resonance/model"
	"gorm.io/gorm"
)

// SessionRepoOption 配置 SessionRepo 的选项
type SessionRepoOption func(*sessionRepoOptions)

type sessionRepoOptions struct {
	logger clog.Logger
}

// WithSessionRepoLogger 设置日志记录器
func WithSessionRepoLogger(logger clog.Logger) SessionRepoOption {
	return func(o *sessionRepoOptions) {
		o.logger = logger
	}
}

// sessionRepo 实现 SessionRepo 接口
type sessionRepo struct {
	db     db.DB
	logger clog.Logger
}

// NewSessionRepo 创建 SessionRepo 实例
func NewSessionRepo(database db.DB, opts ...SessionRepoOption) (SessionRepo, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}

	options := &sessionRepoOptions{}
	for _, opt := range opts {
		opt(options)
	}

	// 提供默认 logger
	var logger clog.Logger
	if options.logger != nil {
		logger = options.logger.WithNamespace("session_repo")
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
		logger = logger.WithNamespace("session_repo")
	}

	return &sessionRepo{
		db:     database,
		logger: logger,
	}, nil
}

// CreateSession 创建会话
func (r *sessionRepo) CreateSession(ctx context.Context, session *model.Session) error {
	if session == nil {
		return fmt.Errorf("session cannot be nil")
	}
	if session.SessionID == "" {
		return fmt.Errorf("session_id cannot be empty")
	}

	gormDB := r.db.DB(ctx)
	if err := gormDB.Create(session).Error; err != nil {
		r.logger.Error("创建会话失败",
			clog.String("session_id", session.SessionID),
			clog.Error(err))
		return fmt.Errorf("failed to create session: %w", err)
	}

	r.logger.Info("创建会话成功", clog.String("session_id", session.SessionID))
	return nil
}

// GetSession 获取会话详情
func (r *sessionRepo) GetSession(ctx context.Context, sessionID string) (*model.Session, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id cannot be empty")
	}

	var session model.Session
	gormDB := r.db.DB(ctx)
	if err := gormDB.Where("session_id = ?", sessionID).First(&session).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("session not found: %s", sessionID)
		}
		r.logger.Error("获取会话失败",
			clog.String("session_id", sessionID),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	return &session, nil
}

// GetUserSession 获取特定用户的特定会话详情（包含最后阅读位置）
func (r *sessionRepo) GetUserSession(ctx context.Context, username, sessionID string) (*model.SessionMember, error) {
	if username == "" {
		return nil, fmt.Errorf("username cannot be empty")
	}
	if sessionID == "" {
		return nil, fmt.Errorf("session_id cannot be empty")
	}

	var member model.SessionMember
	gormDB := r.db.DB(ctx)
	if err := gormDB.Where("session_id = ? AND username = ?", sessionID, username).
		First(&member).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user session not found: username=%s, session_id=%s", username, sessionID)
		}
		r.logger.Error("获取用户会话失败",
			clog.String("username", username),
			clog.String("session_id", sessionID),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get user session: %w", err)
	}

	return &member, nil
}

// GetUserSessionsBatch 批量获取用户的会话信息（避免 N+1 查询）
func (r *sessionRepo) GetUserSessionsBatch(ctx context.Context, username string, sessionIDs []string) ([]*model.SessionMember, error) {
	if username == "" {
		return nil, fmt.Errorf("username cannot be empty")
	}
	if len(sessionIDs) == 0 {
		return []*model.SessionMember{}, nil
	}

	var members []*model.SessionMember
	gormDB := r.db.DB(ctx)
	if err := gormDB.Where("username = ? AND session_id IN ?", username, sessionIDs).
		Find(&members).Error; err != nil {
		r.logger.Error("批量获取用户会话失败",
			clog.String("username", username),
			clog.Int("count", len(sessionIDs)),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get user sessions: %w", err)
	}

	return members, nil
}

// GetUserSessionList 获取用户的所有会话列表
func (r *sessionRepo) GetUserSessionList(ctx context.Context, username string) ([]*model.Session, error) {
	if username == "" {
		return nil, fmt.Errorf("username cannot be empty")
	}

	gormDB := r.db.DB(ctx)

	// 从 t_session_member 表中获取用户所在的会话ID
	var sessionIDs []string
	if err := gormDB.Model(&model.SessionMember{}).
		Where("username = ?", username).
		Pluck("session_id", &sessionIDs).Error; err != nil {
		r.logger.Error("获取用户会话列表失败",
			clog.String("username", username),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get user session list: %w", err)
	}

	// 如果没有会话，返回空列表
	if len(sessionIDs) == 0 {
		return []*model.Session{}, nil
	}

	// 获取会话详情
	var sessions []*model.Session
	if err := gormDB.Where("session_id IN ?", sessionIDs).Find(&sessions).Error; err != nil {
		r.logger.Error("获取会话详情失败",
			clog.String("username", username),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get session details: %w", err)
	}

	return sessions, nil
}

// AddMember 添加成员
func (r *sessionRepo) AddMember(ctx context.Context, member *model.SessionMember) error {
	if member == nil {
		return fmt.Errorf("member cannot be nil")
	}
	if member.SessionID == "" {
		return fmt.Errorf("session_id cannot be empty")
	}
	if member.Username == "" {
		return fmt.Errorf("username cannot be empty")
	}

	gormDB := r.db.DB(ctx)
	if err := gormDB.Create(member).Error; err != nil {
		r.logger.Error("添加成员失败",
			clog.String("session_id", member.SessionID),
			clog.String("username", member.Username),
			clog.Error(err))
		return fmt.Errorf("failed to add member: %w", err)
	}

	r.logger.Info("添加成员成功",
		clog.String("session_id", member.SessionID),
		clog.String("username", member.Username))
	return nil
}

// GetMembers 获取会话成员
func (r *sessionRepo) GetMembers(ctx context.Context, sessionID string) ([]*model.SessionMember, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id cannot be empty")
	}

	var members []*model.SessionMember
	gormDB := r.db.DB(ctx)
	if err := gormDB.Where("session_id = ?", sessionID).Find(&members).Error; err != nil {
		r.logger.Error("获取会话成员失败",
			clog.String("session_id", sessionID),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get members: %w", err)
	}

	return members, nil
}

// UpdateMaxSeqID 更新会话最新序列号 (CAS操作)
func (r *sessionRepo) UpdateMaxSeqID(ctx context.Context, sessionID string, newSeqID int64) error {
	if sessionID == "" {
		return fmt.Errorf("session_id cannot be empty")
	}

	gormDB := r.db.DB(ctx)

	// 使用乐观锁更新：只有当 newSeqID > 当前 max_seq_id 时才更新
	result := gormDB.Model(&model.Session{}).
		Where("session_id = ? AND max_seq_id < ?", sessionID, newSeqID).
		Update("max_seq_id", newSeqID)

	if result.Error != nil {
		r.logger.Error("更新会话序列号失败",
			clog.String("session_id", sessionID),
			clog.Int64("new_seq_id", newSeqID),
			clog.Error(result.Error))
		return fmt.Errorf("failed to update max seq id: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		// 可能是会话不存在，或者 newSeqID 不大于当前值
		var session model.Session
		if err := gormDB.Where("session_id = ?", sessionID).First(&session).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf("session not found: %s", sessionID)
			}
			return fmt.Errorf("failed to check session: %w", err)
		}
		// 会话存在但序列号没有更新，说明 newSeqID <= 当前值
		r.logger.Debug("会话序列号未更新，因为新值不大于当前值",
			clog.String("session_id", sessionID),
			clog.Int64("current_max_seq_id", session.MaxSeqID),
			clog.Int64("new_seq_id", newSeqID))
	}

	return nil
}

// GetContactList 获取联系人列表（有过单聊关系的用户）
func (r *sessionRepo) GetContactList(ctx context.Context, username string) ([]*model.User, error) {
	if username == "" {
		return nil, fmt.Errorf("username cannot be empty")
	}

	gormDB := r.db.DB(ctx)

	// 1. 查找用户所在的所有单聊会话（type = 1）
	// 使用原生 SQL 查询，避免 GORM 表别名问题
	var singleChatSessions []string
	sql := `
		SELECT sm.session_id
		FROM t_session_member sm
		INNER JOIN t_session s ON sm.session_id = s.session_id
		WHERE sm.username = ? AND s.type = 1
	`
	if err := gormDB.Raw(sql, username).Pluck("session_id", &singleChatSessions).Error; err != nil {
		r.logger.Error("获取单聊会话列表失败",
			clog.String("username", username),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get single chat sessions: %w", err)
	}

	// 如果没有单聊会话，返回空列表
	if len(singleChatSessions) == 0 {
		return []*model.User{}, nil
	}

	// 2. 获取这些会话中的其他成员
	var contactUsernames []string
	if err := gormDB.Model(&model.SessionMember{}).
		Where("session_id IN ? AND username != ?", singleChatSessions, username).
		Distinct("username").
		Pluck("username", &contactUsernames).Error; err != nil {
		r.logger.Error("获取联系人用户名失败",
			clog.String("username", username),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get contact usernames: %w", err)
	}

	// 如果没有联系人，返回空列表
	if len(contactUsernames) == 0 {
		return []*model.User{}, nil
	}

	// 3. 获取联系人详情
	var contacts []*model.User
	if err := gormDB.Where("username IN ?", contactUsernames).Find(&contacts).Error; err != nil {
		r.logger.Error("获取联系人详情失败",
			clog.String("username", username),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get contact details: %w", err)
	}

	return contacts, nil
}

// UpdateLastReadSeq 更新用户在会话中的已读位置
func (r *sessionRepo) UpdateLastReadSeq(ctx context.Context, sessionID, username string, lastReadSeq int64) error {
	if sessionID == "" || username == "" {
		return fmt.Errorf("session_id or username cannot be empty")
	}

	gormDB := r.db.DB(ctx)

	// 只有当 newSeq > currentSeq 时才更新，防止回退
	result := gormDB.Model(&model.SessionMember{}).
		Where("session_id = ? AND username = ? AND last_read_seq < ?", sessionID, username, lastReadSeq).
		Update("last_read_seq", lastReadSeq)

	if result.Error != nil {
		r.logger.Error("更新用户已读位置失败",
			clog.String("session_id", sessionID),
			clog.String("username", username),
			clog.Int64("last_read_seq", lastReadSeq),
			clog.Error(result.Error))
		return fmt.Errorf("failed to update last read seq: %w", result.Error)
	}

	return nil
}

// Close 释放资源
func (r *sessionRepo) Close() error {
	r.logger.Info("关闭 SessionRepo")
	// db 实例由外部管理，这里不需要关闭
	return nil
}
