package repo

import (
	"context"

	gatewayv1 "github.com/ceyewan/resonance/im-api/gen/go/gateway/v1"
)

// MessageRepository 消息仓储接口
type MessageRepository interface {
	// SaveMessage 保存消息
	SaveMessage(ctx context.Context, msg *gatewayv1.PushMessage) error

	// GetRecentMessages 获取会话的最近消息
	GetRecentMessages(ctx context.Context, sessionID string, limit int64, beforeSeq int64) ([]*gatewayv1.PushMessage, error)

	// GetMessageByID 根据消息 ID 获取消息
	GetMessageByID(ctx context.Context, msgID int64) (*gatewayv1.PushMessage, error)

	// GetLastMessage 获取会话的最后一条消息
	GetLastMessage(ctx context.Context, sessionID string) (*gatewayv1.PushMessage, error)

	// GetNextSeqID 获取会话的下一个序列号
	GetNextSeqID(ctx context.Context, sessionID string) (int64, error)
}

