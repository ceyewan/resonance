package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ceyewan/genesis/mq"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
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
	require.Len(t, messageRepo.savedMessage.IdempotencyHash, 64)
	require.Equal(t, int64(7), messageRepo.savedMessage.ReplyToEventID)
	require.False(t, messageRepo.savedMessage.CreatedAt.IsZero())

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

func TestChatService_SendEvent_IdempotentRetryReturnsOriginalAckWithoutAllocating(t *testing.T) {
	createdAt := time.Date(2026, 8, 8, 1, 2, 3, 456000000, time.UTC)
	canonical, err := canonicalMessage(&commonv1.Message{
		Type:        commonv1.MessageType_MESSAGE_TYPE_TEXT,
		Content:     "hello",
		ClientMsgId: "agent:run-1:final",
	})
	require.NoError(t, err)
	hash, err := messageIdempotencyHash("s_1", "bot", canonical)
	require.NoError(t, err)

	sessionRepo := &testSessionRepo{
		getMembersFn: func(context.Context, string) ([]*model.SessionMember, error) {
			return []*model.SessionMember{{Username: "bot"}}, nil
		},
	}
	messageRepo := &testMessageRepo{
		getMessageByIdempotencyFn: func(context.Context, string, string, string) (*model.MessageContent, error) {
			return &model.MessageContent{
				EventID:         7001,
				SessionID:       "s_1",
				SenderUsername:  "bot",
				SeqID:           42,
				ClientMsgID:     "agent:run-1:final",
				IdempotencyHash: hash,
				CreatedAt:       createdAt,
			}, nil
		},
	}
	svc := NewChatService(
		sessionRepo,
		messageRepo,
		&testGenerator{nextFn: func() (int64, error) {
			t.Fatal("id generator must not run on an idempotency hit")
			return 0, nil
		}},
		&testSequencer{nextFn: func(context.Context, string) (int64, error) {
			t.Fatal("sequencer must not run on an idempotency hit")
			return 0, nil
		}},
		&testMQ{},
		testLogger(),
	)

	response, err := svc.SendEvent(newTestIncomingContext("bot"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload:   &logicv1.SendEventRequest_Message{Message: canonical},
	})
	require.NoError(t, err)
	require.Equal(t, int64(7001), response.GetEventId())
	require.Equal(t, int64(42), response.GetSeqId())
	require.Equal(t, createdAt.UnixMilli(), response.GetTimestampMs())
	require.Nil(t, messageRepo.savedMessage)
	require.Nil(t, messageRepo.savedOutbox)
}

func TestChatService_SendEvent_IdempotencyConflict(t *testing.T) {
	sessionRepo := &testSessionRepo{
		getMembersFn: func(context.Context, string) ([]*model.SessionMember, error) {
			return []*model.SessionMember{{Username: "alice"}}, nil
		},
	}
	messageRepo := &testMessageRepo{
		getMessageByIdempotencyFn: func(context.Context, string, string, string) (*model.MessageContent, error) {
			return &model.MessageContent{IdempotencyHash: strings.Repeat("0", 64)}, nil
		},
	}
	svc := NewChatService(sessionRepo, messageRepo, &testGenerator{next: 1}, &testSequencer{}, &testMQ{}, testLogger())

	_, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload: &logicv1.SendEventRequest_Message{Message: &commonv1.Message{
			Type:        commonv1.MessageType_MESSAGE_TYPE_TEXT,
			Content:     "different",
			ClientMsgId: "same-key",
		}},
	})
	require.Error(t, err)
	require.Equal(t, codes.AlreadyExists, status.Code(err))
	require.Nil(t, messageRepo.savedMessage)
}

