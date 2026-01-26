package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/mq"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/internal/model"
	"github.com/ceyewan/resonance/internal/repo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SessionService 会话服务
type SessionService struct {
	logicv1.UnimplementedSessionServiceServer
	sessionRepo  repo.SessionRepo
	messageRepo  repo.MessageRepo
	userRepo     repo.UserRepo
	sessionIDGen idgen.Generator // 用于生成 SessionID
	msgIDGen     idgen.Generator // 用于生成消息 ID
	sequencer    idgen.Sequencer // 用于生成会话 SeqID
	mqClient     mq.Client       // 用于发送系统消息
	logger       clog.Logger
}

// NewSessionService 创建会话服务
func NewSessionService(
	sessionRepo repo.SessionRepo,
	messageRepo repo.MessageRepo,
	userRepo repo.UserRepo,
	sessionIDGen idgen.Generator,
	msgIDGen idgen.Generator,
	sequencer idgen.Sequencer,
	mqClient mq.Client,
	logger clog.Logger,
) *SessionService {
	return &SessionService{
		sessionRepo:  sessionRepo,
		messageRepo:  messageRepo,
		userRepo:     userRepo,
		sessionIDGen: sessionIDGen,
		msgIDGen:     msgIDGen,
		sequencer:    sequencer,
		mqClient:     mqClient,
		logger:       logger,
	}
}

