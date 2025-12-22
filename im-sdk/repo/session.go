package repo

import (
	"context"

	gatewayv1 "github.com/ceyewan/resonance/im-api/gen/go/gateway/v1"
	logicv1 "github.com/ceyewan/resonance/im-api/gen/go/logic/v1"
)

// SessionRepository 会话仓储接口
type SessionRepository interface {
	// CreateSession 创建会话
	CreateSession(ctx context.Context, creatorUsername string, members []string, name string, sessionType int32) (string, error)

	// GetSessionByID 根据 ID 获取会话
	GetSessionByID(ctx context.Context, sessionID string) (*logicv1.SessionInfo, error)

	// GetUserSessions 获取用户的所有会话
	GetUserSessions(ctx context.Context, username string) ([]*logicv1.SessionInfo, error)

	// GetSessionMembers 获取会话成员列表
	GetSessionMembers(ctx context.Context, sessionID string) ([]string, error)

	// IsSessionMember 检查用户是否是会话成员
	IsSessionMember(ctx context.Context, sessionID string, username string) (bool, error)

	// UpdateLastReadSeq 更新用户在会话中的已读序列号
	UpdateLastReadSeq(ctx context.Context, sessionID string, username string, seqID int64) error

	// GetUnreadCount 获取未读消息数
	GetUnreadCount(ctx context.Context, sessionID string, username string) (int64, error)
}

// ContactRepository 联系人仓储接口
type ContactRepository interface {
	// GetContacts 获取用户的联系人列表
	GetContacts(ctx context.Context, username string) ([]*logicv1.ContactInfo, error)

	// AddContact 添加联系人
	AddContact(ctx context.Context, username string, contactUsername string) error
}

