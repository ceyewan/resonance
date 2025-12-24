package client

import (
	"context"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
)

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
