package service

import (
	"context"
	"errors"
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
	allowLegacy bool
}

type ChatServiceOption func(*ChatService)

func WithLegacyGlobalChatAuthorizationForTests() ChatServiceOption {
	return func(service *ChatService) { service.allowLegacy = true }
}

// NewChatService 创建聊天服务
func NewChatService(
	sessionRepo repo.SessionRepo,
	messageRepo repo.MessageRepo,
	idGen idgen.Generator,
	sequencer idgen.Sequencer,
	mqClient mq.MQ,
	logger clog.Logger,
	options ...ChatServiceOption,
) *ChatService {
	service := &ChatService{
		sessionRepo: sessionRepo,
		messageRepo: messageRepo,
		idGen:       idGen,
		sequencer:   sequencer,
		mqClient:    mqClient,
		logger:      logger,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

// SendEvent 实现 ChatService.SendEvent（Unary 调用）
func (s *ChatService) SendEvent(ctx context.Context, req *logicv1.SendEventRequest) (*logicv1.SendEventResponse, error) {
	username, err := MustUsernameFromCtx(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireSessionTenant(ctx, s.sessionRepo, req.SessionId, s.allowLegacy); err != nil {
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
	msgPayload, err := canonicalMessage(req.GetMessage())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	idempotencyHash := ""
	if msgPayload.GetClientMsgId() != "" {
		idempotencyHash, err = messageIdempotencyHash(req.GetSessionId(), username, msgPayload)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to prepare message")
		}

		existing, lookupErr := s.messageRepo.GetMessageByIdempotencyKey(
			ctx, req.GetSessionId(), username, msgPayload.GetClientMsgId(),
		)
		switch {
		case lookupErr == nil:
			if existing.IdempotencyHash == "" || existing.IdempotencyHash != idempotencyHash {
				return nil, status.Error(codes.AlreadyExists, "client_msg_id is already used by another message")
			}
			return messageResponse(existing), nil
		case errors.Is(lookupErr, repo.ErrMessageNotFound):
			// 首次请求，继续进入数据库唯一约束保护的原子写路径。
		default:
			s.logger.Error("failed to check message idempotency", clog.Error(lookupErr))
			return nil, status.Error(codes.Internal, "failed to check message idempotency")
		}
	}

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
	createdAt := time.Now().UTC()
	timestampMs := createdAt.UnixMilli()

	msgContent := &model.MessageContent{
		EventID:         eventID,
		SessionID:       req.SessionId,
		SenderUsername:  username,
		SeqID:           seqID,
		Content:         msgPayload.Content,
		MsgType:         event.FormatMessageType(msgPayload.Type),
		ReplyToEventID:  msgPayload.ReplyToEventId,
		ClientMsgID:     msgPayload.ClientMsgId,
		IdempotencyHash: idempotencyHash,
		CreatedAt:       createdAt,
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
		if errors.Is(err, repo.ErrMessageIdempotencyConflict) {
			return nil, status.Error(codes.AlreadyExists, "client_msg_id is already used by another message")
		}
		if errors.Is(err, repo.ErrAgentFinalMessageNotCommittable) {
			return nil, status.Error(codes.Aborted, "agent result is no longer committable")
		}
		s.logger.Error("failed to publish message to mq", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to save message")
	}

	if result.Created {
		mqpublish.PublishMessageToMQAsync(s.mqClient, result.OutboxID, result.Topic, result.EventData, s.logger)
	}

	s.logger.Info("message processed successfully",
		clog.Int64("event_id", result.Message.EventID),
		clog.Int64("seq_id", result.Message.SeqID))

	return messageResponse(result.Message), nil
}

func messageResponse(message *model.MessageContent) *logicv1.SendEventResponse {
	return &logicv1.SendEventResponse{
		EventId:     message.EventID,
		SeqId:       message.SeqID,
		TimestampMs: message.CreatedAt.UnixMilli(),
	}
}
