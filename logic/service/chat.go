package service

import (
	"context"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/mq"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/logic/internal/mqpublish"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/pkg/event"
	"github.com/ceyewan/resonance/repo"
)

// ChatService 聊天服务
type ChatService struct {
	logicv1.UnimplementedChatServiceServer
	sessionRepo repo.SessionRepo
	messageRepo repo.MessageRepo
	idGen       idgen.Generator // Snowflake ID 生成器
	sequencer   idgen.Sequencer
	mqClient    mq.MQ
	logger      clog.Logger
}

// NewChatService 创建聊天服务
func NewChatService(
	sessionRepo repo.SessionRepo,
	messageRepo repo.MessageRepo,
	idGen idgen.Generator,
	sequencer idgen.Sequencer,
	mqClient mq.MQ,
	logger clog.Logger,
) *ChatService {
	return &ChatService{
		sessionRepo: sessionRepo,
		messageRepo: messageRepo,
		idGen:       idGen,
		sequencer:   sequencer,
		mqClient:    mqClient,
		logger:      logger,
	}
}

// SendEvent 实现 ChatService.SendEvent（Unary 调用）
func (s *ChatService) SendEvent(ctx context.Context, req *logicv1.SendEventRequest) (*logicv1.SendEventResponse, error) {
	username, err := MustUsernameFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	members, err := s.sessionRepo.GetMembers(ctx, req.SessionId)
	if err != nil {
		s.logger.Error("failed to get session members", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to get session members")
	}

	isMember := false
	targetUsernames := make([]string, 0, len(members))
	for _, m := range members {
		targetUsernames = append(targetUsernames, m.Username)
		if m.Username == username {
			isMember = true
		}
	}
	if !isMember {
		s.logger.Warn("user is not session member",
			clog.String("username", username),
			clog.String("session_id", req.SessionId))
		return nil, status.Errorf(codes.PermissionDenied, "not a session member")
	}

	switch {
	case req.GetMessage() != nil:
		return s.handleMessage(ctx, req, username, targetUsernames)
	case req.GetRecall() != nil:
		return s.handleRecall(ctx, req, username, targetUsernames)
	case req.GetEdit() != nil:
		return s.handleEdit(ctx, req, username, targetUsernames)
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported payload")
	}
}

func (s *ChatService) handleMessage(ctx context.Context, req *logicv1.SendEventRequest, username string, targetUsernames []string) (*logicv1.SendEventResponse, error) {
	msgPayload := req.GetMessage()

	s.logger.Debug("handling message",
		clog.String("from", username),
		clog.String("session_id", req.SessionId))

	eventID, err := s.idGen.Next()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to generate event id: %v", err)
	}

	// Redis 计数器初始化：服务重启后 Redis key 可能为空，但 DB 里有历史 max_seq_id，
	// 直接递增会从 1 开始导致 seq_id 冲突，所以先用 SetIfNotExists 恢复锚点。
	session, err := s.sessionRepo.GetSession(ctx, req.SessionId)
	if err == nil && session.MaxSeqID > 0 {
		if _, err := s.sequencer.SetIfNotExists(ctx, req.SessionId, session.MaxSeqID); err != nil {
			s.logger.Warn("failed to initialize session sequence",
				clog.String("session_id", req.SessionId),
				clog.Int64("max_seq_id", session.MaxSeqID),
				clog.Error(err))
		}
	}

	seqID, err := s.sequencer.Next(ctx, req.SessionId)
	if err != nil {
		s.logger.Error("failed to generate seq id", clog.Error(err), clog.String("session_id", req.SessionId))
		return nil, status.Errorf(codes.Unavailable, "server busy: failed to generate sequence")
	}
	timestampMs := time.Now().UnixMilli()

	msgContent := &model.MessageContent{
		EventID:        eventID,
		SessionID:      req.SessionId,
		SenderUsername: username,
		SeqID:          seqID,
		Content:        msgPayload.Content,
		MsgType:        event.FormatMessageType(msgPayload.Type),
		ReplyToEventID: msgPayload.ReplyToEventId,
		ClientMsgID:    msgPayload.ClientMsgId,
	}

	chatEvent := &commonv1.ChatEvent{
		EventId:      eventID,
		SeqId:        seqID,
		SessionId:    req.SessionId,
		FromUsername: username,
		TimestampMs:  timestampMs,
		Payload:      &commonv1.ChatEvent_Message{Message: msgPayload},
	}
	mqEvent := &mqv1.MQEvent{
		Event:           chatEvent,
		TargetUsernames: targetUsernames,
	}

	result, err := mqpublish.PublishMessageToMQ(ctx, s.messageRepo, mqEvent, msgContent)
	if err != nil {
		s.logger.Error("failed to publish message to mq", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to save message")
	}

	mqpublish.PublishMessageToMQAsync(s.mqClient, result.OutboxID, result.Topic, result.EventData, s.logger)

	s.logger.Info("message processed successfully",
		clog.Int64("event_id", eventID),
		clog.Int64("seq_id", seqID))

	return &logicv1.SendEventResponse{
		EventId:     eventID,
		SeqId:       seqID,
		TimestampMs: timestampMs,
	}, nil
}
