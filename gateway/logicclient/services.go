package logicclient

import (
	"context"
	"fmt"

	"google.golang.org/grpc/metadata"

	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
)

const usernameMetadataKey = "x-username"

func withUsernameMetadata(ctx context.Context, username string) context.Context {
	if username == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, usernameMetadataKey, username)
}

// ==================== AuthService 接口 ====================

func (c *Client) Login(ctx context.Context, req *logicv1.LoginRequest) (*logicv1.LoginResponse, error) {
	return c.authSvc().Login(ctx, req)
}

func (c *Client) Register(ctx context.Context, req *logicv1.RegisterRequest) (*logicv1.RegisterResponse, error) {
	return c.authSvc().Register(ctx, req)
}

func (c *Client) ValidateToken(ctx context.Context, token string) (*logicv1.ValidateTokenResponse, error) {
	return c.authSvc().ValidateToken(ctx, &logicv1.ValidateTokenRequest{
		AccessToken: token,
	})
}

// ==================== ChatService 接口 ====================

func (c *Client) SendEvent(ctx context.Context, username string, msg *gatewayv1.ChatRequest) (*logicv1.SendEventResponse, error) {
	if c.chatClient == nil {
		return nil, fmt.Errorf("chat client not initialized")
	}

	req := &logicv1.SendEventRequest{SessionId: msg.SessionId}
	if msg.Recall != nil {
		req.Payload = &logicv1.SendEventRequest_Recall{Recall: msg.Recall}
	} else {
		req.Payload = &logicv1.SendEventRequest_Message{Message: msg.Message}
	}

	return c.chatClient.SendEvent(withUsernameMetadata(ctx, username), req)
}

// ==================== SessionService 接口 ====================

func (c *Client) GetSessionList(ctx context.Context, username string) (*logicv1.GetSessionListResponse, error) {
	return c.sessionSvc().GetSessionList(withUsernameMetadata(ctx, username), &logicv1.GetSessionListRequest{})
}

func (c *Client) CreateSession(ctx context.Context, username string, req *logicv1.CreateSessionRequest) (*logicv1.CreateSessionResponse, error) {
	return c.sessionSvc().CreateSession(withUsernameMetadata(ctx, username), req)
}

func (c *Client) GetHistoryEvents(ctx context.Context, username string, req *logicv1.GetHistoryEventsRequest) (*logicv1.GetHistoryEventsResponse, error) {
	return c.sessionSvc().GetHistoryEvents(withUsernameMetadata(ctx, username), req)
}

func (c *Client) GetContactList(ctx context.Context, username string) (*logicv1.GetContactListResponse, error) {
	return c.sessionSvc().GetContactList(withUsernameMetadata(ctx, username), &logicv1.GetContactListRequest{})
}

func (c *Client) SearchUser(ctx context.Context, username string, query string) (*logicv1.SearchUserResponse, error) {
	return c.sessionSvc().SearchUser(withUsernameMetadata(ctx, username), &logicv1.SearchUserRequest{
		Query: query,
	})
}

func (c *Client) UpdateReadPosition(ctx context.Context, username string, req *logicv1.UpdateReadPositionRequest) (*logicv1.UpdateReadPositionResponse, error) {
	return c.sessionSvc().UpdateReadPosition(withUsernameMetadata(ctx, username), req)
}

func (c *Client) PullInboxDelta(ctx context.Context, username string, req *logicv1.PullInboxDeltaRequest) (*logicv1.PullInboxDeltaResponse, error) {
	return c.sessionSvc().PullInboxDelta(withUsernameMetadata(ctx, username), req)
}

// ==================== PresenceService 接口 ====================

func (c *Client) SyncUserOnline(ctx context.Context, username string, remoteIP string) error {
	if c.statusBatcher == nil {
		return fmt.Errorf("status batcher not initialized")
	}
	c.statusBatcher.SyncUserOnline(username, remoteIP)
	return nil
}

func (c *Client) SyncUserOffline(ctx context.Context, username string) error {
	if c.statusBatcher == nil {
		return fmt.Errorf("status batcher not initialized")
	}
	c.statusBatcher.SyncUserOffline(username)
	return nil
}

func (c *Client) IsUserOnline(ctx context.Context, username string) (bool, string, error) {
	if c.sessionClient == nil {
		return false, "", fmt.Errorf("session client not initialized")
	}
	return false, "", nil
}
