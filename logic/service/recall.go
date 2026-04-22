package service

import (
	"context"
	"errors"
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

const recallTimeWindow = 2 * time.Minute

func (s *ChatService) handleRecall(ctx context.Context, req *logicv1.SendEventRequest, username string, targetUsernames []string) (*logicv1.SendEventResponse, error) {
	payload := req.GetRecall()

	// 1. 查原消息
	msg, err := s.messageRepo.GetMessageByEventID(ctx, payload.TargetEventId)
	if err != nil {
		if errors.Is(err, repo.ErrMessageNotFound) {
			return nil, status.Errorf(codes.NotFound, "message not found")
		}
		s.logger.Error("failed to get message for recall", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to get message")
	}

	// 2. 只能撤回自己发的消息
	if msg.SenderUsername != username {
		return nil, status.Errorf(codes.PermissionDenied, "can only recall your own messages")
	}

	// 3. 消息必须属于当前会话
	if msg.SessionID != req.SessionId {
		return nil, status.Errorf(codes.PermissionDenied, "message does not belong to this session")
	}

	// 4. 已撤回
	if msg.RecalledAt != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "message already recalled")
	}

	// 5. 时间窗口（2 分钟内）
	if time.Since(msg.CreatedAt) > recallTimeWindow {
		return nil, status.Errorf(codes.FailedPrecondition, "recall time window expired")
	}

	// 6. 生成 event_id / seq_id
	eventID, err := s.idGen.Next()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to generate event id: %v", err)
	}
	// 与 handleMessage 相同的 seq 初始化：防止服务重启后 Redis key 丢失导致 seq 从 1 开始
	session, err := s.sessionRepo.GetSession(ctx, req.SessionId)
	if err == nil && session.MaxSeqID > 0 {
		if _, err := s.sequencer.SetIfNotExists(ctx, req.SessionId, session.MaxSeqID); err != nil {
			s.logger.Warn("failed to initialize session sequence for recall",
				clog.String("session_id", req.SessionId),
				clog.Error(err))
		}
	}
	seqID, err := s.sequencer.Next(ctx, req.SessionId)
	if err != nil {
		s.logger.Error("failed to generate seq id for recall", clog.Error(err))
		return nil, status.Errorf(codes.Unavailable, "server busy: failed to generate sequence")
	}
	timestampMs := time.Now().UnixMilli()
	recalledAt := time.UnixMilli(timestampMs)

	// 7. 构造 ChatEvent + MQEvent
	chatEvent := &commonv1.ChatEvent{
		EventId:      eventID,
		SeqId:        seqID,
		SessionId:    req.SessionId,
		FromUsername: username,
		TimestampMs:  timestampMs,
		Payload: &commonv1.ChatEvent_Recall{
			Recall: &commonv1.MessageRecall{TargetEventId: payload.TargetEventId},
		},
	}
	mqEvent := &mqv1.MQEvent{
		Event:           chatEvent,
		TargetUsernames: targetUsernames,
	}

	// 8. 序列化 + 获取 topic
	mqEvent.TraceHeaders = make(map[string]string)
	observability.InjectTraceContext(ctx, mqEvent.TraceHeaders)

	eventData, err := proto.Marshal(mqEvent)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal recall event")
	}
	topic := proto.GetExtension(mqEvent.ProtoReflect().Descriptor().Options(), commonv1.E_DefaultTopic).(string)

	outbox := &model.MessageOutbox{
		EventID:       eventID,
		Topic:         topic,
		Payload:       eventData,
		Status:        model.OutboxStatusPending,
		NextRetryTime: recalledAt,
	}

	// 9. 事务：UPDATE recalled_at + INSERT outbox
	if err := s.messageRepo.RecallMessageWithOutbox(ctx, payload.TargetEventId, recalledAt, outbox); err != nil {
		if errors.Is(err, repo.ErrMessageAlreadyRecalled) {
			return nil, status.Errorf(codes.FailedPrecondition, "message already recalled")
		}
		s.logger.Error("failed to recall message", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to recall message")
	}

	// 10. 异步投递 MQ（失败由 outbox job 补偿）
	mqpublish.PublishMessageToMQAsync(s.mqClient, outbox.ID, topic, eventData, s.logger)

	s.logger.Info("recall processed successfully",
		clog.Int64("target_event_id", payload.TargetEventId),
		clog.Int64("recall_event_id", eventID),
		clog.Int64("seq_id", seqID))

	return &logicv1.SendEventResponse{
		EventId:     eventID,
		SeqId:       seqID,
		TimestampMs: timestampMs,
	}, nil
}
