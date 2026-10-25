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
)

func TestPilotSessionIdentityIsIndependentOfModelConfiguration(t *testing.T) {
	require.Equal(t, generateAgentSessionID("tenant-a", "alice"), generateAgentSessionID("tenant-a", "alice"))
	require.NotEqual(t, generateAgentSessionID("tenant-a", "alice"), generateAgentSessionID("tenant-b", "alice"))
	require.NotEqual(t, generateAgentSessionID("tenant-a", "alice"), generateAgentSessionID("tenant-a", "bob"))
}

func TestSessionService_CreateAgentSessionRequiresAuthenticatedPrincipal(t *testing.T) {
	s := &SessionService{}
	_, err := s.CreateAgentSession(context.Background(), &logicv1.CreateAgentSessionRequest{})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestSessionService_CreateSessionRejectsBotAndThirdPartyAIChat(t *testing.T) {
	users := &testUserRepo{getUserByUsernameFn: func(_ context.Context, name string) (*model.User, error) {
		kind := model.UserKindHuman
		if name == "pilot" {
			kind = model.UserKindAgentBot
		}
		return &model.User{Username: name, Kind: kind}, nil
	}}
	s := NewSessionService(&testSessionRepo{}, &testMessageRepo{}, users, &testGenerator{}, &testGenerator{}, &testSequencer{}, &testMQ{}, testLogger(), WithTenantMembershipReader(&testTenantMembershipReader{}))
	ctx := WithUserPrincipal(context.Background(), &UserPrincipal{TenantID: "tenant-a", Username: "alice", Version: 1, Scopes: []string{model.ScopeChatUse}})
	_, err := s.CreateSession(ctx, &logicv1.CreateSessionRequest{Type: commonv1.SessionType_SESSION_TYPE_DIRECT, Members: []string{"pilot"}})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
