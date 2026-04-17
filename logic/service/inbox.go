package service

import (
	"context"

	"github.com/ceyewan/genesis/clog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
)

// PullInboxDelta 实现 SessionService.PullInboxDelta
func (s *SessionService) PullInboxDelta(ctx context.Context, req *logicv1.PullInboxDeltaRequest) (*logicv1.PullInboxDeltaResponse, error) {
	username, err := MustUsernameFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	limit := int(req.Limit)
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	items, err := s.messageRepo.GetInboxDelta(ctx, username, req.CursorId, limit)
	if err != nil {
		s.logger.Error("failed to get inbox delta", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to get inbox delta")
	}

	events := make([]*commonv1.InboxEvent, 0, len(items))
	nextCursorID := req.CursorId
	for _, item := range items {
		chatEvent := &commonv1.ChatEvent{}
		if err := proto.Unmarshal(item.Payload, chatEvent); err != nil {
			s.logger.Error("failed to unmarshal inbox payload", clog.Int64("inbox_id", item.ID), clog.Error(err))
			return nil, status.Errorf(codes.Internal, "failed to decode inbox payload")
		}
		events = append(events, &commonv1.InboxEvent{
			InboxId: item.ID,
			Event:   chatEvent,
		})
		if item.ID > nextCursorID {
			nextCursorID = item.ID
		}
	}

	return &logicv1.PullInboxDeltaResponse{
		Events:       events,
		NextCursorId: nextCursorID,
		HasMore:      len(items) == limit,
	}, nil
}
