package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
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
	agentPolicy  AgentSessionPolicy
	memberships  TenantMembershipReader
	allowLegacy  bool
}

type TenantMembershipReader interface {
	GetTenantMembership(ctx context.Context, tenantID, username string) (*model.TenantMembership, error)
}

var (
	errAgentSessionActorNotHuman  = errors.New("AI session actor must be a human account")
	errAgentSessionBotUnavailable = errors.New("configured agent bot is unavailable")
)

// AgentSessionPolicy is server-owned configuration for the only AI profiles
// that can be selected by CreateAgentSession.
type AgentSessionPolicy struct {
	BotUsername          string
	BotNickname          string
	UserAssistantVersion int64
	IAMAdminVersion      int64
}

type SessionServiceOption func(*SessionService)

func WithAgentSessionPolicy(policy AgentSessionPolicy) SessionServiceOption {
	return func(service *SessionService) {
		service.agentPolicy = policy
	}
}

func WithTenantMembershipReader(reader TenantMembershipReader) SessionServiceOption {
	return func(service *SessionService) {
		service.memberships = reader
	}
}

// WithLegacyGlobalSessionAuthorizationForTests exists only for old in-process
// integration harnesses that provide x-username without a tenant principal.
func WithLegacyGlobalSessionAuthorizationForTests() SessionServiceOption {
	return func(service *SessionService) { service.allowLegacy = true }
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
	options ...SessionServiceOption,
) *SessionService {
	service := &SessionService{
		sessionRepo:  sessionRepo,
		messageRepo:  messageRepo,
		userRepo:     userRepo,
		sessionIDGen: sessionIDGen,
		msgIDGen:     msgIDGen,
		sequencer:    sequencer,
		mqClient:     mqClient,
		logger:       logger,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

// GetSessionList 实现 SessionService.GetSessionList
func (s *SessionService) GetSessionList(ctx context.Context, req *logicv1.GetSessionListRequest) (*logicv1.GetSessionListResponse, error) {
	principal, err := s.requireUserPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	username := principal.Username
	if s.agentPolicy.BotUsername != "" && s.agentPolicy.UserAssistantVersion > 0 {
		if _, ensureErr := s.EnsureDefaultAgentSession(ctx, principal.TenantID, username); ensureErr != nil {
			observability.RecordDefaultAgentSessionProvision(ctx, "session_list_repair", "failed")
			s.logger.Warn("failed to repair default agent session", clog.Error(ensureErr))
		} else {
			observability.RecordDefaultAgentSessionProvision(ctx, "session_list_repair", "succeeded")
		}
	}

	sessions, err := s.sessionRepo.GetUserSessionListByTenant(ctx, principal.TenantID, username)
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
	directPeers := make(map[string]string)
	for _, sess := range sessions {
		if sess == nil || sess.TenantID != principal.TenantID {
			continue
		}
		if sess.Type == int(commonv1.SessionType_SESSION_TYPE_DIRECT) && sess.Name == "" {
			members, memberErr := s.sessionRepo.GetMembers(ctx, sess.SessionID)
			if memberErr == nil {
				for _, member := range members {
					if member != nil && member.Username != username {
						directPeers[sess.SessionID] = member.Username
						otherUsernames = append(otherUsernames, member.Username)
						break
					}
				}
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
		if sess == nil || sess.TenantID != principal.TenantID {
			continue
		}
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
			if user, ok := userMap[directPeers[sess.SessionID]]; ok {
				sessionName = user.Nickname
			}
		}

		sessionInfos = append(sessionInfos, &commonv1.SessionInfo{
			SessionId:           sess.SessionID,
			Name:                sessionName,
			Type:                commonv1.SessionType(sess.Type),
			AvatarUrl:           "",
			UnreadCount:         unread,
			LastReadSeq:         lastReadSeq,
			LastEvent:           lastEvent,
			Kind:                sessionKindToProto(sess.Kind),
			AgentProfile:        agentProfileToProto(sess.ProfileID),
			AgentProfileVersion: sess.ProfileVersion,
		})
	}

	return &logicv1.GetSessionListResponse{Sessions: sessionInfos}, nil
}

// CreateSession 实现 SessionService.CreateSession
func (s *SessionService) CreateSession(ctx context.Context, req *logicv1.CreateSessionRequest) (*logicv1.CreateSessionResponse, error) {
	principal, err := s.requireUserPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if !s.allowLegacy && !containsString(principal.Scopes, model.ScopeChatUse) {
		return nil, status.Error(codes.PermissionDenied, "chat scope is required")
	}
	creatorUsername := principal.Username
	if err := s.requireActiveTenantMember(ctx, principal.TenantID, creatorUsername); err != nil {
		return nil, err
	}

	if req.Type != commonv1.SessionType_SESSION_TYPE_DIRECT && req.Type != commonv1.SessionType_SESSION_TYPE_GROUP {
		return nil, status.Error(codes.InvalidArgument, "session type must be direct or group")
	}
	if req.Type == commonv1.SessionType_SESSION_TYPE_DIRECT && len(req.Members) != 1 {
		return nil, status.Error(codes.InvalidArgument, "single chat must have exactly one member")
	}

	creator, err := s.userRepo.GetUserByUsername(ctx, creatorUsername)
	if err != nil || creator == nil || creator.Kind != model.UserKindHuman {
		return nil, status.Error(codes.PermissionDenied, "only human users can create user sessions")
	}

	sessionID := ""
	if req.Type == commonv1.SessionType_SESSION_TYPE_DIRECT {
		if req.Members[0] == creatorUsername {
			return nil, status.Error(codes.InvalidArgument, "cannot create a direct session with yourself")
		}
		sessionID = generateDirectSessionID(principal.TenantID, creatorUsername, req.Members[0])
	} else {
		generatedID, err := s.generateGroupChatID()
		if err != nil {
			return nil, status.Error(codes.Unavailable, "failed to generate session id")
		}
		sessionID = generatedID
	}

	seenMembers := make(map[string]struct{}, len(req.Members))
	verifiedMembers := make([]string, 0, len(req.Members))
	for _, username := range req.Members {
		if username == "" || username == creatorUsername {
			if req.Type == commonv1.SessionType_SESSION_TYPE_DIRECT {
				return nil, status.Error(codes.InvalidArgument, "invalid direct session member")
			}
			continue
		}
		if _, exists := seenMembers[username]; exists {
			continue
		}
		member, memberErr := s.userRepo.GetUserByUsername(ctx, username)
		if memberErr != nil || member == nil || member.Kind != model.UserKindHuman {
			return nil, status.Error(codes.InvalidArgument, "user session members must be human accounts")
		}
		if err := s.requireActiveTenantMember(ctx, principal.TenantID, username); err != nil {
			return nil, err
		}
		seenMembers[username] = struct{}{}
		verifiedMembers = append(verifiedMembers, username)
	}
	if req.Type == commonv1.SessionType_SESSION_TYPE_GROUP && len(seenMembers) == 0 {
		return nil, status.Error(codes.InvalidArgument, "group session requires at least one other member")
	}
	if req.Type == commonv1.SessionType_SESSION_TYPE_DIRECT {
		existing, findErr := s.sessionRepo.FindDirectSessionByMembers(ctx, principal.TenantID, creatorUsername, verifiedMembers[0])
		switch {
		case findErr == nil:
			return &logicv1.CreateSessionResponse{SessionId: existing.SessionID}, nil
		case errors.Is(findErr, repo.ErrSessionNotFound):
			// First direct conversation for this tenant/member pair.
		default:
			return nil, status.Error(codes.Internal, "failed to resolve direct session")
		}
	}

	session := &model.Session{
		SessionID:     sessionID,
		Type:          int(req.Type),
		Kind:          model.SessionKindStandard,
		TenantID:      principal.TenantID,
		Name:          req.Name,
		OwnerUsername: creatorUsername,
		MaxSeqID:      0,
	}
	members := make([]*model.SessionMember, 0, len(verifiedMembers)+1)
	members = append(members, &model.SessionMember{SessionID: sessionID, Username: creatorUsername, Role: 1})
	for _, member := range verifiedMembers {
		members = append(members, &model.SessionMember{SessionID: sessionID, Username: member, Role: 0})
	}
	persisted, created, err := s.sessionRepo.CreateSessionWithMembers(ctx, session, members)
	if err != nil {
		s.logger.Error("failed to create session", clog.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to create session")
	}
	if created {
		if err := s.sendSessionCreatedSystemMessage(ctx, persisted.SessionID, creatorUsername, req); err != nil {
			s.logger.Error("failed to send system message", clog.Error(err))
		}
	}

	return &logicv1.CreateSessionResponse{SessionId: persisted.SessionID}, nil
}

// CreateAgentSession creates a new, profile-pinned AI conversation. Tenant,
// actor, roles and scopes come only from the authenticated UserPrincipal.
func (s *SessionService) CreateAgentSession(ctx context.Context, req *logicv1.CreateAgentSessionRequest) (*logicv1.CreateAgentSessionResponse, error) {
	principal, err := s.requireUserPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireActiveTenantMember(ctx, principal.TenantID, principal.Username); err != nil {
		return nil, err
	}
	profileID, profileVersion, requiredRole, requiredScope, displayName, ok := s.resolveAgentProfile(req.GetProfile())
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "unsupported agent profile")
	}
	if !containsString(principal.Scopes, requiredScope) || (requiredRole != "" && !containsString(principal.Roles, requiredRole)) {
		return nil, status.Error(codes.PermissionDenied, "agent profile is not permitted")
	}
	persisted, err := s.createPinnedAgentSession(ctx, principal.TenantID, principal.Username, profileID, profileVersion, displayName)
	if err != nil {
		switch {
		case errors.Is(err, errAgentSessionActorNotHuman):
			return nil, status.Error(codes.PermissionDenied, errAgentSessionActorNotHuman.Error())
		case errors.Is(err, errAgentSessionBotUnavailable):
			return nil, status.Error(codes.FailedPrecondition, errAgentSessionBotUnavailable.Error())
		}
		s.logger.Error("failed to create agent session", clog.Error(err))
		return nil, status.Error(codes.Internal, "failed to create agent session")
	}
	return &logicv1.CreateAgentSessionResponse{SessionId: persisted.SessionID}, nil
}

// EnsureDefaultAgentSession is the idempotent provisioning and lazy-repair
// path for the ordinary assistant. IAM profiles are never provisioned here.
func (s *SessionService) EnsureDefaultAgentSession(ctx context.Context, tenantID, username string) (string, error) {
	if tenantID == "" || username == "" || s.agentPolicy.BotUsername == "" || s.agentPolicy.UserAssistantVersion < 1 {
		return "", fmt.Errorf("default agent session policy is unavailable")
	}
	if s.memberships == nil {
		return "", fmt.Errorf("tenant membership reader is unavailable")
	}
	membership, err := s.memberships.GetTenantMembership(ctx, tenantID, username)
	if err != nil || membership == nil || membership.TenantID != tenantID || membership.Username != username ||
		membership.Status != model.TenantMembershipStatusActive {
		return "", fmt.Errorf("active tenant membership is required")
	}
	name := s.agentPolicy.BotNickname
	if name == "" {
		name = "AI Assistant"
	}
	persisted, err := s.createPinnedAgentSession(
		ctx, tenantID, username, model.AgentProfileUserAssistant, s.agentPolicy.UserAssistantVersion, name,
	)
	if err != nil {
		return "", err
	}
	return persisted.SessionID, nil
}

func (s *SessionService) createPinnedAgentSession(
	ctx context.Context,
	tenantID, username, profileID string,
	profileVersion int64,
	displayName string,
) (*model.Session, error) {
	creator, err := s.userRepo.GetUserByUsername(ctx, username)
	if err != nil || creator == nil || creator.Kind != model.UserKindHuman || creator.Username != username {
		return nil, errAgentSessionActorNotHuman
	}
	bot, err := s.userRepo.GetUserByUsername(ctx, s.agentPolicy.BotUsername)
	if err != nil || bot == nil || bot.Kind != model.UserKindAgentBot || bot.Username != s.agentPolicy.BotUsername {
		return nil, errAgentSessionBotUnavailable
	}
	sessionID := generateAgentSessionID(tenantID, username, profileID, profileVersion)
	session := &model.Session{
		SessionID: sessionID, Type: int(commonv1.SessionType_SESSION_TYPE_DIRECT), Kind: model.SessionKindAI,
		TenantID: tenantID, ProfileID: profileID, ProfileVersion: profileVersion,
		Name: displayName, OwnerUsername: username,
	}
	members := []*model.SessionMember{
		{SessionID: sessionID, Username: username, Role: 1},
		{SessionID: sessionID, Username: bot.Username, Role: 0},
	}
	persisted, _, err := s.sessionRepo.CreateSessionWithMembers(ctx, session, members)
	if err != nil {
		return nil, err
	}
	return persisted, nil
}

func (s *SessionService) resolveAgentProfile(profile commonv1.AgentProfile) (profileID string, version int64, role, scope, name string, ok bool) {
	if s.agentPolicy.BotUsername == "" {
		return "", 0, "", "", "", false
	}
	switch profile {
	case commonv1.AgentProfile_AGENT_PROFILE_USER_ASSISTANT:
		if s.agentPolicy.UserAssistantVersion < 1 {
			return "", 0, "", "", "", false
		}
		name = s.agentPolicy.BotNickname
		if name == "" {
			name = "AI Assistant"
		}
		return model.AgentProfileUserAssistant, s.agentPolicy.UserAssistantVersion, "", model.ScopeChatUse, name, true
	case commonv1.AgentProfile_AGENT_PROFILE_IAM_ADMIN:
		if s.agentPolicy.IAMAdminVersion < 1 {
			return "", 0, "", "", "", false
		}
		return model.AgentProfileIAMAdmin, s.agentPolicy.IAMAdminVersion, model.SystemRoleIAMAdmin, model.ScopeIAMUsersRead, "IAM Admin Assistant", true
	default:
		return "", 0, "", "", "", false
	}
}

func containsString(values []string, target string) bool {
	return slices.Contains(values, target)
}

func sessionKindToProto(kind int) commonv1.SessionKind {
	if kind == model.SessionKindAI {
		return commonv1.SessionKind_SESSION_KIND_AI
	}
	return commonv1.SessionKind_SESSION_KIND_STANDARD
}

func agentProfileToProto(profileID string) commonv1.AgentProfile {
	switch profileID {
	case model.AgentProfileUserAssistant:
		return commonv1.AgentProfile_AGENT_PROFILE_USER_ASSISTANT
	case model.AgentProfileIAMAdmin:
		return commonv1.AgentProfile_AGENT_PROFILE_IAM_ADMIN
	default:
		return commonv1.AgentProfile_AGENT_PROFILE_UNSPECIFIED
	}
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
	if result.Created {
		mqpublish.PublishMessageToMQAsync(s.mqClient, result.OutboxID, result.Topic, result.EventData, s.logger)
	}
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

func generateDirectSessionID(tenantID, user1, user2 string) string {
	if user2 < user1 {
		user1, user2 = user2, user1
	}
	return "direct:" + stableSessionDigest("direct-v1", tenantID, user1, user2)
}

func generateAgentSessionID(tenantID, owner, profileID string, profileVersion int64) string {
	return "agent:" + stableSessionDigest("agent-v1", tenantID, owner, profileID, fmt.Sprintf("%d", profileVersion))
}

func stableSessionDigest(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))[:56]
}

func (s *SessionService) generateGroupChatID() (string, error) {
	if s.sessionIDGen == nil {
		return "", fmt.Errorf("session id generator is unavailable")
	}
	id, err := s.sessionIDGen.Next()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("group:%d", id), nil
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
	tenantID, tenantErr := requireTrustedTenant(ctx)
	if tenantErr != nil && s.allowLegacy {
		tenantID = model.DefaultTenantID
		tenantErr = nil
	}
	if tenantErr != nil {
		return nil, tenantErr
	}
	if session.TenantID == "" || session.TenantID != tenantID {
		return nil, status.Error(codes.PermissionDenied, "session belongs to another tenant")
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
