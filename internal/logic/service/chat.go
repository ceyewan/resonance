package service

import (
	"context"
	"io"

	"connectrpc.com/connect"
	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/mq"
	gatewayv1 "github.com/ceyewan/resonance/im-api/gen/go/gateway/v1"
	logicv1 "github.com/ceyewan/resonance/im-api/gen/go/logic/v1"
	mqv1 "github.com/ceyewan/resonance/im-api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/im-sdk/repo"
)

// ChatService 聊天服务
type ChatService struct {
	sessionRepo repo.SessionRepository
	messageRepo repo.MessageRepository
	idGen       idgen.IDGenerator
	mqPublisher mq.Publisher
	logger      clog.Logger
}

// NewChatService 创建聊天服务
func NewChatService(
	sessionRepo repo.SessionRepository,
	messageRepo repo.MessageRepository,
	idGen idgen.IDGenerator,
	mqPublisher mq.Publisher,
	logger clog.Logger,
) *ChatService {
	return &ChatService{
		sessionRepo: sessionRepo,
		messageRepo: messageRepo,
		idGen:       idGen,
		mqPublisher: mqPublisher,
		logger:      logger,
	}
}

// SendMessage 实现 ChatService.SendMessage（双向流）
func (s *ChatService) SendMessage(
	ctx context.Context,
	stream *connect.BidiStream[logicv1.SendMessageRequest, logicv1.SendMessageResponse],
) error {
	s.logger.Info("chat stream established")

	for {
		req, err := stream.Receive()
		if err != nil {
			if err == io.EOF {
				s.logger.Info("chat stream closed by client")
				return nil
			}
			s.logger.Error("failed to receive message", clog.Error(err))
			return err
		}

		// 处理消息
		resp := s.handleMessage(ctx, req)

		// 发送响应
		if err := stream.Send(resp); err != nil {
			s.logger.Error("failed to send response", clog.Error(err))
			return err
		}
	}
}

// handleMessage 处理单条消息
func (s *ChatService) handleMessage(ctx context.Context, req *logicv1.SendMessageRequest) *logicv1.SendMessageResponse {
	s.logger.Debug("handling message",
		clog.String("from", req.FromUsername),
		clog.String("session_id", req.SessionId))

	// 验证会话成员权限
	isMember, err := s.sessionRepo.IsSessionMember(ctx, req.SessionId, req.FromUsername)
	if err != nil {
		s.logger.Error("failed to check session member", clog.Error(err))
		return &logicv1.SendMessageResponse{
			Error: "failed to check session member",
		}
	}

	if !isMember {
		s.logger.Warn("user is not session member",
			clog.String("username", req.FromUsername),
			clog.String("session_id", req.SessionId))
		return &logicv1.SendMessageResponse{
			Error: "not a session member",
		}
	}

	// 生成消息 ID (Snowflake)
	msgID := s.idGen.NextID()

	// 获取会话的下一个序列号
	seqID, err := s.messageRepo.GetNextSeqID(ctx, req.SessionId)
	if err != nil {
		s.logger.Error("failed to get next seq id", clog.Error(err))
		return &logicv1.SendMessageResponse{
			Error: "failed to generate seq id",
		}
	}

	// 构造推送消息
	pushMsg := &gatewayv1.PushMessage{
		MsgId:        msgID,
		SeqId:        seqID,
		SessionId:    req.SessionId,
		FromUsername: req.FromUsername,
		ToUsername:   req.ToUsername,
		Content:      req.Content,
		Type:         req.Type,
		Timestamp:    req.Timestamp,
	}

	// 保存消息到数据库
	if err := s.messageRepo.SaveMessage(ctx, pushMsg); err != nil {
		s.logger.Error("failed to save message", clog.Error(err))
		return &logicv1.SendMessageResponse{
			MsgId: msgID,
			SeqId: seqID,
			Error: "failed to save message",
		}
	}

	// 发布到 MQ (转发给 Task 服务处理)
	event := &mqv1.PushEvent{
		MsgId:        pushMsg.MsgId,
		SeqId:        pushMsg.SeqId,
		SessionId:    pushMsg.SessionId,
		FromUsername: pushMsg.FromUsername,
		ToUsername:   pushMsg.ToUsername,
		Content:      pushMsg.Content,
		Type:         pushMsg.Type,
		Timestamp:    pushMsg.Timestamp,
	}

	if err := s.mqPublisher.Publish(ctx, event); err != nil {
		s.logger.Error("failed to publish to mq", clog.Error(err))
		return &logicv1.SendMessageResponse{
			MsgId: msgID,
			SeqId: seqID,
			Error: "failed to publish message",
		}
	}

	s.logger.Info("message processed successfully",
		clog.Int64("msg_id", msgID),
		clog.Int64("seq_id", seqID))

	return &logicv1.SendMessageResponse{
		MsgId: msgID,
		SeqId: seqID,
		Error: "",
	}
}

