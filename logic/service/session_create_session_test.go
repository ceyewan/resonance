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
	seq := &testSequencer{
		nextFn: func(ctx context.Context, key string) (int64, error) {
			require.Equal(t, "single:amy:zoe", key)
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
	)

	resp, err := svc.CreateSession(newTestIncomingContext("zoe"), &logicv1.CreateSessionRequest{
		Type:    commonv1.SessionType_SESSION_TYPE_DIRECT,
		Members: []string{"amy"},
	})
	require.NoError(t, err)
	require.Equal(t, "single:amy:zoe", resp.SessionId)

	require.NotNil(t, sessionRepo.createdSession)
	require.Equal(t, "single:amy:zoe", sessionRepo.createdSession.SessionID)
	require.Equal(t, int(commonv1.SessionType_SESSION_TYPE_DIRECT), sessionRepo.createdSession.Type)
	require.Len(t, sessionRepo.addedMembers, 2)
	require.Equal(t, "zoe", sessionRepo.addedMembers[0].Username)
	require.Equal(t, 1, sessionRepo.addedMembers[0].Role)
	require.Equal(t, "amy", sessionRepo.addedMembers[1].Username)
	require.Equal(t, 0, sessionRepo.addedMembers[1].Role)

	require.NotNil(t, messageRepo.savedMessage)
	require.Equal(t, int64(10001), messageRepo.savedMessage.EventID)
	require.Equal(t, int64(21), messageRepo.savedMessage.SeqID)
	require.Equal(t, "single:amy:zoe", messageRepo.savedMessage.SessionID)
	require.Equal(t, "system", messageRepo.savedMessage.SenderUsername)
	require.Equal(t, int(commonv1.MessageType_MESSAGE_TYPE_SYSTEM), messageRepo.savedMessage.MsgType)
	require.Equal(t, "Zoe 开始了与你的对话", messageRepo.savedMessage.Content)
	require.NotNil(t, messageRepo.savedOutbox)

	ev := &mqv1.MQEvent{}
	require.NoError(t, proto.Unmarshal(messageRepo.savedOutbox.Payload, ev))
	require.Equal(t, []string{"zoe", "amy"}, ev.TargetUsernames)
	require.Equal(t, "single:amy:zoe", ev.GetEvent().GetSessionId())
	require.Equal(t, "system", ev.GetEvent().GetFromUsername())
	require.Equal(t, "Zoe 开始了与你的对话", ev.GetEvent().GetMessage().GetContent())
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
