package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/ceyewan/genesis/clog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/logic/internal/mqpublish"
	"github.com/ceyewan/resonance/logic/observability"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
)

func (s *ChatService) handleEdit(ctx context.Context, req *logicv1.SendEventRequest, username string, targetUsernames []string) (*logicv1.SendEventResponse, error) {
	payload := req.GetEdit()
	newContent := strings.TrimSpace(payload.GetNewContent())
	if payload.GetTargetEventId() == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "target event id is required")
	}
	if newContent == "" {
		return nil, status.Errorf(codes.InvalidArgument, "new content cannot be empty")
	}

	msg, err := s.messageRepo.GetMessageByEventID(ctx, payload.TargetEventId)
	if err != nil {
		if errors.Is(err, repo.ErrMessageNotFound) {
			return nil, status.Errorf(codes.NotFound, "message not found")
		}
		s.logger.Error("failed to get message for edit", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to get message")
	}

	if msg.SenderUsername != username {
		return nil, status.Errorf(codes.PermissionDenied, "can only edit your own messages")
	}
	if msg.SessionID != req.SessionId {
		return nil, status.Errorf(codes.PermissionDenied, "message does not belong to this session")
	}
	if msg.RecalledAt != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "message already recalled")
	}
	if msg.MsgType != int(commonv1.MessageType_MESSAGE_TYPE_TEXT) {
		return nil, status.Errorf(codes.FailedPrecondition, "message type does not support edit")
	}
	if time.Since(msg.CreatedAt) > recallTimeWindow {
		return nil, status.Errorf(codes.FailedPrecondition, "edit time window expired")
	}
	if msg.Content == newContent {
		return nil, status.Errorf(codes.FailedPrecondition, "message content unchanged")
	}

	eventID, err := s.idGen.Next()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to generate event id: %v", err)
	}
	session, err := s.sessionRepo.GetSession(ctx, req.SessionId)
	if err == nil && session.MaxSeqID > 0 {
		if _, err := s.sequencer.SetIfNotExists(ctx, req.SessionId, session.MaxSeqID); err != nil {
			s.logger.Warn("failed to initialize session sequence for edit",
				clog.String("session_id", req.SessionId),
				clog.Error(err))
		}
	}
	seqID, err := s.sequencer.Next(ctx, req.SessionId)
	if err != nil {
		s.logger.Error("failed to generate seq id for edit", clog.Error(err))
		return nil, status.Errorf(codes.Unavailable, "server busy: failed to generate sequence")
	}

	timestampMs := time.Now().UnixMilli()
	editedAt := time.UnixMilli(timestampMs)
	chatEvent := &commonv1.ChatEvent{
		EventId:      eventID,
		SeqId:        seqID,
		SessionId:    req.SessionId,
		FromUsername: username,
		TimestampMs:  timestampMs,
		Payload: &commonv1.ChatEvent_Edit{
			Edit: &commonv1.MessageEdit{
				TargetEventId: payload.TargetEventId,
				NewContent:    newContent,
			},
		},
	}
	mqEvent := &mqv1.MQEvent{
		Event:           chatEvent,
		TargetUsernames: targetUsernames,
	}
	mqEvent.TraceHeaders = make(map[string]string)
	observability.InjectTraceContext(ctx, mqEvent.TraceHeaders)

	eventData, err := proto.Marshal(mqEvent)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal edit event")
	}
	topic := proto.GetExtension(mqEvent.ProtoReflect().Descriptor().Options(), commonv1.E_DefaultTopic).(string)
	outbox := &model.MessageOutbox{
		EventID:       eventID,
		Topic:         topic,
		Payload:       eventData,
		Status:        model.OutboxStatusPending,
		NextRetryTime: editedAt,
	}

	if err := s.messageRepo.EditMessageWithOutbox(ctx, payload.TargetEventId, newContent, editedAt, outbox); err != nil {
		switch {
		case errors.Is(err, repo.ErrMessageNotFound):
			return nil, status.Errorf(codes.NotFound, "message not found")
		case errors.Is(err, repo.ErrMessageAlreadyRecalled):
			return nil, status.Errorf(codes.FailedPrecondition, "message already recalled")
		default:
			s.logger.Error("failed to edit message", clog.Error(err))
			return nil, status.Errorf(codes.Internal, "failed to edit message")
		}
	}

	mqpublish.PublishMessageToMQAsync(s.mqClient, outbox.ID, topic, eventData, s.logger)

	s.logger.Info("edit processed successfully",
		clog.Int64("target_event_id", payload.TargetEventId),
		clog.Int64("edit_event_id", eventID),
		clog.Int64("seq_id", seqID))

	return &logicv1.SendEventResponse{
		EventId:     eventID,
		SeqId:       seqID,
		TimestampMs: timestampMs,
	}, nil
}
