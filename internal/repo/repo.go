package repo

import (
	"context"

	"github.com/ceyewan/resonance/internal/model"
)

// RouterRepo 定义了路由表（用户与网关实例映射）的数据访问接口，通常由 Redis 实现
type RouterRepo interface {
	// SetUserGateway 设置用户的网关映射关系
	SetUserGateway(ctx context.Context, router *model.Router) error
	// GetUserGateway 获取用户的网关映射关系
	GetUserGateway(ctx context.Context, username string) (*model.Router, error)
	// DeleteUserGateway 删除用户的网关映射关系
	DeleteUserGateway(ctx context.Context, username string) error
	// BatchGetUsersGateway 批量获取用户的网关映射关系
	BatchGetUsersGateway(ctx context.Context, usernames []string) ([]*model.Router, error)
	// Close 释放资源（如数据库连接等）
	Close() error
}

// UserRepo 定义了用户数据访问接口
type UserRepo interface {
	// CreateUser 创建新用户
	CreateUser(ctx context.Context, user *model.User) error
	// GetUserByUsername 根据用户名获取用户
	GetUserByUsername(ctx context.Context, username string) (*model.User, error)
	// SearchUsers 搜索用户
	SearchUsers(ctx context.Context, query string) ([]*model.User, error)
	// UpdateUser 更新用户信息
	UpdateUser(ctx context.Context, user *model.User) error
	// Close 释放资源（如数据库连接等）
	Close() error
}

// SessionRepo 定义了会话数据访问接口
type SessionRepo interface {
	// CreateSession 创建会话
	CreateSession(ctx context.Context, session *model.Session) error
	// GetSession 获取会话详情
	GetSession(ctx context.Context, sessionID string) (*model.Session, error)
	// GetUserSession 获取特定用户的特定会话详情（包含最后阅读位置）
	GetUserSession(ctx context.Context, username, sessionID string) (*model.SessionMember, error)
	// GetUserSessionList 获取用户的所有会话列表
	GetUserSessionList(ctx context.Context, username string) ([]*model.Session, error)
	// AddMember 添加成员
	AddMember(ctx context.Context, member *model.SessionMember) error
	// GetMembers 获取会话成员
	GetMembers(ctx context.Context, sessionID string) ([]*model.SessionMember, error)
	// UpdateMaxSeqID 更新会话最新序列号 (CAS操作)
	UpdateMaxSeqID(ctx context.Context, sessionID string, newSeqID int64) error
	// GetContactList 获取联系人列表（有过单聊关系的用户）
	GetContactList(ctx context.Context, username string) ([]*model.User, error)
	// UpdateLastReadSeq 更新用户在会话中的已读位置
	UpdateLastReadSeq(ctx context.Context, sessionID, username string, lastReadSeq int64) error
	// Close 释放资源（如数据库连接等）
	Close() error
}

// MessageRepo 定义了消息数据访问接口
type MessageRepo interface {
	// SaveMessage 保存消息内容
	SaveMessage(ctx context.Context, msg *model.MessageContent) error
	// SaveInbox 批量写入信箱 (写扩散)
	SaveInbox(ctx context.Context, inboxes []*model.Inbox) error
	// GetHistoryMessages 拉取历史消息
	GetHistoryMessages(ctx context.Context, sessionID string, startSeq int64, limit int) ([]*model.MessageContent, error)
	// GetLastMessage 获取会话的最后一条消息
	GetLastMessage(ctx context.Context, sessionID string) (*model.MessageContent, error)
	// GetUnreadMessages 获取用户未读消息 (从小群信箱)
	GetUnreadMessages(ctx context.Context, username string, limit int) ([]*model.Inbox, error)
	// Close 释放资源（如数据库连接等）
	Close() error
}
