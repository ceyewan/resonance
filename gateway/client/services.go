package client

import (
	"context"
	"fmt"

	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
)

// ==================== AuthService 接口 ====================

// Login 调用 Logic 的登录接口
func (c *Client) Login(ctx context.Context, req *logicv1.LoginRequest) (*logicv1.LoginResponse, error) {
	return c.authSvc().Login(ctx, req)
}

// Register 调用 Logic 的注册接口
func (c *Client) Register(ctx context.Context, req *logicv1.RegisterRequest) (*logicv1.RegisterResponse, error) {
	return c.authSvc().Register(ctx, req)
}

// ValidateToken 验证 Token
func (c *Client) ValidateToken(ctx context.Context, token string) (*logicv1.ValidateTokenResponse, error) {
	return c.authSvc().ValidateToken(ctx, &logicv1.ValidateTokenRequest{
		AccessToken: token,
	})
}

// ==================== ChatService 接口 ====================

// SendMessage 发送消息到 Logic（Unary 调用）
func (c *Client) SendMessage(ctx context.Context, msg *gatewayv1.ChatRequest) (*logicv1.SendMessageResponse, error) {
	if c.chatClient == nil {
		return nil, fmt.Errorf("chat client not initialized")
	}

	req := &logicv1.SendMessageRequest{
		SessionId:    msg.SessionId,
		FromUsername: msg.FromUsername,
		ToUsername:   msg.ToUsername,
		Content:      msg.Content,
		Type:         msg.Type,
		Timestamp:    msg.Timestamp,
	}

	return c.chatClient.SendMessage(ctx, req)
}

// ==================== SessionService 接口 ====================

// GetSessionList 获取会话列表
func (c *Client) GetSessionList(ctx context.Context, username string) (*logicv1.GetSessionListResponse, error) {
	return c.sessionSvc().GetSessionList(ctx, &logicv1.GetSessionListRequest{
		Username: username,
	})
}

// CreateSession 创建会话
func (c *Client) CreateSession(ctx context.Context, req *logicv1.CreateSessionRequest) (*logicv1.CreateSessionResponse, error) {
	return c.sessionSvc().CreateSession(ctx, req)
}

// GetRecentMessages 获取历史消息
func (c *Client) GetRecentMessages(ctx context.Context, req *logicv1.GetRecentMessagesRequest) (*logicv1.GetRecentMessagesResponse, error) {
	return c.sessionSvc().GetRecentMessages(ctx, req)
}

// GetContactList 获取联系人列表
func (c *Client) GetContactList(ctx context.Context, username string) (*logicv1.GetContactListResponse, error) {
	return c.sessionSvc().GetContactList(ctx, &logicv1.GetContactListRequest{
		Username: username,
	})
}

// SearchUser 搜索用户
func (c *Client) SearchUser(ctx context.Context, query string) (*logicv1.SearchUserResponse, error) {
	return c.sessionSvc().SearchUser(ctx, &logicv1.SearchUserRequest{
		Query: query,
	})
}

// UpdateReadPosition 更新会话已读位置
func (c *Client) UpdateReadPosition(ctx context.Context, req *logicv1.UpdateReadPositionRequest) (*logicv1.UpdateReadPositionResponse, error) {
	return c.sessionSvc().UpdateReadPosition(ctx, req)
}

// ==================== PresenceService 接口 ====================

// SyncUserOnline 同步用户上线到 Logic（通过 StatusBatcher 批量处理）
func (c *Client) SyncUserOnline(ctx context.Context, username string, remoteIP string) error {
	if c.statusBatcher == nil {
		return fmt.Errorf("status batcher not initialized")
	}
	c.statusBatcher.SyncUserOnline(username, remoteIP)
	return nil
}

// SyncUserOffline 同步用户下线到 Logic（通过 StatusBatcher 批量处理）
func (c *Client) SyncUserOffline(ctx context.Context, username string) error {
	if c.statusBatcher == nil {
		return fmt.Errorf("status batcher not initialized")
	}
	c.statusBatcher.SyncUserOffline(username)
	return nil
}

// IsUserOnline 检查用户是否在线（通过 SessionService 查询）
func (c *Client) IsUserOnline(ctx context.Context, username string) (bool, string, error) {
	if c.sessionClient == nil {
		return false, "", fmt.Errorf("session client not initialized")
	}

	// 使用 SessionService.GetUserSession 查询用户在线状态
	// 这里需要根据实际 API 调整
	return false, "", nil
}
