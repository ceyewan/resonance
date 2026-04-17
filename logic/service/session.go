package service

import (
	"context"
	"fmt"
	"strings"
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

// SessionService 会话服务
type SessionService struct {
	logicv1.UnimplementedSessionServiceServer
	sessionRepo  repo.SessionRepo
	messageRepo  repo.MessageRepo
	userRepo     repo.UserRepo
	sessionIDGen idgen.Generator
	msgIDGen     idgen.Generator
	sequencer    idgen.Sequencer
	mqClient     mq.MQ
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
	mqClient mq.MQ,
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
	username, err := MustUsernameFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	sessions, err := s.sessionRepo.GetUserSessionList(ctx, username)
	if err != nil {
		s.logger.Error("failed to get user sessions", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to get user sessions")
	}

	if len(sessions) == 0 {
		return &logicv1.GetSessionListResponse{Sessions: []*logicv1.SessionInfo{}}, nil
	}

	sessionIDs := make([]string, len(sessions))
	for i, sess := range sessions {
		sessionIDs[i] = sess.SessionID
	}
	lastMessages, _ := s.messageRepo.GetLastMessagesBatch(ctx, sessionIDs)
	msgMap := make(map[string]*model.MessageContent, len(lastMessages))
	for _, msg := range lastMessages {
		msgMap[msg.SessionID] = msg
	}

	userSessions, _ := s.sessionRepo.GetUserSessionsBatch(ctx, username, sessionIDs)
	userSessMap := make(map[string]*model.SessionMember, len(userSessions))
	for _, us := range userSessions {
		userSessMap[us.SessionID] = us
	}

	otherUsernames := make([]string, 0)
	for _, sess := range sessions {
		if sess.Type == int(commonv1.SessionType_SESSION_TYPE_DIRECT) && sess.Name == "" {
			parts := strings.Split(sess.SessionID, ":")
			if len(parts) == 3 {
				otherUser := parts[1]
				if otherUser == username {
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

	sessionInfos := make([]*logicv1.SessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		var lastEvent *commonv1.ChatEvent
		if msg, ok := msgMap[sess.SessionID]; ok {
			lastEvent = buildMessageEventFromModel(sess.SessionID, msg)
		} else {
			lastEvent = &commonv1.ChatEvent{
				SeqId:     sess.MaxSeqID,
				SessionId: sess.SessionID,
				Payload: &commonv1.ChatEvent_Message{
					Message: &commonv1.Message{
						Type:    commonv1.MessageType_MESSAGE_TYPE_UNSPECIFIED,
						Content: "",
					},
				},
			}
		}

		userSess := userSessMap[sess.SessionID]
		unread := int64(0)
		lastReadSeq := int64(0)
		if userSess != nil {
			unread = sess.MaxSeqID - userSess.LastReadSeq
			lastReadSeq = userSess.LastReadSeq
		}

		sessionName := sess.Name
		if sess.Type == int(commonv1.SessionType_SESSION_TYPE_DIRECT) && sessionName == "" {
			parts := strings.Split(sess.SessionID, ":")
			if len(parts) == 3 {
				otherUser := parts[1]
				if otherUser == username {
					otherUser = parts[2]
				}
				if user, ok := userMap[otherUser]; ok {
					sessionName = user.Nickname
				}
			}
		}

		sessionInfos = append(sessionInfos, &logicv1.SessionInfo{
			SessionId:   sess.SessionID,
			Name:        sessionName,
			Type:        commonv1.SessionType(sess.Type),
			AvatarUrl:   "",
			UnreadCount: unread,
			LastReadSeq: lastReadSeq,
			LastEvent:   lastEvent,
		})
	}

	return &logicv1.GetSessionListResponse{Sessions: sessionInfos}, nil
}

// CreateSession 实现 SessionService.CreateSession
func (s *SessionService) CreateSession(ctx context.Context, req *logicv1.CreateSessionRequest) (*logicv1.CreateSessionResponse, error) {
	creatorUsername, err := MustUsernameFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	sessionID := ""
	if req.Type == commonv1.SessionType_SESSION_TYPE_DIRECT {
		if len(req.Members) != 1 {
			return nil, status.Errorf(codes.InvalidArgument, "single chat must have exactly one member")
		}
		sessionID = generateSingleChatID(creatorUsername, req.Members[0])
	} else {
		sessionID = s.generateGroupChatID()
	}

	session := &model.Session{
		SessionID:     sessionID,
		Type:          int(req.Type),
		Name:          req.Name,
		OwnerUsername: creatorUsername,
		MaxSeqID:      0,
	}
	if err := s.sessionRepo.CreateSession(ctx, session); err != nil {
		s.logger.Error("failed to create session", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to create session")
	}

	_ = s.sessionRepo.AddMember(ctx, &model.SessionMember{
		SessionID: sessionID,
		Username:  creatorUsername,
		Role:      1,
	})

	for _, member := range req.Members {
		if member == creatorUsername {
			continue
		}
		_ = s.sessionRepo.AddMember(ctx, &model.SessionMember{
			SessionID: sessionID,
			Username:  member,
			Role:      0,
		})
	}

	if err := s.sendSessionCreatedSystemMessage(ctx, sessionID, creatorUsername, req); err != nil {
		s.logger.Error("failed to send system message", clog.Error(err))
	}

	return &logicv1.CreateSessionResponse{SessionId: sessionID}, nil
}

func (s *SessionService) sendSessionCreatedSystemMessage(ctx context.Context, sessionID, creatorUsername string, req *logicv1.CreateSessionRequest) error {
	content := s.buildSystemMessageContent(ctx, creatorUsername, req)
	if content == "" {
		return nil
	}

	eventID := s.msgIDGen.Next()
	seqID, err := s.sequencer.Next(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("generate seq id: %w", err)
	}
	timestampMs := time.Now().UnixMilli()

	msgContent := &model.MessageContent{
		MsgID:          eventID,
		SessionID:      sessionID,
		SenderUsername: "system",
		SeqID:          seqID,
		Content:        content,
		MsgType:        "system",
	}

	chatEvent := &commonv1.ChatEvent{
		EventId:      eventID,
		SeqId:        seqID,
		SessionId:    sessionID,
		FromUsername: "system",
		TimestampMs:  timestampMs,
		Payload: &commonv1.ChatEvent_Message{
			Message: &commonv1.Message{
				Type:    commonv1.MessageType_MESSAGE_TYPE_SYSTEM,
				Content: content,
			},
		},
	}

	seen := map[string]struct{}{creatorUsername: {}}
	targets := []string{creatorUsername}
	for _, m := range req.Members {
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		targets = append(targets, m)
	}
	event := &mqv1.MQEvent{
		Event:           chatEvent,
		TargetUsernames: targets,
	}

	result, err := PublishMessageToMQ(ctx, s.messageRepo, event, msgContent, s.logger)
	if err != nil {
		return fmt.Errorf("publish message to mq: %w", err)
	}
	PublishMessageToMQAsync(s.mqClient, result.OutboxID, result.Topic, result.EventData, s.logger)
	return nil
}

func (s *SessionService) buildSystemMessageContent(ctx context.Context, creatorUsername string, req *logicv1.CreateSessionRequest) string {
	creatorNickname := creatorUsername
	if user, err := s.userRepo.GetUserByUsername(ctx, creatorUsername); err == nil {
		creatorNickname = user.Nickname
	}

	if req.Type == commonv1.SessionType_SESSION_TYPE_DIRECT {
		return fmt.Sprintf("%s 开始了与你的对话", creatorNickname)
	}
	return fmt.Sprintf("%s 创建了群聊「%s」", creatorNickname, req.Name)
}

// GetHistoryEvents 实现 SessionService.GetHistoryEvents
func (s *SessionService) GetHistoryEvents(ctx context.Context, req *logicv1.GetHistoryEventsRequest) (*logicv1.GetHistoryEventsResponse, error) {
	username, err := MustUsernameFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	if req.SessionId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "session_id is required")
	}

	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	if _, err := s.sessionRepo.GetUserSession(ctx, username, req.SessionId); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, status.Errorf(codes.PermissionDenied, "no permission to access session")
		}
		return nil, status.Errorf(codes.Internal, "failed to verify session permission")
	}

	messages, err := s.messageRepo.GetHistoryMessages(ctx, req.SessionId, req.BeforeSeq, limit)
	if err != nil {
		s.logger.Error("failed to get history messages", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to get history messages")
	}

	events := make([]*commonv1.ChatEvent, 0, len(messages))
	for _, msg := range messages {
		events = append(events, buildMessageEventFromModel(req.SessionId, msg))
	}
	return &logicv1.GetHistoryEventsResponse{Events: events}, nil
}

// GetContactList 实现 SessionService.GetContactList
func (s *SessionService) GetContactList(ctx context.Context, req *logicv1.GetContactListRequest) (*logicv1.GetContactListResponse, error) {
	username, err := MustUsernameFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	contacts, err := s.sessionRepo.GetContactList(ctx, username)
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

	return &logicv1.GetContactListResponse{Contacts: contactInfos}, nil
}

// SearchUser 实现 SessionService.SearchUser
func (s *SessionService) SearchUser(ctx context.Context, req *logicv1.SearchUserRequest) (*logicv1.SearchUserResponse, error) {
	if _, err := MustUsernameFromCtx(ctx); err != nil {
		return nil, err
	}

	users, err := s.userRepo.SearchUsers(ctx, req.Query)
	if err != nil {
		s.logger.Error("failed to search users", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to search users")
	}

	contacts := make([]*logicv1.ContactInfo, len(users))
	for i, u := range users {
		contacts[i] = &logicv1.ContactInfo{
			Username:  u.Username,
			Nickname:  u.Nickname,
			AvatarUrl: u.Avatar,
		}
	}

	return &logicv1.SearchUserResponse{Users: contacts}, nil
}

func generateSingleChatID(user1, user2 string) string {
	if user1 < user2 {
		return "single:" + user1 + ":" + user2
	}
	return "single:" + user2 + ":" + user1
}

func (s *SessionService) generateGroupChatID() string {
	return fmt.Sprintf("group:%d", s.sessionIDGen.Next())
}

// UpdateReadPosition 实现 SessionService.UpdateReadPosition
func (s *SessionService) UpdateReadPosition(ctx context.Context, req *logicv1.UpdateReadPositionRequest) (*logicv1.UpdateReadPositionResponse, error) {
	username, err := MustUsernameFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	if err := s.sessionRepo.UpdateLastReadSeq(ctx, req.SessionId, username, req.SeqId); err != nil {
		s.logger.Error("failed to update read position", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to update read position")
	}

	session, err := s.sessionRepo.GetSession(ctx, req.SessionId)
	if err != nil {
		s.logger.Error("failed to get session", clog.Error(err))
		return &logicv1.UpdateReadPositionResponse{UnreadCount: 0}, nil
	}

	unread := session.MaxSeqID - req.SeqId
	if unread < 0 {
		unread = 0
	}

	return &logicv1.UpdateReadPositionResponse{UnreadCount: unread}, nil
}

// PullInboxDelta 实现 SessionService.PullInboxDelta
func (s *SessionService) PullInboxDelta(ctx context.Context, req *logicv1.PullInboxDeltaRequest) (*logicv1.PullInboxDeltaResponse, error) {
	username, err := MustUsernameFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	limit := int(req.Limit)
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	items, err := s.messageRepo.GetInboxDelta(ctx, username, req.CursorId, limit)
	if err != nil {
		s.logger.Error("failed to get inbox delta", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to get inbox delta")
	}

	events := make([]*logicv1.InboxEvent, 0, len(items))
	nextCursorID := req.CursorId
	for _, item := range items {
		events = append(events, &logicv1.InboxEvent{
			InboxId: item.InboxID,
			Event:   buildMessageEventFromInboxItem(item),
		})
		if item.InboxID > nextCursorID {
			nextCursorID = item.InboxID
		}
	}

	return &logicv1.PullInboxDeltaResponse{
		Events:       events,
		NextCursorId: nextCursorID,
		HasMore:      len(items) == limit,
	}, nil
}

func buildMessageEventFromModel(sessionID string, msg *model.MessageContent) *commonv1.ChatEvent {
	return &commonv1.ChatEvent{
		EventId:      msg.MsgID,
		SeqId:        msg.SeqID,
		SessionId:    sessionID,
		FromUsername: msg.SenderUsername,
		TimestampMs:  msg.CreatedAt.UnixMilli(),
		Payload: &commonv1.ChatEvent_Message{
			Message: &commonv1.Message{
				Type:    parseMessageType(msg.MsgType),
				Content: msg.Content,
			},
		},
	}
}

func buildMessageEventFromInboxItem(item *repo.InboxDeltaItem) *commonv1.ChatEvent {
	return &commonv1.ChatEvent{
		EventId:      item.MsgID,
		SeqId:        item.SeqID,
		SessionId:    item.SessionID,
		FromUsername: item.SenderUsername,
		TimestampMs:  item.CreatedAt.UnixMilli(),
		Payload: &commonv1.ChatEvent_Message{
			Message: &commonv1.Message{
				Type:    parseMessageType(item.MsgType),
				Content: item.Content,
			},
		},
	}
}
