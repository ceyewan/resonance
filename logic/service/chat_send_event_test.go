package service

import (
	"context"
	"errors"
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

func TestChatService_SendEvent_DeniedForNonMember(t *testing.T) {
	sessionRepo := &testSessionRepo{
		getMembersFn: func(ctx context.Context, sessionID string) ([]*model.SessionMember, error) {
			return []*model.SessionMember{
				{Username: "alice"},
				{Username: "bob"},
			}, nil
		},
	}
	messageRepo := &testMessageRepo{}

	svc := NewChatService(
		sessionRepo,
		messageRepo,
		&testGenerator{next: 1001},
		&testSequencer{},
		&testMQ{},
		testLogger(),
	)

	_, err := svc.SendEvent(newTestIncomingContext("mallory"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload: &logicv1.SendEventRequest_Message{
			Message: &commonv1.Message{
				Type:    commonv1.MessageType_MESSAGE_TYPE_TEXT,
				Content: "hello",
			},
		},
	})
	require.Error(t, err)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Nil(t, messageRepo.savedMessage)
	require.Nil(t, messageRepo.savedOutbox)
}

func TestChatService_SendEvent_SequencerNextFailed(t *testing.T) {
	sessionRepo := &testSessionRepo{
		getMembersFn: func(ctx context.Context, sessionID string) ([]*model.SessionMember, error) {
			return []*model.SessionMember{
				{Username: "alice"},
			}, nil
		},
	}
	messageRepo := &testMessageRepo{}
	sequencerErr := errors.New("redis unavailable")
	seq := &testSequencer{
		nextFn: func(ctx context.Context, key string) (int64, error) {
			return 0, sequencerErr
		},
	}

	svc := NewChatService(
		sessionRepo,
		messageRepo,
		&testGenerator{next: 1001},
		seq,
		&testMQ{},
		testLogger(),
	)

	_, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload: &logicv1.SendEventRequest_Message{
			Message: &commonv1.Message{
				Type:    commonv1.MessageType_MESSAGE_TYPE_TEXT,
				Content: "hello",
			},
		},
	})
	require.Error(t, err)
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.Nil(t, messageRepo.savedMessage)
	require.Nil(t, messageRepo.savedOutbox)
}

func TestChatService_SendEvent_Success(t *testing.T) {
	sessionRepo := &testSessionRepo{
		getMembersFn: func(ctx context.Context, sessionID string) ([]*model.SessionMember, error) {
			return []*model.SessionMember{
				{Username: "alice"},
				{Username: "bob"},
			}, nil
		},
		getSessionFn: func(ctx context.Context, sessionID string) (*model.Session, error) {
			return &model.Session{
				SessionID: sessionID,
				MaxSeqID:  10,
			}, nil
		},
	}
	messageRepo := &testMessageRepo{}
	seq := &testSequencer{
		nextFn: func(ctx context.Context, key string) (int64, error) {
			return 11, nil
		},
		setIfNotExistsFn: func(ctx context.Context, key string, value int64) (bool, error) {
			return true, nil
		},
	}

	svc := NewChatService(
		sessionRepo,
		messageRepo,
		&testGenerator{next: 1001},
		seq,
		&testMQ{},
		testLogger(),
	)

	resp, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload: &logicv1.SendEventRequest_Message{
			Message: &commonv1.Message{
				Type:           commonv1.MessageType_MESSAGE_TYPE_TEXT,
				Content:        "hello world",
				ClientMsgId:    "cmsg-1",
				ReplyToEventId: 7,
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, int64(1001), resp.EventId)
	require.Equal(t, int64(11), resp.SeqId)
	require.NotZero(t, resp.TimestampMs)

	require.NotNil(t, messageRepo.savedMessage)
	require.Equal(t, int64(1001), messageRepo.savedMessage.EventID)
	require.Equal(t, "s_1", messageRepo.savedMessage.SessionID)
	require.Equal(t, "alice", messageRepo.savedMessage.SenderUsername)
	require.Equal(t, int64(11), messageRepo.savedMessage.SeqID)
	require.Equal(t, "hello world", messageRepo.savedMessage.Content)
	require.Equal(t, "cmsg-1", messageRepo.savedMessage.ClientMsgID)
	require.Equal(t, int64(7), messageRepo.savedMessage.ReplyToEventID)

	require.NotNil(t, messageRepo.savedOutbox)
	require.Equal(t, int64(1001), messageRepo.savedOutbox.EventID)
	require.Equal(t, model.OutboxStatusPending, messageRepo.savedOutbox.Status)
	require.NotEmpty(t, messageRepo.savedOutbox.Topic)
	require.NotEmpty(t, messageRepo.savedOutbox.Payload)

	mqEvent := &mqv1.MQEvent{}
	require.NoError(t, proto.Unmarshal(messageRepo.savedOutbox.Payload, mqEvent))
	require.Equal(t, []string{"alice", "bob"}, mqEvent.TargetUsernames)
	require.Equal(t, int64(1001), mqEvent.GetEvent().GetEventId())
	require.Equal(t, int64(11), mqEvent.GetEvent().GetSeqId())
	require.Equal(t, "s_1", mqEvent.GetEvent().GetSessionId())
	require.Equal(t, "alice", mqEvent.GetEvent().GetFromUsername())
	require.Equal(t, "hello world", mqEvent.GetEvent().GetMessage().GetContent())
}
