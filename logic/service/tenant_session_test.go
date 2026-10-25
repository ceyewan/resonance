package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ceyewan/resonance/model"
)

func TestPilotCannotAccessAnotherTenantOrOrdinaryChat(t *testing.T) {
	sessions := &testSessionRepo{getSessionFn: func(_ context.Context, id string) (*model.Session, error) {
		kind := model.SessionKindAI
		if id == "ordinary" {
			kind = model.SessionKindStandard
		}
		return &model.Session{SessionID: id, TenantID: "tenant-a", Type: 1, Kind: kind, OwnerUsername: "alice"}, nil
	}, getMembersFn: func(context.Context, string) ([]*model.SessionMember, error) {
		return []*model.SessionMember{{Username: "alice"}, {Username: "pilot"}}, nil
	}}
	ctx := WithServicePrincipal(context.Background(), "pilot-service", "tenant-a", "pilot")
	require.NoError(t, requireSessionTenant(ctx, sessions, "ai"))
	require.Equal(t, codes.PermissionDenied, status.Code(requireSessionTenant(ctx, sessions, "ordinary")))
	other := WithServicePrincipal(context.Background(), "pilot-service", "tenant-b", "pilot")
	require.Equal(t, codes.PermissionDenied, status.Code(requireSessionTenant(other, sessions, "ai")))
	sessions.getMembersFn = func(context.Context, string) ([]*model.SessionMember, error) {
		return []*model.SessionMember{{Username: "alice"}, {Username: "pilot"}, {Username: "bob"}}, nil
	}
	require.Equal(t, codes.PermissionDenied, status.Code(requireSessionTenant(ctx, sessions, "ai")))
}

func TestPilotPrivateChatIDsAreIsolated(t *testing.T) {
	require.NotEqual(t, generateAgentSessionID("tenant-a", "alice"), generateAgentSessionID("tenant-b", "alice"))
	require.NotEqual(t, generateAgentSessionID("tenant-a", "alice"), generateAgentSessionID("tenant-a", "bob"))
	require.LessOrEqual(t, len(generateAgentSessionID("tenant-a", "alice")), 64)
}
