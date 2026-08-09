package service

import (
	"context"
	"errors"

	"github.com/ceyewan/genesis/clog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/pkg/event"
	"github.com/ceyewan/resonance/repo"
)

// GetHistoryEvents 实现 SessionService.GetHistoryEvents
func (s *SessionService) GetHistoryEvents(ctx context.Context, req *logicv1.GetHistoryEventsRequest) (*logicv1.GetHistoryEventsResponse, error) {
	username, err := MustUsernameFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	if req.SessionId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "session_id is required")
	}
	if err := requireSessionTenant(ctx, s.sessionRepo, req.SessionId, s.allowLegacy); err != nil {
		return nil, err
	}

	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	if _, err := s.sessionRepo.GetUserSession(ctx, username, req.SessionId); err != nil {
		if errors.Is(err, repo.ErrSessionMemberNotFound) {
			return nil, status.Errorf(codes.PermissionDenied, "no permission to access session")
		}
		return nil, status.Errorf(codes.Internal, "failed to verify session permission")
	}

	messages, err := s.messageRepo.GetHistoryMessages(ctx, req.SessionId, req.BeforeSeq, limit)
	if err != nil {
		s.logger.Error("failed to get history messages", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to get history messages")
	}

	events := make([]*commonv1.ChatEvent, 0, len(messages))
	for _, msg := range messages {
		events = append(events, event.BuildMessageEventFromModel(req.SessionId, msg))
	}
	return &logicv1.GetHistoryEventsResponse{Events: events}, nil
}
