package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
)

func TestSessionService_GetHistoryEvents_DeniedForNonMember(t *testing.T) {
	sessionRepo := &testSessionRepo{
		getUserSessionFn: func(ctx context.Context, username, sessionID string) (*model.SessionMember, error) {
			return nil, fmt.Errorf("%w: username=%s, session_id=%s", repo.ErrSessionMemberNotFound, username, sessionID)
		},
	}
	messageRepo := &testMessageRepo{}

	svc := NewSessionService(sessionRepo, messageRepo, &testUserRepo{}, nil, nil, nil, nil, testLogger())

	ctx := newTestIncomingContext("mallory")
	_, err := svc.GetHistoryEvents(ctx, &logicv1.GetHistoryEventsRequest{
		SessionId: "s_123",
		Limit:     20,
	})
	require.Error(t, err)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.False(t, messageRepo.historyCalled, "越权时不应触发历史消息查询")
}
