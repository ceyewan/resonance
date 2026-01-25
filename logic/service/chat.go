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
	"github.com/ceyewan/resonance/internal/model"
	"github.com/ceyewan/resonance/internal/repo"
	"google.golang.org/protobuf/proto"
)

// ChatService 聊天服务
type ChatService struct {
	logicv1.UnimplementedChatServiceServer
	sessionRepo repo.SessionRepo
	messageRepo repo.MessageRepo
	idGen       idgen.Generator // Snowflake ID 生成器
	sequencer   idgen.Sequencer
	mqClient    mq.Client
	logger      clog.Logger
}

// NewChatService 创建聊天服务
func NewChatService(
	sessionRepo repo.SessionRepo,
	messageRepo repo.MessageRepo,
	idGen idgen.Generator,
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

// SendMessage 实现 ChatService.SendMessage（Unary 调用）
func (s *ChatService) SendMessage(ctx context.Context, req *logicv1.SendMessageRequest) (*logicv1.SendMessageResponse, error) {
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

	// 准备 MQ 事件
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
	topic := string(proto.GetExtension(event.ProtoReflect().Descriptor().Options(), commonv1.E_DefaultTopic).(string))

	// 准备本地消息表记录
	outbox := &model.MessageOutbox{
		MsgID:         msgID,
		Topic:         topic,
		Payload:       eventData,
		Status:        model.OutboxStatusPending,
		NextRetryTime: time.Now(),
	}

	// 使用事务保存消息、更新序列号、记录 Outbox
	if err := s.messageRepo.SaveMessageWithOutbox(ctx, msgContent, outbox); err != nil {
		s.logger.Error("failed to save message with outbox", clog.Error(err))
		return &logicv1.SendMessageResponse{
			MsgId: msgID,
			SeqId: seqID,
			Error: "failed to save message",
		}, nil
	}

	// 立即尝试发布到 MQ (Look-aside 优化)
	// 显式传递参数，避免闭包捕获的不确定性
	go func(outboxID int64, topic string, data []byte) {
		// 注意：这里使用 context.Background() 或是单独的超时 context，因为当前 ctx 可能会随着 RPC 结束而取消
		publishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := s.mqClient.Publish(publishCtx, topic, data); err != nil {
			s.logger.Warn("immediate publish failed, will be retried by job",
				clog.Int64("msg_id", msgID),
				clog.Error(err))
			return
		}

		// 发布成功，标记为已发送
		if err := s.messageRepo.UpdateOutboxStatus(context.Background(), outboxID, model.OutboxStatusSent); err != nil {
			s.logger.Error("failed to update outbox status after publish",
				clog.Int64("msg_id", msgID),
				clog.Error(err))
		}
	}(outbox.ID, topic, eventData)

	s.logger.Info("message processed successfully",
		clog.Int64("msg_id", msgID),
		clog.Int64("seq_id", seqID))

	return &logicv1.SendMessageResponse{
		MsgId: msgID,
		SeqId: seqID,
		Error: "",
	}, nil
}
