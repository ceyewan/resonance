package service

import (
	"context"
	"io"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/mq"
	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/internal/model"
	"github.com/ceyewan/resonance/internal/repo"
	"google.golang.org/protobuf/proto"
)

// ChatService 聊天服务
type ChatService struct {
	logicv1.UnimplementedChatServiceServer
	sessionRepo repo.SessionRepo
	messageRepo repo.MessageRepo
	idGen       *idgen.Snowflake
	sequencer   idgen.Sequencer
	mqClient    mq.Client
	logger      clog.Logger
}

// NewChatService 创建聊天服务
func NewChatService(
	sessionRepo repo.SessionRepo,
	messageRepo repo.MessageRepo,
	idGen *idgen.Snowflake,
	sequencer idgen.Sequencer,
	mqClient mq.Client,
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

// SendMessage 实现 ChatService.SendMessage（双向流）
func (s *ChatService) SendMessage(srv logicv1.ChatService_SendMessageServer) error {
	s.logger.Info("chat stream established")

	for {
		req, err := srv.Recv()
		if err != nil {
			if err == io.EOF {
				s.logger.Info("chat stream closed by client")
				return nil
			}
			s.logger.Error("failed to receive message", clog.Error(err))
			return err
		}

		// 处理消息
		resp, err := s.handleMessage(srv.Context(), req)
		if err != nil {
			s.logger.Error("failed to handle message", clog.Error(err))
			// 尝试发送错误响应
			_ = srv.Send(&logicv1.SendMessageResponse{
				Error: err.Error(),
			})
			continue
		}

		// 发送响应
		if err := srv.Send(resp); err != nil {
			s.logger.Error("failed to send response", clog.Error(err))
			return err
		}
	}
}

// handleMessage 处理单条消息
func (s *ChatService) handleMessage(ctx context.Context, req *logicv1.SendMessageRequest) (*logicv1.SendMessageResponse, error) {
	s.logger.Debug("handling message",
		clog.String("from", req.FromUsername),
		clog.String("session_id", req.SessionId))

	// 获取会话成员
	members, err := s.sessionRepo.GetMembers(ctx, req.SessionId)
	if err != nil {
		s.logger.Error("failed to get session members", clog.Error(err))
		return &logicv1.SendMessageResponse{
			Error: "failed to get session members",
		}, nil
	}

	// 检查发送者是否在会话中
	isMember := false
	for _, m := range members {
		if m.Username == req.FromUsername {
			isMember = true
			break
		}
	}
	if !isMember {
		s.logger.Warn("user is not session member",
			clog.String("username", req.FromUsername),
			clog.String("session_id", req.SessionId))
		return &logicv1.SendMessageResponse{
			Error: "not a session member",
		}, nil
	}

	// 生成消息 ID (Snowflake)
	msgID := s.idGen.Next()

	// TODO: Redis 计数器初始化
	// 当 Redis 中没有 session 的 seq key 时，sequencer.Next 会从 1 开始
	// 如果该 session 已有历史消息（MaxSeqID > 0），会导致 seq_id 冲突
	// 解决方案：在调用 sequencer.Next 之前，检查 session.MaxSeqID
	// 如果 MaxSeqID > 0 且 Redis key 不存在，使用 sequencer.SetIfNotExists 初始化
	// 参考：https://github.com/ceyewan/genesis/issues/xxx （待提交 Feature Request）

	// 使用 Redis 原子递增获取会话 SeqID，修复并发竞态问题
	seqID, err := s.sequencer.Next(ctx, req.SessionId)
	if err != nil {
		s.logger.Error("failed to generate seq id", clog.Error(err), clog.String("session_id", req.SessionId))
		return &logicv1.SendMessageResponse{
			MsgId: msgID,
			Error: "server busy: failed to generate sequence",
		}, nil
	}

	// 保存消息到数据库
	msgContent := &model.MessageContent{
		MsgID:          msgID,
		SessionID:      req.SessionId,
		SenderUsername: req.FromUsername,
		SeqID:          seqID,
		Content:        req.Content,
		MsgType:        req.Type,
	}

	if err := s.messageRepo.SaveMessage(ctx, msgContent); err != nil {
		s.logger.Error("failed to save message", clog.Error(err))
		return &logicv1.SendMessageResponse{
			MsgId: msgID,
			SeqId: seqID,
			Error: "failed to save message",
		}, nil
	}

	// 更新会话的 MaxSeqID
	if err := s.sessionRepo.UpdateMaxSeqID(ctx, req.SessionId, seqID); err != nil {
		s.logger.Error("failed to update max seq id", clog.Error(err))
		// 继续处理，非致命错误
	}

	// 发布到 MQ (转发给 Task 服务处理写扩散)
	event := &mqv1.PushEvent{
		MsgId:        msgID,
		SeqId:        seqID,
		SessionId:    req.SessionId,
		FromUsername: req.FromUsername,
		ToUsername:   req.ToUsername,
		Content:      req.Content,
		Type:         req.Type,
		Timestamp:    req.Timestamp,
	}

	eventData, err := proto.Marshal(event)
	if err != nil {
		s.logger.Error("failed to marshal push event", clog.Error(err))
		return &logicv1.SendMessageResponse{
			MsgId: msgID,
			SeqId: seqID,
			Error: "failed to marshal event",
		}, nil
	}

	// 发布到 MQ
	topic := string(proto.GetExtension(event.ProtoReflect().Descriptor().Options(), commonv1.E_DefaultTopic).(string))
	if err := s.mqClient.Publish(ctx, topic, eventData); err != nil {
		s.logger.Error("failed to publish to mq", clog.Error(err))
		return &logicv1.SendMessageResponse{
			MsgId: msgID,
			SeqId: seqID,
			Error: "failed to publish message",
		}, nil
	}

	s.logger.Info("message processed successfully",
		clog.Int64("msg_id", msgID),
		clog.Int64("seq_id", seqID))

	return &logicv1.SendMessageResponse{
		MsgId: msgID,
		SeqId: seqID,
		Error: "",
	}, nil
}
