package service

import (
	"context"

	"connectrpc.com/connect"
	"github.com/ceyewan/genesis/clog"
	logicv1 "github.com/ceyewan/resonance/im-api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/im-sdk/repo"
)

// SessionService 会话服务
type SessionService struct {
	sessionRepo repo.SessionRepository
	contactRepo repo.ContactRepository
	messageRepo repo.MessageRepository
	userRepo    repo.UserRepository
	logger      clog.Logger
}

// NewSessionService 创建会话服务
func NewSessionService(
	sessionRepo repo.SessionRepository,
	contactRepo repo.ContactRepository,
	messageRepo repo.MessageRepository,
	userRepo repo.UserRepository,
	logger clog.Logger,
) *SessionService {
	return &SessionService{
		sessionRepo: sessionRepo,
		contactRepo: contactRepo,
		messageRepo: messageRepo,
		userRepo:    userRepo,
		logger:      logger,
	}
}

// GetSessionList 实现 SessionService.GetSessionList
func (s *SessionService) GetSessionList(
	ctx context.Context,
	req *connect.Request[logicv1.GetSessionListRequest],
) (*connect.Response[logicv1.GetSessionListResponse], error) {
	s.logger.Info("get session list", clog.String("username", req.Msg.Username))

	// 获取用户的所有会话
	sessions, err := s.sessionRepo.GetUserSessions(ctx, req.Msg.Username)
	if err != nil {
		s.logger.Error("failed to get user sessions", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &logicv1.GetSessionListResponse{
		Sessions: sessions,
	}

	return connect.NewResponse(resp), nil
}

// CreateSession 实现 SessionService.CreateSession
func (s *SessionService) CreateSession(
	ctx context.Context,
	req *connect.Request[logicv1.CreateSessionRequest],
) (*connect.Response[logicv1.CreateSessionResponse], error) {
	s.logger.Info("create session",
		clog.String("creator", req.Msg.CreatorUsername),
		clog.Int32("type", req.Msg.Type))

	// 创建会话
	sessionID, err := s.sessionRepo.CreateSession(
		ctx,
		req.Msg.CreatorUsername,
		req.Msg.Members,
		req.Msg.Name,
		req.Msg.Type,
	)
	if err != nil {
		s.logger.Error("failed to create session", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &logicv1.CreateSessionResponse{
		SessionId: sessionID,
	}

	return connect.NewResponse(resp), nil
}

// GetRecentMessages 实现 SessionService.GetRecentMessages
func (s *SessionService) GetRecentMessages(
	ctx context.Context,
	req *connect.Request[logicv1.GetRecentMessagesRequest],
) (*connect.Response[logicv1.GetRecentMessagesResponse], error) {
	s.logger.Info("get recent messages",
		clog.String("session_id", req.Msg.SessionId),
		clog.Int64("limit", req.Msg.Limit))

	// 获取历史消息
	messages, err := s.messageRepo.GetRecentMessages(
		ctx,
		req.Msg.SessionId,
		req.Msg.Limit,
		req.Msg.BeforeSeq,
	)
	if err != nil {
		s.logger.Error("failed to get recent messages", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &logicv1.GetRecentMessagesResponse{
		Messages: messages,
	}

	return connect.NewResponse(resp), nil
}

// GetContactList 实现 SessionService.GetContactList
func (s *SessionService) GetContactList(
	ctx context.Context,
	req *connect.Request[logicv1.GetContactListRequest],
) (*connect.Response[logicv1.GetContactListResponse], error) {
	s.logger.Info("get contact list", clog.String("username", req.Msg.Username))

	// 获取联系人列表
	contacts, err := s.contactRepo.GetContacts(ctx, req.Msg.Username)
	if err != nil {
		s.logger.Error("failed to get contacts", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &logicv1.GetContactListResponse{
		Contacts: contacts,
	}

	return connect.NewResponse(resp), nil
}

// SearchUser 实现 SessionService.SearchUser
func (s *SessionService) SearchUser(
	ctx context.Context,
	req *connect.Request[logicv1.SearchUserRequest],
) (*connect.Response[logicv1.SearchUserResponse], error) {
	s.logger.Info("search user", clog.String("query", req.Msg.Query))

	// 搜索用户
	users, err := s.userRepo.SearchUsers(ctx, req.Msg.Query, 20)
	if err != nil {
		s.logger.Error("failed to search users", clog.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// 转换为 ContactInfo
	contacts := make([]*logicv1.ContactInfo, len(users))
	for i, u := range users {
		contacts[i] = &logicv1.ContactInfo{
			Username:  u.Username,
			Nickname:  u.Nickname,
			AvatarUrl: u.AvatarUrl,
		}
	}

	resp := &logicv1.SearchUserResponse{
		Users: contacts,
	}

	return connect.NewResponse(resp), nil
}

