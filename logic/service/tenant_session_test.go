package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
)

func TestSessionService_TenantBoundaryFiltersListAndDeniesHistoryRead(t *testing.T) {
	sessionRepo := &testSessionRepo{
		getUserSessionTenantFn: func(_ context.Context, tenantID, username string) ([]*model.Session, error) {
			require.Equal(t, "tenant-b", tenantID)
			require.Equal(t, "alice", username)
			return []*model.Session{
				{SessionID: "tenant-b-session", TenantID: "tenant-b", Type: int(commonv1.SessionType_SESSION_TYPE_GROUP)},
				{SessionID: "tenant-a-session", TenantID: "tenant-a", Type: int(commonv1.SessionType_SESSION_TYPE_GROUP)},
			}, nil
		},
		getSessionFn: func(_ context.Context, sessionID string) (*model.Session, error) {
			return &model.Session{SessionID: sessionID, TenantID: "tenant-a"}, nil
		},
		getUserSessionFn: func(context.Context, string, string) (*model.SessionMember, error) {
			t.Fatal("cross-tenant request must fail before membership lookup")
			return nil, nil
		},
	}
	svc := NewSessionService(sessionRepo, &testMessageRepo{}, &testUserRepo{}, nil, nil, nil, nil, testLogger())
	ctx := WithUserPrincipal(context.Background(), &UserPrincipal{TenantID: "tenant-b", Username: "alice", Version: 1, Scopes: []string{model.ScopeChatUse}})

	list, err := svc.GetSessionList(ctx, &logicv1.GetSessionListRequest{})
	require.NoError(t, err)
	require.Len(t, list.Sessions, 1)
	require.Equal(t, "tenant-b-session", list.Sessions[0].SessionId)

	_, err = svc.GetHistoryEvents(ctx, &logicv1.GetHistoryEventsRequest{SessionId: "tenant-a-session"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = svc.UpdateReadPosition(ctx, &logicv1.UpdateReadPositionRequest{SessionId: "tenant-a-session", SeqId: 1})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestChatService_SameUsernameCannotCrossTenant(t *testing.T) {
	sessionRepo := &testSessionRepo{
		getSessionFn: func(context.Context, string) (*model.Session, error) {
			return &model.Session{SessionID: "tenant-a-session", TenantID: "tenant-a"}, nil
		},
		getMembersFn: func(context.Context, string) ([]*model.SessionMember, error) {
			t.Fatal("cross-tenant request must fail before membership lookup")
			return nil, nil
		},
	}
	svc := NewChatService(sessionRepo, &testMessageRepo{}, &testGenerator{next: 1}, &testSequencer{}, &testMQ{}, testLogger())
	ctx := WithUserPrincipal(context.Background(), &UserPrincipal{TenantID: "tenant-b", Username: "alice", Version: 1})
	_, err := svc.SendEvent(ctx, &logicv1.SendEventRequest{
		SessionId: "tenant-a-session",
		Payload: &logicv1.SendEventRequest_Message{Message: &commonv1.Message{
			Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: "must not cross",
		}},
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestSessionService_CreateSessionRequiresTargetTenantMembership(t *testing.T) {
	memberships := &testTenantMembershipReader{getFn: func(_ context.Context, tenantID, username string) (*model.TenantMembership, error) {
		if tenantID == "tenant-a" && username == "alice" {
			return &model.TenantMembership{TenantID: tenantID, Username: username, Status: model.TenantMembershipStatusActive, Version: 1}, nil
		}
		return nil, repo.ErrTenantMembershipNotFound
	}}
	users := &testUserRepo{getUserByUsernameFn: func(_ context.Context, username string) (*model.User, error) {
		return &model.User{Username: username, Kind: model.UserKindHuman}, nil
	}}
	sessions := &testSessionRepo{}
	svc := NewSessionService(sessions, &testMessageRepo{}, users, &testGenerator{next: 1}, nil, nil, nil, testLogger(), WithTenantMembershipReader(memberships))
	ctx := WithUserPrincipal(context.Background(), &UserPrincipal{TenantID: "tenant-a", Username: "alice", Version: 1, Scopes: []string{model.ScopeChatUse}})

	_, err := svc.CreateSession(ctx, &logicv1.CreateSessionRequest{
		Type: commonv1.SessionType_SESSION_TYPE_DIRECT, Members: []string{"bob"},
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Nil(t, sessions.createdSession)
}

func TestTenantDerivedSessionIDsSeparateTenantAndProfileVersion(t *testing.T) {
	require.NotEqual(t,
		generateDirectSessionID("tenant-a", "alice", "bob"),
		generateDirectSessionID("tenant-b", "alice", "bob"),
	)
	require.NotEqual(t,
		generateAgentSessionID("tenant-a", "alice", model.AgentProfileUserAssistant, 1),
		generateAgentSessionID("tenant-a", "alice", model.AgentProfileUserAssistant, 2),
	)
	require.LessOrEqual(t, len(generateAgentSessionID("tenant-a", "alice", model.AgentProfileUserAssistant, 1)), 64)
}

func TestRequireSessionTenant_BindsServiceWorkloadToExactAIProfileVersion(t *testing.T) {
	sessions := &testSessionRepo{getSessionFn: func(_ context.Context, sessionID string) (*model.Session, error) {
		switch sessionID {
		case "assistant-v1":
			return &model.Session{
				SessionID: sessionID, TenantID: "tenant-a", Kind: model.SessionKindAI,
				ProfileID: model.AgentProfileUserAssistant, ProfileVersion: 1,
			}, nil
		case "admin-v1":
			return &model.Session{
				SessionID: sessionID, TenantID: "tenant-a", Kind: model.SessionKindAI,
				ProfileID: model.AgentProfileIAMAdmin, ProfileVersion: 1,
			}, nil
		default:
			return &model.Session{SessionID: sessionID, TenantID: "tenant-a", Kind: model.SessionKindStandard}, nil
		}
	}}
	assistant := WithProfiledServicePrincipal(
		context.Background(), "pilot-user-service", "tenant-a", "resonance-agent",
		model.AgentProfileUserAssistant, 1,
	)
	err := requireSessionTenant(assistant, sessions, "assistant-v1")
	require.NoError(t, err)
	err = requireSessionTenant(assistant, sessions, "admin-v1")
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	err = requireSessionTenant(assistant, sessions, "ordinary")
	require.Equal(t, codes.PermissionDenied, status.Code(err))

	wrongVersion := WithProfiledServicePrincipal(
		context.Background(), "pilot-user-v2", "tenant-a", "resonance-agent",
		model.AgentProfileUserAssistant, 2,
	)
	err = requireSessionTenant(wrongVersion, sessions, "assistant-v1")
	require.Equal(t, codes.PermissionDenied, status.Code(err))

	unprofiled := WithServicePrincipal(context.Background(), "pilot-user-service", "tenant-a", "resonance-agent")
	err = requireSessionTenant(unprofiled, sessions, "assistant-v1")
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}
