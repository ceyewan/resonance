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
	"github.com/ceyewan/resonance/model"
)

func TestSessionService_PullInboxDelta_DefaultLimit(t *testing.T) {
	messageRepo := &testMessageRepo{
		getInboxDeltaFn: func(ctx context.Context, username string, cursorID int64, limit int) ([]*model.Inbox, error) {
			require.Equal(t, "alice", username)
			require.Equal(t, int64(10), cursorID)
			require.Equal(t, 100, limit)
			return nil, nil
		},
	}
	svc := NewSessionService(&testSessionRepo{}, messageRepo, &testUserRepo{}, nil, nil, nil, nil, testLogger())

	resp, err := svc.PullInboxDelta(newTestIncomingContext("alice"), &logicv1.PullInboxDeltaRequest{
		CursorId: 10,
		Limit:    0,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Events)
	require.Equal(t, int64(10), resp.NextCursorId)
	require.False(t, resp.HasMore)
}

func TestSessionService_PullInboxDelta_MaxLimit(t *testing.T) {
	messageRepo := &testMessageRepo{
		getInboxDeltaFn: func(ctx context.Context, username string, cursorID int64, limit int) ([]*model.Inbox, error) {
			require.Equal(t, 500, limit)
			return nil, nil
		},
	}
	svc := NewSessionService(&testSessionRepo{}, messageRepo, &testUserRepo{}, nil, nil, nil, nil, testLogger())

	_, err := svc.PullInboxDelta(newTestIncomingContext("alice"), &logicv1.PullInboxDeltaRequest{
		CursorId: 0,
		Limit:    9999,
	})
	require.NoError(t, err)
}

func TestSessionService_PullInboxDelta_RepoFailed(t *testing.T) {
	messageRepo := &testMessageRepo{
		getInboxDeltaFn: func(ctx context.Context, username string, cursorID int64, limit int) ([]*model.Inbox, error) {
			return nil, errors.New("db failed")
		},
	}
	svc := NewSessionService(&testSessionRepo{}, messageRepo, &testUserRepo{}, nil, nil, nil, nil, testLogger())

	_, err := svc.PullInboxDelta(newTestIncomingContext("alice"), &logicv1.PullInboxDeltaRequest{
		CursorId: 1,
		Limit:    10,
	})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestSessionService_PullInboxDelta_InvalidPayload(t *testing.T) {
	messageRepo := &testMessageRepo{
		getInboxDeltaFn: func(ctx context.Context, username string, cursorID int64, limit int) ([]*model.Inbox, error) {
			return []*model.Inbox{
				{ID: 11, SessionID: "s_1", Payload: []byte("bad-payload")},
			}, nil
		},
	}
	svc := NewSessionService(&testSessionRepo{}, messageRepo, &testUserRepo{}, nil, nil, nil, nil, testLogger())

	_, err := svc.PullInboxDelta(newTestIncomingContext("alice"), &logicv1.PullInboxDeltaRequest{
		CursorId: 5,
		Limit:    10,
	})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestSessionService_PullInboxDelta_Success(t *testing.T) {
	ev1 := &commonv1.ChatEvent{
		EventId:      101,
		SeqId:        7,
		SessionId:    "s_1",
		FromUsername: "bob",
		Payload: &commonv1.ChatEvent_Message{
			Message: &commonv1.Message{
				Type:    commonv1.MessageType_MESSAGE_TYPE_TEXT,
				Content: "hello",
			},
		},
	}
	ev2 := &commonv1.ChatEvent{
		EventId:      102,
		SeqId:        8,
		SessionId:    "s_1",
		FromUsername: "bob",
		Payload: &commonv1.ChatEvent_Message{
			Message: &commonv1.Message{
				Type:    commonv1.MessageType_MESSAGE_TYPE_TEXT,
				Content: "world",
			},
		},
	}
	payload1, err := proto.Marshal(ev1)
	require.NoError(t, err)
	payload2, err := proto.Marshal(ev2)
	require.NoError(t, err)

	messageRepo := &testMessageRepo{
		getInboxDeltaFn: func(ctx context.Context, username string, cursorID int64, limit int) ([]*model.Inbox, error) {
			require.Equal(t, 2, limit)
			return []*model.Inbox{
				{ID: 9, SessionID: "s_1", Payload: payload1},
				{ID: 12, SessionID: "s_1", Payload: payload2},
			}, nil
		},
	}
	svc := NewSessionService(&testSessionRepo{}, messageRepo, &testUserRepo{}, nil, nil, nil, nil, testLogger())

	resp, err := svc.PullInboxDelta(newTestIncomingContext("alice"), &logicv1.PullInboxDeltaRequest{
		CursorId: 6,
		Limit:    2,
	})
	require.NoError(t, err)
	require.Len(t, resp.Events, 2)
	require.Equal(t, int64(12), resp.NextCursorId)
	require.True(t, resp.HasMore)
	require.Equal(t, int64(9), resp.Events[0].InboxId)
	require.Equal(t, int64(101), resp.Events[0].Event.GetEventId())
	require.Equal(t, "hello", resp.Events[0].Event.GetMessage().GetContent())
	require.Equal(t, int64(12), resp.Events[1].InboxId)
	require.Equal(t, int64(102), resp.Events[1].Event.GetEventId())
}
