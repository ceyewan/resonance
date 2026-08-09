package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/model"
)

func TestSessionService_CreateSession_DirectRequiresExactlyOneMember(t *testing.T) {
	sessionRepo := &testSessionRepo{}
	messageRepo := &testMessageRepo{}
	svc := NewSessionService(
		sessionRepo,
		messageRepo,
		&testUserRepo{},
		&testGenerator{next: 1},
		&testGenerator{next: 2},
		&testSequencer{},
		&testMQ{},
		testLogger(),
		withTestTenantMemberships(),
	)

	_, err := svc.CreateSession(newTestIncomingContext("alice"), &logicv1.CreateSessionRequest{
		Type:    commonv1.SessionType_SESSION_TYPE_DIRECT,
		Members: []string{"bob", "carol"},
	})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Nil(t, sessionRepo.createdSession)
	require.Nil(t, messageRepo.savedMessage)
	require.Nil(t, messageRepo.savedOutbox)
}

func TestSessionService_CreateSession_DirectSuccess(t *testing.T) {
	sessionRepo := &testSessionRepo{}
	messageRepo := &testMessageRepo{}
	expectedSessionID := generateDirectSessionID(model.DefaultTenantID, "amy", "zoe")
	seq := &testSequencer{
		nextFn: func(ctx context.Context, key string) (int64, error) {
			require.Equal(t, expectedSessionID, key)
			return 21, nil
		},
	}
	userRepo := &testUserRepo{
		getUserByUsernameFn: func(ctx context.Context, username string) (*model.User, error) {
			return &model.User{Username: username, Nickname: "Zoe"}, nil
		},
	}
	svc := NewSessionService(
		sessionRepo,
		messageRepo,
		userRepo,
		&testGenerator{next: 9001},
		&testGenerator{next: 10001},
		seq,
		&testMQ{},
		testLogger(),
		withTestTenantMemberships(),
	)

	resp, err := svc.CreateSession(newTestIncomingContext("zoe"), &logicv1.CreateSessionRequest{
		Type:    commonv1.SessionType_SESSION_TYPE_DIRECT,
		Members: []string{"amy"},
	})
	require.NoError(t, err)
	require.Equal(t, expectedSessionID, resp.SessionId)

	require.NotNil(t, sessionRepo.createdSession)
	require.Equal(t, expectedSessionID, sessionRepo.createdSession.SessionID)
	require.Equal(t, model.DefaultTenantID, sessionRepo.createdSession.TenantID)
	require.Equal(t, int(commonv1.SessionType_SESSION_TYPE_DIRECT), sessionRepo.createdSession.Type)
	require.Len(t, sessionRepo.addedMembers, 2)
	require.Equal(t, "zoe", sessionRepo.addedMembers[0].Username)
	require.Equal(t, 1, sessionRepo.addedMembers[0].Role)
	require.Equal(t, "amy", sessionRepo.addedMembers[1].Username)
	require.Equal(t, 0, sessionRepo.addedMembers[1].Role)

	require.NotNil(t, messageRepo.savedMessage)
	require.Equal(t, int64(10001), messageRepo.savedMessage.EventID)
	require.Equal(t, int64(21), messageRepo.savedMessage.SeqID)
	require.Equal(t, expectedSessionID, messageRepo.savedMessage.SessionID)
	require.Equal(t, "system", messageRepo.savedMessage.SenderUsername)
	require.Equal(t, int(commonv1.MessageType_MESSAGE_TYPE_SYSTEM), messageRepo.savedMessage.MsgType)
	require.Equal(t, "Zoe 开始了与你的对话", messageRepo.savedMessage.Content)
	require.NotNil(t, messageRepo.savedOutbox)

	ev := &mqv1.MQEvent{}
	require.NoError(t, proto.Unmarshal(messageRepo.savedOutbox.Payload, ev))
	require.Equal(t, []string{"zoe", "amy"}, ev.TargetUsernames)
	require.Equal(t, expectedSessionID, ev.GetEvent().GetSessionId())
	require.Equal(t, "system", ev.GetEvent().GetFromUsername())
	require.Equal(t, "Zoe 开始了与你的对话", ev.GetEvent().GetMessage().GetContent())
}

