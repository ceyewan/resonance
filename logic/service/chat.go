package service

import (
	"context"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/mq"
	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	username, err := usernameFromContext(ctx)
	if err != nil {
		return nil, err
	}

	s.logger.Debug("handling message",
		clog.String("from", username),
		clog.String("session_id", req.SessionId))

	// 获取会话成员
	members, err := s.sessionRepo.GetMembers(ctx, req.SessionId)
	if err != nil {
		s.logger.Error("failed to get session members", clog.Error(err))
		return nil, err
	}

	// 检查发送者是否在会话中
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

	// 当前阶段仅落地 message payload
	msgPayload := req.GetMessage()
	if msgPayload == nil {
		return nil, status.Errorf(codes.InvalidArgument, "only message payload is supported in phase 1")
	}

	// 生成 event ID (Snowflake)
	eventID := s.idGen.Next()

	// Redis 计数器初始化
	// 当 Redis 中没有 session 的 seq key 时，sequencer.Next 会从 1 开始
	// 如果该 session 已有历史消息（MaxSeqID > 0），会导致 seq_id 冲突
	// 解决方案：在调用 sequencer.Next 之前，检查 session.MaxSeqID
	// 如果 MaxSeqID > 0 且 Redis key 不存在，使用 sequencer.SetIfNotExists 初始化
	session, err := s.sessionRepo.GetSession(ctx, req.SessionId)
	if err == nil && session.MaxSeqID > 0 {
		// Session 存在且有历史消息，初始化 Redis 计数器（仅当 key 不存在时）
		s.sequencer.SetIfNotExists(ctx, req.SessionId, session.MaxSeqID)
	}

	// 使用 Redis 原子递增获取会话 SeqID，修复并发竞态问题
	seqID, err := s.sequencer.Next(ctx, req.SessionId)
	if err != nil {
		s.logger.Error("failed to generate seq id", clog.Error(err), clog.String("session_id", req.SessionId))
		return nil, status.Errorf(codes.Unavailable, "server busy: failed to generate sequence")
	}
	timestampMs := time.Now().UnixMilli()

	// 保存消息到数据库
	msgContent := &model.MessageContent{
		MsgID:          eventID,
		SessionID:      req.SessionId,
		SenderUsername: username,
		SeqID:          seqID,
		Content:        msgPayload.Content,
		MsgType:        formatMessageType(msgPayload.Type),
	}

	// 准备 MQ 事件
	chatEvent := &commonv1.ChatEvent{
		EventId:      eventID,
		SeqId:        seqID,
		SessionId:    req.SessionId,
		FromUsername: username,
		TimestampMs:  timestampMs,
		Payload: &commonv1.ChatEvent_Message{
			Message: msgPayload,
		},
	}
	event := &mqv1.MQEvent{
		Event:           chatEvent,
		TargetUsernames: targetUsernames,
	}

	// 发布消息到 MQ 并保存到 Outbox
	result, err := PublishMessageToMQ(ctx, s.messageRepo, event, msgContent, s.logger)
	if err != nil {
		s.logger.Error("failed to publish message to mq", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to save message")
	}

	// 立即尝试发布到 MQ (Look-aside 优化)
	PublishMessageToMQAsync(s.mqClient, result.OutboxID, result.Topic, result.EventData, s.logger)

	s.logger.Info("message processed successfully",
		clog.Int64("event_id", eventID),
		clog.Int64("seq_id", seqID))

	return &logicv1.SendEventResponse{
		EventId:     eventID,
		SeqId:       seqID,
		TimestampMs: timestampMs,
	}, nil
}