// GetSessionList 实现 SessionService.GetSessionList
func (s *SessionService) GetSessionList(ctx context.Context, req *logicv1.GetSessionListRequest) (*logicv1.GetSessionListResponse, error) {
	s.logger.Info("get session list", clog.String("username", req.Username))

	// 获取用户的所有会话
	sessions, err := s.sessionRepo.GetUserSessionList(ctx, req.Username)
	if err != nil {
		s.logger.Error("failed to get user sessions", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to get user sessions")
	}

	if len(sessions) == 0 {
		return &logicv1.GetSessionListResponse{
			Sessions: []*logicv1.SessionInfo{},
		}, nil
	}

	// 批量查询最后一条消息（避免 N+1 查询）
	sessionIDs := make([]string, len(sessions))
	for i, sess := range sessions {
		sessionIDs[i] = sess.SessionID
	}
	lastMessages, _ := s.messageRepo.GetLastMessagesBatch(ctx, sessionIDs)
	msgMap := make(map[string]*model.MessageContent)
	for _, msg := range lastMessages {
		msgMap[msg.SessionID] = msg
	}

	// 批量查询用户会话信息（避免 N+1 查询）
	userSessions, _ := s.sessionRepo.GetUserSessionsBatch(ctx, req.Username, sessionIDs)
	userSessMap := make(map[string]*model.SessionMember)
	for _, us := range userSessions {
		userSessMap[us.SessionID] = us
	}

	// 提取单聊中的对方用户名，批量查询用户信息
	otherUsernames := make([]string, 0)
	for _, sess := range sessions {
		if sess.Type == 1 && sess.Name == "" {
			parts := strings.Split(sess.SessionID, ":")
			if len(parts) == 3 {
				otherUser := parts[1]
				if otherUser == req.Username {
					otherUser = parts[2]
				}
				otherUsernames = append(otherUsernames, otherUser)
			}
		}
	}
	userMap := make(map[string]*model.User)
	if len(otherUsernames) > 0 {
		users, _ := s.userRepo.GetUsersByUsernames(ctx, otherUsernames)
		for _, u := range users {
			userMap[u.Username] = u
		}
	}

	// 转换为 SessionInfo
	sessionInfos := make([]*logicv1.SessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		// 获取最后一条消息
		lastMsg := &gatewayv1.PushMessage{
			MsgId:        0,
			SeqId:        sess.MaxSeqID,
			SessionId:    sess.SessionID,
			FromUsername: "",
			ToUsername:   "",
			Content:      "",
			Type:         "",
			Timestamp:    0,
		}

		if msg, ok := msgMap[sess.SessionID]; ok {
			lastMsg.SeqId = msg.SeqID
			lastMsg.Content = msg.Content
			lastMsg.Type = msg.MsgType
			lastMsg.Timestamp = msg.CreatedAt.Unix()
			lastMsg.FromUsername = msg.SenderUsername
		}

		// 获取用户会话信息（包含未读数）
		userSess := userSessMap[sess.SessionID]
		unread := int64(0)
		if userSess != nil {
			unread = sess.MaxSeqID - userSess.LastReadSeq
		}

		// 单聊会话名称处理：如果没有设置名称，使用对方用户的昵称
		sessionName := sess.Name
		if sess.Type == 1 && sessionName == "" {
			parts := strings.Split(sess.SessionID, ":")
			if len(parts) == 3 {
				otherUser := parts[1]
				if otherUser == req.Username {
					otherUser = parts[2]
				}
				if user, ok := userMap[otherUser]; ok {
					sessionName = user.Nickname
				}
			}
		}

		lastReadSeq := int64(0)
		if userSess != nil {
			lastReadSeq = userSess.LastReadSeq
		}

		sessionInfos = append(sessionInfos, &logicv1.SessionInfo{
			SessionId:   sess.SessionID,
			Name:        sessionName,
			Type:        int32(sess.Type),
			AvatarUrl:   "",
			UnreadCount: unread,
			LastReadSeq: lastReadSeq,
			LastMessage: lastMsg,
		})
	}

	return &logicv1.GetSessionListResponse{
		Sessions: sessionInfos,
	}, nil
}

// CreateSession 实现 SessionService.CreateSession
func (s *SessionService) CreateSession(ctx context.Context, req *logicv1.CreateSessionRequest) (*logicv1.CreateSessionResponse, error) {
	s.logger.Info("create session",
		clog.String("creator", req.CreatorUsername),
		clog.Int("type", int(req.Type)))

	// 生成 session_id (对于单聊，使用两个用户名排序后的组合)
	sessionID := ""
	if req.Type == 1 {
		// 单聊：使用两个用户名组合
		if len(req.Members) != 1 {
			return nil, status.Errorf(codes.InvalidArgument, "single chat must have exactly one member")
		}
		sessionID = generateSingleChatID(req.CreatorUsername, req.Members[0])
	} else {
		// 群聊：生成 UUID 或使用 ID 生成器
		sessionID = s.generateGroupChatID()
	}

	// 创建会话
	session := &model.Session{
		SessionID:     sessionID,
		Type:          int(req.Type),
		Name:          req.Name,
		OwnerUsername: req.CreatorUsername,
		MaxSeqID:      0,
	}

	if err := s.sessionRepo.CreateSession(ctx, session); err != nil {
		s.logger.Error("failed to create session", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to create session")
	}

	// 添加创建者作为成员
	if err := s.sessionRepo.AddMember(ctx, &model.SessionMember{
		SessionID: sessionID,
		Username:  req.CreatorUsername,
		Role:      1, // 创建者是管理员
	}); err != nil {
		s.logger.Error("failed to add creator to session", clog.Error(err))
	}

	// 添加其他成员
	for _, member := range req.Members {
		if member != req.CreatorUsername {
			if err := s.sessionRepo.AddMember(ctx, &model.SessionMember{
				SessionID: sessionID,
				Username:  member,
				Role:      0, // 普通成员
			}); err != nil {
				s.logger.Error("failed to add member to session", clog.Error(err), clog.String("member", member))
			}
		}
	}

	// 发送系统消息通知所有成员
	if err := s.sendSessionCreatedSystemMessage(ctx, sessionID, req); err != nil {
		s.logger.Error("failed to send system message", clog.Error(err))
		// 系统消息发送失败不影响会话创建
	}

	return &logicv1.CreateSessionResponse{
		SessionId: sessionID,
	}, nil
}

// sendSessionCreatedSystemMessage 发送会话创建的系统消息
func (s *SessionService) sendSessionCreatedSystemMessage(ctx context.Context, sessionID string, req *logicv1.CreateSessionRequest) error {
	// 构建系统消息内容
	content := s.buildSystemMessageContent(ctx, req)
	if content == "" {
		return nil
	}

	// 生成消息 ID 和 SeqID
	msgID := s.msgIDGen.Next()
	seqID, err := s.sequencer.Next(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("generate seq id: %w", err)
	}
	timestamp := time.Now().Unix()

	// 保存消息到数据库
	msgContent := &model.MessageContent{
		MsgID:          msgID,
		SessionID:      sessionID,
		SenderUsername: "system",
		SeqID:          seqID,
		Content:        content,
		MsgType:        "system",
	}

	// 收集所有接收者
	toUsernames := make([]string, 0, len(req.Members))
	for _, member := range req.Members {
		if member != req.CreatorUsername {
			toUsernames = append(toUsernames, member)
		}
	}

	// 准备 MQ 事件
	sessionName := req.Name
	if req.Type == 1 && sessionName == "" {
		if user, err := s.userRepo.GetUserByUsername(ctx, req.CreatorUsername); err == nil {
			sessionName = user.Nickname
		} else {
			sessionName = req.CreatorUsername
		}
	}

	event := &mqv1.PushEvent{
		MsgId:        msgID,
		SeqId:        seqID,
		SessionId:    sessionID,
		FromUsername: "system",
		Content:      content,
		Type:         "system",
		Timestamp:    timestamp,
		SessionName:  sessionName,
		SessionType:  int32(req.Type),
	}

	// 发布消息到 MQ 并保存到 Outbox
	result, err := PublishMessageToMQ(ctx, s.messageRepo, event, msgContent, s.logger)
	if err != nil {
		return fmt.Errorf("publish message to mq: %w", err)
	}

	// 立即尝试发布到 MQ (Look-aside 优化)
	PublishMessageToMQAsync(s.mqClient, result.OutboxID, result.Topic, result.EventData, s.logger)

	s.logger.Info("system message sent",
		clog.Int64("msg_id", msgID),
		clog.Int64("seq_id", seqID),
		clog.String("session_id", sessionID),
		clog.Int("recipients", len(toUsernames)))

	return nil
}

// buildSystemMessageContent 构建系统消息内容
func (s *SessionService) buildSystemMessageContent(ctx context.Context, req *logicv1.CreateSessionRequest) string {
	// 获取创建者昵称
	creatorNickname := req.CreatorUsername
	if user, err := s.userRepo.GetUserByUsername(ctx, req.CreatorUsername); err == nil {
		creatorNickname = user.Nickname
	}

	if req.Type == 1 {
		// 单聊：对方收到 "xxx 开始了与你的对话"
		return fmt.Sprintf("%s 开始了与你的对话", creatorNickname)
	}
	// 群聊：所有人收到 "xxx 创建了群聊「群名」"
	return fmt.Sprintf("%s 创建了群聊「%s」", creatorNickname, req.Name)
}

// GetRecentMessages 实现 SessionService.GetRecentMessages
func (s *SessionService) GetRecentMessages(ctx context.Context, req *logicv1.GetRecentMessagesRequest) (*logicv1.GetRecentMessagesResponse, error) {
	s.logger.Info("get recent messages",
		clog.String("session_id", req.SessionId),
		clog.Int64("limit", req.Limit))

	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	// 获取历史消息
	messages, err := s.messageRepo.GetHistoryMessages(ctx, req.SessionId, req.BeforeSeq, limit)
	if err != nil {
		s.logger.Error("failed to get recent messages", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to get recent messages")
	}

	// 转换为 PushMessage 格式
	pushMessages := make([]*gatewayv1.PushMessage, 0, len(messages))
	for _, msg := range messages {
		pushMessages = append(pushMessages, &gatewayv1.PushMessage{
			MsgId:        msg.MsgID,
			SeqId:        msg.SeqID,
			SessionId:    req.SessionId,
			FromUsername: msg.SenderUsername,
			ToUsername:   "",
			Content:      msg.Content,
			Type:         msg.MsgType,
			Timestamp:    msg.CreatedAt.Unix(),
		})
	}

	return &logicv1.GetRecentMessagesResponse{
		Messages: pushMessages,
	}, nil
}

// GetContactList 实现 SessionService.GetContactList
func (s *SessionService) GetContactList(ctx context.Context, req *logicv1.GetContactListRequest) (*logicv1.GetContactListResponse, error) {
	s.logger.Info("get contact list", clog.String("username", req.Username))

	// 获取联系人列表
	contacts, err := s.sessionRepo.GetContactList(ctx, req.Username)
	if err != nil {
		s.logger.Error("failed to get contacts", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to get contacts")
	}

	contactInfos := make([]*logicv1.ContactInfo, 0, len(contacts))
	for _, c := range contacts {
		contactInfos = append(contactInfos, &logicv1.ContactInfo{
			Username:  c.Username,
			Nickname:  c.Nickname,
			AvatarUrl: c.Avatar,
		})
	}

	return &logicv1.GetContactListResponse{
		Contacts: contactInfos,
	}, nil
}

// SearchUser 实现 SessionService.SearchUser
func (s *SessionService) SearchUser(ctx context.Context, req *logicv1.SearchUserRequest) (*logicv1.SearchUserResponse, error) {
	s.logger.Info("search user", clog.String("query", req.Query))

	// 搜索用户
	users, err := s.userRepo.SearchUsers(ctx, req.Query)
	if err != nil {
		s.logger.Error("failed to search users", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to search users")
	}

	// 转换为 ContactInfo
	contacts := make([]*logicv1.ContactInfo, len(users))
	for i, u := range users {
		contacts[i] = &logicv1.ContactInfo{
			Username:  u.Username,
			Nickname:  u.Nickname,
			AvatarUrl: u.Avatar,
		}
	}

	return &logicv1.SearchUserResponse{
		Users: contacts,
	}, nil
}

// generateSingleChatID 生成单聊会话 ID
func generateSingleChatID(user1, user2 string) string {
	if user1 < user2 {
		return "single:" + user1 + ":" + user2
	}
	return "single:" + user2 + ":" + user1
}

// generateGroupChatID 生成群聊会话 ID
func (s *SessionService) generateGroupChatID() string {
	return fmt.Sprintf("group:%d", s.sessionIDGen.Next())
}

// UpdateReadPosition 实现 SessionService.UpdateReadPosition
func (s *SessionService) UpdateReadPosition(ctx context.Context, req *logicv1.UpdateReadPositionRequest) (*logicv1.UpdateReadPositionResponse, error) {
	s.logger.Info("update read position",
		clog.String("session_id", req.SessionId),
		clog.String("username", req.Username),
		clog.Int64("seq_id", req.SeqId))

	// 更新已读位置
	if err := s.sessionRepo.UpdateLastReadSeq(ctx, req.SessionId, req.Username, req.SeqId); err != nil {
		s.logger.Error("failed to update read position", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to update read position")
	}

	// 获取当前会话最新 seq_id 以计算未读数
	session, err := s.sessionRepo.GetSession(ctx, req.SessionId)
	if err != nil {
		s.logger.Error("failed to get session", clog.Error(err))
		return &logicv1.UpdateReadPositionResponse{UnreadCount: 0}, nil // 降级处理
	}

	unread := session.MaxSeqID - req.SeqId
	if unread < 0 {
		unread = 0
	}

	return &logicv1.UpdateReadPositionResponse{
		UnreadCount: unread,
	}, nil
}
