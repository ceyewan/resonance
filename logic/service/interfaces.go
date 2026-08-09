package service

import (
	"context"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
)

// AuthServiceInterface 认证服务接口
// 用于提高可测试性和可维护性，允许 mock 实现
type AuthServiceInterface interface {
	Login(ctx context.Context, req *logicv1.LoginRequest) (*logicv1.LoginResponse, error)
	Register(ctx context.Context, req *logicv1.RegisterRequest) (*logicv1.RegisterResponse, error)
	ValidateToken(ctx context.Context, req *logicv1.ValidateTokenRequest) (*logicv1.ValidateTokenResponse, error)
}

// SessionServiceInterface 会话服务接口
// 用于提高可测试性和可维护性，允许 mock 实现
type SessionServiceInterface interface {
	GetSessionList(ctx context.Context, req *logicv1.GetSessionListRequest) (*logicv1.GetSessionListResponse, error)
	CreateSession(ctx context.Context, req *logicv1.CreateSessionRequest) (*logicv1.CreateSessionResponse, error)
	CreateAgentSession(ctx context.Context, req *logicv1.CreateAgentSessionRequest) (*logicv1.CreateAgentSessionResponse, error)
	GetHistoryEvents(ctx context.Context, req *logicv1.GetHistoryEventsRequest) (*logicv1.GetHistoryEventsResponse, error)
	GetContactList(ctx context.Context, req *logicv1.GetContactListRequest) (*logicv1.GetContactListResponse, error)
	SearchUser(ctx context.Context, req *logicv1.SearchUserRequest) (*logicv1.SearchUserResponse, error)
	PullInboxDelta(ctx context.Context, req *logicv1.PullInboxDeltaRequest) (*logicv1.PullInboxDeltaResponse, error)
}

// ChatServiceInterface 聊天服务接口
// 用于提高可测试性和可维护性，允许 mock 实现
type ChatServiceInterface interface {
	SendEvent(ctx context.Context, req *logicv1.SendEventRequest) (*logicv1.SendEventResponse, error)
}

// PresenceServiceInterface 在线状态服务接口
// 用于提高可测试性和可维护性，允许 mock 实现
type PresenceServiceInterface interface {
	SyncStatus(ctx context.Context, req *logicv1.SyncStatusRequest) (*logicv1.SyncStatusResponse, error)
}

// AgentApprovalServiceInterface 审批事实服务接口；该接口不执行 Tool。
type AgentApprovalServiceInterface interface {
	CreateApproval(ctx context.Context, req *logicv1.CreateApprovalRequest) (*logicv1.CreateApprovalResponse, error)
	DecideApproval(ctx context.Context, req *logicv1.DecideApprovalRequest) (*logicv1.DecideApprovalResponse, error)
	GetApproval(ctx context.Context, req *logicv1.GetApprovalRequest) (*logicv1.GetApprovalResponse, error)
	ListApprovals(ctx context.Context, req *logicv1.ListApprovalsRequest) (*logicv1.ListApprovalsResponse, error)
}

// 确保实现了接口
var (
	_ AuthServiceInterface          = (*AuthService)(nil)
	_ SessionServiceInterface       = (*SessionService)(nil)
	_ ChatServiceInterface          = (*ChatService)(nil)
	_ PresenceServiceInterface      = (*PresenceService)(nil)
	_ AgentApprovalServiceInterface = (*AgentApprovalService)(nil)
)
