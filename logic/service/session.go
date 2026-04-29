package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/mq"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/logic/internal/mqpublish"
	"github.com/ceyewan/resonance/logic/observability"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/pkg/event"
	"github.com/ceyewan/resonance/repo"
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
		return &logicv1.GetSessionListResponse{Sessions: []*commonv1.SessionInfo{}}, nil
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

	sessionInfos := make([]*commonv1.SessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		var lastEvent *commonv1.ChatEvent
		if msg, ok := msgMap[sess.SessionID]; ok {
			lastEvent = event.BuildMessageEventFromModel(sess.SessionID, msg)
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
			lastReadSeq = userSess.LastReadSeq
			if count, err := s.messageRepo.GetUnreadMessageCount(ctx, username, sess.SessionID); err == nil {
				unread = count
			} else {
				s.logger.Warn("failed to get unread message count, fallback to seq delta",
					clog.String("session_id", sess.SessionID),
					clog.String("username", username),
					clog.Error(err))
				unread = max(sess.MaxSeqID-userSess.LastReadSeq, 0)
			}
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

		sessionInfos = append(sessionInfos, &commonv1.SessionInfo{
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

	eventID, err := s.msgIDGen.Next()
	if err != nil {
		return fmt.Errorf("generate event id: %w", err)
	}
	seqID, err := s.sequencer.Next(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("generate seq id: %w", err)
	}
	timestampMs := time.Now().UnixMilli()

	msgContent := &model.MessageContent{
		EventID:        eventID,
		SessionID:      sessionID,
		SenderUsername: "system",
		SeqID:          seqID,
		Content:        content,
		MsgType:        event.FormatMessageType(commonv1.MessageType_MESSAGE_TYPE_SYSTEM),
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

	result, err := mqpublish.PublishMessageToMQ(ctx, s.messageRepo, event, msgContent)
	if err != nil {
		return fmt.Errorf("publish message to mq: %w", err)
	}
	mqpublish.PublishMessageToMQAsync(s.mqClient, result.OutboxID, result.Topic, result.EventData, s.logger)
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

func generateSingleChatID(user1, user2 string) string {
	if user1 < user2 {
		return "single:" + user1 + ":" + user2
	}
	return "single:" + user2 + ":" + user1
}

func (s *SessionService) generateGroupChatID() string {
	id, _ := s.sessionIDGen.Next()
	return fmt.Sprintf("group:%d", id)
}

// UpdateReadPosition 实现 SessionService.UpdateReadPosition
func (s *SessionService) UpdateReadPosition(ctx context.Context, req *logicv1.UpdateReadPositionRequest) (*logicv1.UpdateReadPositionResponse, error) {
	username, err := MustUsernameFromCtx(ctx)
	if err != nil {
		return nil, err
	}

	session, err := s.sessionRepo.GetSession(ctx, req.SessionId)
	if err != nil {
		s.logger.Error("failed to get session", clog.Error(err))
		return &logicv1.UpdateReadPositionResponse{UnreadCount: 0}, nil
	}

	timestampMs := time.Now().UnixMilli()
	readAt := time.UnixMilli(timestampMs)
	targetUsernames := make([]string, 0)
	if s.msgIDGen != nil && s.sequencer != nil {
		if session.MaxSeqID > 0 {
			if _, err := s.sequencer.SetIfNotExists(ctx, req.SessionId, session.MaxSeqID); err != nil {
				s.logger.Warn("failed to initialize session sequence for read receipt",
					clog.String("session_id", req.SessionId),
					clog.Error(err))
			}
		}
		if members, err := s.sessionRepo.GetMembers(ctx, req.SessionId); err != nil {
			s.logger.Error("failed to get session members for read receipt", clog.Error(err))
			return nil, status.Errorf(codes.Internal, "failed to get session members")
		} else {
			for _, member := range members {
				if member.Username == username {
					continue
				}
				targetUsernames = append(targetUsernames, member.Username)
			}
		}
	}

	var outbox *model.MessageOutbox
	if len(targetUsernames) > 0 {
		eventID, err := s.msgIDGen.Next()
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to generate event id: %v", err)
		}
		seqID, err := s.sequencer.Next(ctx, req.SessionId)
		if err != nil {
			s.logger.Error("failed to generate seq id for read receipt", clog.Error(err))
			return nil, status.Errorf(codes.Unavailable, "server busy: failed to generate sequence")
		}
		chatEvent := &commonv1.ChatEvent{
			EventId:      eventID,
			SeqId:        seqID,
			SessionId:    req.SessionId,
			FromUsername: username,
			TimestampMs:  timestampMs,
			Payload: &commonv1.ChatEvent_ReadReceipt{
				ReadReceipt: &commonv1.ReadReceipt{ReadUptoSeqId: req.SeqId},
			},
		}
		mqEvent := &mqv1.MQEvent{Event: chatEvent, TargetUsernames: targetUsernames}
		mqEvent.TraceHeaders = make(map[string]string)
		observability.InjectTraceContext(ctx, mqEvent.TraceHeaders)
		eventData, err := proto.Marshal(mqEvent)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to marshal read receipt event")
		}
		topic := proto.GetExtension(mqEvent.ProtoReflect().Descriptor().Options(), commonv1.E_DefaultTopic).(string)
		outbox = &model.MessageOutbox{
			EventID:       eventID,
			Topic:         topic,
			Payload:       eventData,
			Status:        model.OutboxStatusPending,
			NextRetryTime: readAt,
		}
	}

	advanced, err := s.sessionRepo.AdvanceLastReadSeqWithOutbox(ctx, req.SessionId, username, req.SeqId, outbox)
	if err != nil {
		if errors.Is(err, repo.ErrSessionMemberNotFound) {
			return nil, status.Errorf(codes.PermissionDenied, "no permission to access session")
		}
		s.logger.Error("failed to update read position", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to update read position")
	}
	if advanced && outbox != nil {
		mqpublish.PublishMessageToMQAsync(s.mqClient, outbox.ID, outbox.Topic, outbox.Payload, s.logger)
	}

	unread, err := s.messageRepo.GetUnreadMessageCount(ctx, username, req.SessionId)
	if err != nil {
		unread = max(session.MaxSeqID-req.SeqId, 0)
	}

	return &logicv1.UpdateReadPositionResponse{UnreadCount: unread}, nil
}