func TestChatService_SendEvent_ReportsInvalidatedAgentFinalAsAborted(t *testing.T) {
	sessionRepo := &testSessionRepo{
		getMembersFn: func(context.Context, string) ([]*model.SessionMember, error) {
			return []*model.SessionMember{{Username: "bot"}}, nil
		},
	}
	messageRepo := &testMessageRepo{
		saveMessageWithOutboxFn: func(context.Context, *model.MessageContent, *model.MessageOutbox) (*repo.MessageSaveResult, error) {
			return nil, repo.ErrAgentFinalMessageNotCommittable
		},
	}
	svc := NewChatService(sessionRepo, messageRepo, &testGenerator{next: 8101}, &testSequencer{}, &testMQ{}, testLogger())

	_, err := svc.SendEvent(newTestIncomingContext("bot"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload: &logicv1.SendEventRequest_Message{Message: &commonv1.Message{
			Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: "stale result", ClientMsgId: "agent:run-invalidated:final",
		}},
	})
	require.Equal(t, codes.Aborted, status.Code(err))
}

func TestChatService_SendEvent_ConcurrentWinnerDoesNotRepublish(t *testing.T) {
	createdAt := time.Date(2026, 8, 8, 2, 3, 4, 0, time.UTC)
	published := make(chan struct{}, 1)
	mqClient := &testMQ{publishFn: func(context.Context, string, []byte, ...mq.PublishOption) error {
		published <- struct{}{}
		return nil
	}}
	sessionRepo := &testSessionRepo{
		getMembersFn: func(context.Context, string) ([]*model.SessionMember, error) {
			return []*model.SessionMember{{Username: "bot"}}, nil
		},
	}
	messageRepo := &testMessageRepo{
		getMessageByIdempotencyFn: func(context.Context, string, string, string) (*model.MessageContent, error) {
			return nil, repo.ErrMessageNotFound
		},
		saveMessageWithOutboxFn: func(_ context.Context, candidate *model.MessageContent, _ *model.MessageOutbox) (*repo.MessageSaveResult, error) {
			return &repo.MessageSaveResult{Message: &model.MessageContent{
				EventID:         9001,
				SessionID:       candidate.SessionID,
				SenderUsername:  candidate.SenderUsername,
				SeqID:           77,
				ClientMsgID:     candidate.ClientMsgID,
				IdempotencyHash: candidate.IdempotencyHash,
				CreatedAt:       createdAt,
			}, Created: false}, nil
		},
	}
	svc := NewChatService(sessionRepo, messageRepo, &testGenerator{next: 8001}, &testSequencer{}, mqClient, testLogger())

	response, err := svc.SendEvent(newTestIncomingContext("bot"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload: &logicv1.SendEventRequest_Message{Message: &commonv1.Message{
			Type:        commonv1.MessageType_MESSAGE_TYPE_TEXT,
			Content:     "final",
			ClientMsgId: "agent:run-2:final",
		}},
	})
	require.NoError(t, err)
	require.Equal(t, int64(9001), response.GetEventId())
	require.Equal(t, int64(77), response.GetSeqId())
	require.Equal(t, createdAt.UnixMilli(), response.GetTimestampMs())
	require.Never(t, func() bool {
		select {
		case <-published:
			return true
		default:
			return false
		}
	}, 100*time.Millisecond, 10*time.Millisecond)
}

func TestChatService_SendEvent_RejectsInvalidClientMessageID(t *testing.T) {
	sessionRepo := &testSessionRepo{
		getMembersFn: func(context.Context, string) ([]*model.SessionMember, error) {
			return []*model.SessionMember{{Username: "alice"}}, nil
		},
	}
	svc := NewChatService(sessionRepo, &testMessageRepo{}, &testGenerator{next: 1}, &testSequencer{}, &testMQ{}, testLogger())

	for _, id := range []string{strings.Repeat("x", 65), " \t "} {
		_, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
			SessionId: "s_1",
			Payload: &logicv1.SendEventRequest_Message{Message: &commonv1.Message{
				Content:     "hello",
				ClientMsgId: id,
			}},
		})
		require.Error(t, err)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	}
}
