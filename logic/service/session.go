package service

import (
	"context"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/idgen"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/internal/model"
	"github.com/ceyewan/resonance/internal/repo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SessionService 会话服务
type SessionService struct {
	logicv1.UnimplementedSessionServiceServer
	sessionRepo repo.SessionRepo
	messageRepo repo.MessageRepo
	userRepo    repo.UserRepo
	idGen       idgen.Generator
	logger      clog.Logger
}

// NewSessionService 创建会话服务
func NewSessionService(
	sessionRepo repo.SessionRepo,
	messageRepo repo.MessageRepo,
	userRepo repo.UserRepo,
	idGen idgen.Generator,
	logger clog.Logger,
) *SessionService {
	return &SessionService{
		sessionRepo: sessionRepo,
		messageRepo: messageRepo,
		userRepo:    userRepo,
		idGen:       idGen,
		logger:      logger,
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

		// 尝试从数据库获取最后一条消息
		if msg, err := s.messageRepo.GetHistoryMessages(ctx, sess.SessionID, sess.MaxSeqID, 1); err == nil && len(msg) > 0 {
			lastMsg.SeqId = msg[0].SeqID
			lastMsg.Content = msg[0].Content
			lastMsg.Type = msg[0].MsgType
			lastMsg.Timestamp = msg[0].CreatedAt.Unix()
			lastMsg.FromUsername = msg[0].SenderUsername
		}

		// 获取用户会话信息（包含未读数）
		userSess, err := s.sessionRepo.GetUserSession(ctx, req.Username, sess.SessionID)
		unread := int64(0)
		if err == nil && userSess != nil {
			unread = sess.MaxSeqID - userSess.LastReadSeq
		}

		sessionInfos = append(sessionInfos, &logicv1.SessionInfo{
			SessionId:   sess.SessionID,
			Name:        sess.Name,
			Type:        int32(sess.Type),
			AvatarUrl:   "",
			UnreadCount: unread,
			LastReadSeq: userSess.LastReadSeq,
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

	return &logicv1.CreateSessionResponse{
		SessionId: sessionID,
	}, nil
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
	return "group:" + s.idGen.Next()
}