func TestSessionService_CreateSession_RejectsProtectedBot(t *testing.T) {
	sessionRepo := &testSessionRepo{}
	messageRepo := &testMessageRepo{}
	userRepo := &testUserRepo{
		getUserByUsernameFn: func(_ context.Context, username string) (*model.User, error) {
			kind := model.UserKindHuman
			if username == "resonance-agent" {
				kind = model.UserKindAgentBot
			}
			return &model.User{Username: username, Nickname: username, Kind: kind}, nil
		},
	}
	svc := NewSessionService(
		sessionRepo,
		messageRepo,
		userRepo,
		&testGenerator{next: 1},
		&testGenerator{next: 2},
		&testSequencer{},
		&testMQ{},
		testLogger(),
		withTestTenantMemberships(),
	)

	_, err := svc.CreateSession(newTestIncomingContext("alice"), &logicv1.CreateSessionRequest{
		Type:    commonv1.SessionType_SESSION_TYPE_DIRECT,
		Members: []string{"resonance-agent"},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Nil(t, sessionRepo.createdSession)
	require.Empty(t, sessionRepo.addedMembers)
}

func TestSessionService_CreateSession_ReusesLegacyDirectInsideTenant(t *testing.T) {
	sessions := &testSessionRepo{findDirectSessionFn: func(_ context.Context, tenantID, username1, username2 string) (*model.Session, error) {
		require.Equal(t, model.DefaultTenantID, tenantID)
		require.Equal(t, "alice", username1)
		require.Equal(t, "bob", username2)
		return &model.Session{SessionID: "single:alice:bob", TenantID: tenantID, Kind: model.SessionKindStandard}, nil
	}}
	users := &testUserRepo{getUserByUsernameFn: func(_ context.Context, username string) (*model.User, error) {
		return &model.User{Username: username, Kind: model.UserKindHuman}, nil
	}}
	svc := NewSessionService(sessions, &testMessageRepo{}, users, &testGenerator{next: 1}, nil, nil, nil, testLogger(), withTestTenantMemberships())

	response, err := svc.CreateSession(newTestIncomingContext("alice"), &logicv1.CreateSessionRequest{
		Type: commonv1.SessionType_SESSION_TYPE_DIRECT, Members: []string{"bob"},
	})
	require.NoError(t, err)
	require.Equal(t, "single:alice:bob", response.SessionId)
	require.Nil(t, sessions.createdSession)
}

func TestSessionService_CreateAgentSession_UsesTrustedPrincipalAndPinnedProfile(t *testing.T) {
	sessionRepo := &testSessionRepo{}
	userRepo := &testUserRepo{getUserByUsernameFn: func(_ context.Context, username string) (*model.User, error) {
		kind := model.UserKindHuman
		if username == "resonance-agent" {
			kind = model.UserKindAgentBot
		}
		return &model.User{Username: username, Kind: kind}, nil
	}}
	svc := NewSessionService(
		sessionRepo, &testMessageRepo{}, userRepo, &testGenerator{next: 41}, nil, nil, nil, testLogger(),
		withTestTenantMemberships(),
		WithAgentSessionPolicy(AgentSessionPolicy{
			BotUsername: "resonance-agent", BotNickname: "Assistant", UserAssistantVersion: 7, IAMAdminVersion: 3,
		}),
	)
	ctx := WithUserPrincipal(context.Background(), &UserPrincipal{
		TenantID: "tenant-a", Username: "alice", Version: 9,
		Roles: []string{model.SystemRoleUser}, Scopes: []string{model.ScopeChatUse},
	})

	response, err := svc.CreateAgentSession(ctx, &logicv1.CreateAgentSessionRequest{
		Profile: commonv1.AgentProfile_AGENT_PROFILE_USER_ASSISTANT,
	})
	require.NoError(t, err)
	expectedSessionID := generateAgentSessionID("tenant-a", "alice", model.AgentProfileUserAssistant, 7)
	require.Equal(t, expectedSessionID, response.SessionId)
	require.Equal(t, &model.Session{
		SessionID: expectedSessionID, Type: int(commonv1.SessionType_SESSION_TYPE_DIRECT), Kind: model.SessionKindAI,
		TenantID: "tenant-a", ProfileID: model.AgentProfileUserAssistant, ProfileVersion: 7,
		Name: "Assistant", OwnerUsername: "alice",
	}, sessionRepo.createdSession)
	require.Equal(t, []string{"alice", "resonance-agent"}, []string{
		sessionRepo.addedMembers[0].Username, sessionRepo.addedMembers[1].Username,
	})
}

func TestSessionService_EnsureDefaultAgentSessionCreatesOnlyUserAssistant(t *testing.T) {
	sessionRepo := &testSessionRepo{}
	userRepo := &testUserRepo{getUserByUsernameFn: func(_ context.Context, username string) (*model.User, error) {
		kind := model.UserKindHuman
		if username == "resonance-agent" {
			kind = model.UserKindAgentBot
		}
		return &model.User{Username: username, Kind: kind}, nil
	}}
	svc := NewSessionService(
		sessionRepo, &testMessageRepo{}, userRepo, nil, nil, nil, nil, testLogger(),
		withTestTenantMemberships(),
		WithAgentSessionPolicy(AgentSessionPolicy{
			BotUsername: "resonance-agent", BotNickname: "Assistant", UserAssistantVersion: 3, IAMAdminVersion: 9,
		}),
	)

	sessionID, err := svc.EnsureDefaultAgentSession(context.Background(), "tenant-a", "alice")
	require.NoError(t, err)
	require.Equal(t, generateAgentSessionID("tenant-a", "alice", model.AgentProfileUserAssistant, 3), sessionID)
	require.Equal(t, model.AgentProfileUserAssistant, sessionRepo.createdSession.ProfileID)
	require.Equal(t, int64(3), sessionRepo.createdSession.ProfileVersion)
	require.Equal(t, []string{"alice", "resonance-agent"}, []string{
		sessionRepo.addedMembers[0].Username, sessionRepo.addedMembers[1].Username,
	})
}

func TestSessionService_CreateAgentSession_RejectsProfileEscalation(t *testing.T) {
	userRepo := &testUserRepo{getUserByUsernameFn: func(_ context.Context, username string) (*model.User, error) {
		kind := model.UserKindHuman
		if username == "resonance-agent" {
			kind = model.UserKindAgentBot
		}
		return &model.User{Username: username, Kind: kind}, nil
	}}
	svc := NewSessionService(
		&testSessionRepo{}, &testMessageRepo{}, userRepo, &testGenerator{next: 1}, nil, nil, nil, testLogger(),
		withTestTenantMemberships(),
		WithAgentSessionPolicy(AgentSessionPolicy{BotUsername: "resonance-agent", UserAssistantVersion: 1, IAMAdminVersion: 1}),
	)

	for _, principal := range []*UserPrincipal{
		{TenantID: "tenant-a", Username: "alice", Roles: []string{model.SystemRoleUser}, Scopes: []string{model.ScopeChatUse}},
		{TenantID: "tenant-a", Username: "alice", Roles: []string{model.SystemRoleIAMAdmin}, Scopes: []string{model.ScopeChatUse}},
		{TenantID: "tenant-a", Username: "alice", Roles: []string{model.SystemRoleUser}, Scopes: []string{model.ScopeIAMUsersRead}},
	} {
		_, err := svc.CreateAgentSession(WithUserPrincipal(context.Background(), principal), &logicv1.CreateAgentSessionRequest{
			Profile: commonv1.AgentProfile_AGENT_PROFILE_IAM_ADMIN,
		})
		require.Equal(t, codes.PermissionDenied, status.Code(err))
	}
	_, err := svc.CreateAgentSession(context.Background(), &logicv1.CreateAgentSessionRequest{
		Profile: commonv1.AgentProfile_AGENT_PROFILE_USER_ASSISTANT,
	})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestSessionService_CreateAgentSession_PreservesActorAndBotStatusCodes(t *testing.T) {
	principal := &UserPrincipal{
		TenantID: "tenant-a", Username: "alice", Roles: []string{model.SystemRoleUser}, Scopes: []string{model.ScopeChatUse},
	}
	for _, test := range []struct {
		name string
		user func(string) *model.User
		code codes.Code
	}{
		{
			name: "actor is not human",
			user: func(username string) *model.User {
				return &model.User{Username: username, Kind: model.UserKindAgentBot}
			},
			code: codes.PermissionDenied,
		},
		{
			name: "configured bot is unavailable",
			user: func(username string) *model.User {
				if username == "alice" {
					return &model.User{Username: username, Kind: model.UserKindHuman}
				}
				return &model.User{Username: username, Kind: model.UserKindHuman}
			},
			code: codes.FailedPrecondition,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			users := &testUserRepo{getUserByUsernameFn: func(_ context.Context, username string) (*model.User, error) {
				return test.user(username), nil
			}}
			svc := NewSessionService(
				&testSessionRepo{}, &testMessageRepo{}, users, nil, nil, nil, nil, testLogger(),
				withTestTenantMemberships(),
				WithAgentSessionPolicy(AgentSessionPolicy{BotUsername: "resonance-agent", UserAssistantVersion: 1}),
			)
			_, err := svc.CreateAgentSession(
				WithUserPrincipal(context.Background(), principal),
				&logicv1.CreateAgentSessionRequest{Profile: commonv1.AgentProfile_AGENT_PROFILE_USER_ASSISTANT},
			)
			require.Equal(t, test.code, status.Code(err))
		})
	}
}

func TestSessionService_CreateAgentSession_IsolatesProfilesForSameUser(t *testing.T) {
	var nextID int64 = 100
	created := make([]model.Session, 0, 2)
	sessionRepo := &testSessionRepo{createSessionFn: func(_ context.Context, session *model.Session) error {
		created = append(created, *session)
		return nil
	}}
	userRepo := &testUserRepo{getUserByUsernameFn: func(_ context.Context, username string) (*model.User, error) {
		kind := model.UserKindHuman
		if username == "resonance-agent" {
			kind = model.UserKindAgentBot
		}
		return &model.User{Username: username, Kind: kind}, nil
	}}
	svc := NewSessionService(
		sessionRepo, &testMessageRepo{}, userRepo, &testGenerator{nextFn: func() (int64, error) {
			nextID++
			return nextID, nil
		}}, nil, nil, nil, testLogger(),
		withTestTenantMemberships(),
		WithAgentSessionPolicy(AgentSessionPolicy{BotUsername: "resonance-agent", UserAssistantVersion: 2, IAMAdminVersion: 5}),
	)
	ctx := WithUserPrincipal(context.Background(), &UserPrincipal{
		TenantID: "tenant-a", Username: "admin", Roles: []string{model.SystemRoleIAMAdmin},
		Scopes: []string{model.ScopeChatUse, model.ScopeIAMUsersRead},
	})

	first, err := svc.CreateAgentSession(ctx, &logicv1.CreateAgentSessionRequest{Profile: commonv1.AgentProfile_AGENT_PROFILE_USER_ASSISTANT})
	require.NoError(t, err)
	second, err := svc.CreateAgentSession(ctx, &logicv1.CreateAgentSessionRequest{Profile: commonv1.AgentProfile_AGENT_PROFILE_IAM_ADMIN})
	require.NoError(t, err)
	require.NotEqual(t, first.SessionId, second.SessionId)
	require.Equal(t, []string{model.AgentProfileUserAssistant, model.AgentProfileIAMAdmin}, []string{created[0].ProfileID, created[1].ProfileID})
	require.Equal(t, []int64{2, 5}, []int64{created[0].ProfileVersion, created[1].ProfileVersion})
}

func TestSessionService_CreateSession_GroupSuccess(t *testing.T) {
	sessionRepo := &testSessionRepo{}
	messageRepo := &testMessageRepo{}
	seq := &testSequencer{
		nextFn: func(ctx context.Context, key string) (int64, error) {
			require.Equal(t, "group:3001", key)
			return 9, nil
		},
	}
	userRepo := &testUserRepo{
		getUserByUsernameFn: func(ctx context.Context, username string) (*model.User, error) {
			return &model.User{Username: username, Nickname: "Alice"}, nil
		},
	}
	svc := NewSessionService(
		sessionRepo,
		messageRepo,
		userRepo,
		&testGenerator{next: 3001},
		&testGenerator{next: 7001},
		seq,
		&testMQ{},
		testLogger(),
		withTestTenantMemberships(),
	)

	resp, err := svc.CreateSession(newTestIncomingContext("alice"), &logicv1.CreateSessionRequest{
		Type:    commonv1.SessionType_SESSION_TYPE_GROUP,
		Name:    "Dev Team",
		Members: []string{"bob", "carol"},
	})
	require.NoError(t, err)
	require.Equal(t, "group:3001", resp.SessionId)

	require.NotNil(t, sessionRepo.createdSession)
	require.Equal(t, "group:3001", sessionRepo.createdSession.SessionID)
	require.Equal(t, int(commonv1.SessionType_SESSION_TYPE_GROUP), sessionRepo.createdSession.Type)
	require.Equal(t, "Dev Team", sessionRepo.createdSession.Name)
	require.Equal(t, "alice", sessionRepo.createdSession.OwnerUsername)

	require.Len(t, sessionRepo.addedMembers, 3)
	require.Equal(t, "alice", sessionRepo.addedMembers[0].Username)
	require.Equal(t, 1, sessionRepo.addedMembers[0].Role)
	require.Equal(t, "bob", sessionRepo.addedMembers[1].Username)
	require.Equal(t, "carol", sessionRepo.addedMembers[2].Username)

	require.NotNil(t, messageRepo.savedMessage)
	require.Equal(t, "Alice 创建了群聊「Dev Team」", messageRepo.savedMessage.Content)
	require.Equal(t, int64(7001), messageRepo.savedMessage.EventID)
	require.Equal(t, int64(9), messageRepo.savedMessage.SeqID)
}
