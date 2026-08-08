package logicclient

import (
	"context"
	"fmt"

	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
)

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

func (c *Client) SendEvent(ctx context.Context, _ string, msg *gatewayv1.ChatRequest) (*logicv1.SendEventResponse, error) {
	if c.chatClient == nil {
		return nil, fmt.Errorf("chat client not initialized")
	}

	req := &logicv1.SendEventRequest{SessionId: msg.SessionId}
	switch {
	case msg.Recall != nil:
		req.Payload = &logicv1.SendEventRequest_Recall{Recall: msg.Recall}
	case msg.Edit != nil:
		req.Payload = &logicv1.SendEventRequest_Edit{Edit: msg.Edit}
	default:
		req.Payload = &logicv1.SendEventRequest_Message{Message: msg.Message}
	}

	return c.chatClient.SendEvent(ctx, req)
}

// ==================== SessionService 接口 ====================

func (c *Client) GetSessionList(ctx context.Context, _ string) (*logicv1.GetSessionListResponse, error) {
	return c.sessionSvc().GetSessionList(ctx, &logicv1.GetSessionListRequest{})
}

func (c *Client) CreateSession(ctx context.Context, _ string, req *logicv1.CreateSessionRequest) (*logicv1.CreateSessionResponse, error) {
	return c.sessionSvc().CreateSession(ctx, req)
}

func (c *Client) CreateAgentSession(ctx context.Context, _ string, req *logicv1.CreateAgentSessionRequest) (*logicv1.CreateAgentSessionResponse, error) {
	return c.sessionSvc().CreateAgentSession(ctx, req)
}

func (c *Client) GetHistoryEvents(ctx context.Context, _ string, req *logicv1.GetHistoryEventsRequest) (*logicv1.GetHistoryEventsResponse, error) {
	return c.sessionSvc().GetHistoryEvents(ctx, req)
}

func (c *Client) GetContactList(ctx context.Context, _ string) (*logicv1.GetContactListResponse, error) {
	return c.sessionSvc().GetContactList(ctx, &logicv1.GetContactListRequest{})
}

func (c *Client) SearchUser(ctx context.Context, _ string, query string) (*logicv1.SearchUserResponse, error) {
	return c.sessionSvc().SearchUser(ctx, &logicv1.SearchUserRequest{
		Query: query,
	})
}

func (c *Client) UpdateReadPosition(ctx context.Context, _ string, req *logicv1.UpdateReadPositionRequest) (*logicv1.UpdateReadPositionResponse, error) {
	return c.sessionSvc().UpdateReadPosition(ctx, req)
}

func (c *Client) PullInboxDelta(ctx context.Context, _ string, req *logicv1.PullInboxDeltaRequest) (*logicv1.PullInboxDeltaResponse, error) {
	return c.sessionSvc().PullInboxDelta(ctx, req)
}

// ==================== AgentApprovalService 接口 ====================

func (c *Client) GetAgentApproval(ctx context.Context, req *logicv1.GetApprovalRequest) (*logicv1.GetApprovalResponse, error) {
	return c.approvalSvc().GetApproval(ctx, req)
}

func (c *Client) ListAgentApprovals(ctx context.Context, req *logicv1.ListApprovalsRequest) (*logicv1.ListApprovalsResponse, error) {
	return c.approvalSvc().ListApprovals(ctx, req)
}

func (c *Client) DecideAgentApproval(ctx context.Context, req *logicv1.DecideApprovalRequest) (*logicv1.DecideApprovalResponse, error) {
	return c.approvalSvc().DecideApproval(ctx, req)
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
