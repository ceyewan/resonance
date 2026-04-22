package service

import (
	"context"
	"testing"
	"time"

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

func twoMembersSessionRepo() *testSessionRepo {
	return &testSessionRepo{
		getMembersFn: func(_ context.Context, _ string) ([]*model.SessionMember, error) {
			return []*model.SessionMember{{Username: "alice"}, {Username: "bob"}}, nil
		},
	}
}

func freshMessage(sender, sessionID string) *model.MessageContent {
	return &model.MessageContent{
		EventID:        999,
		SessionID:      sessionID,
		SenderUsername: sender,
		SeqID:          5,
		Content:        "hello",
		CreatedAt:      time.Now().Add(-30 * time.Second),
	}
}

func TestChatService_Recall_MessageNotFound(t *testing.T) {
	msgRepo := &testMessageRepo{
		getMessageByEventIDFn: func(_ context.Context, _ int64) (*model.MessageContent, error) {
			return nil, repo.ErrMessageNotFound
		},
	}
	svc := NewChatService(twoMembersSessionRepo(), msgRepo, &testGenerator{next: 2001}, &testSequencer{}, &testMQ{}, testLogger())

	_, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload:   &logicv1.SendEventRequest_Recall{Recall: &commonv1.MessageRecall{TargetEventId: 999}},
	})
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Nil(t, msgRepo.savedRecallOutbox)
}

func TestChatService_Recall_NotOwner(t *testing.T) {
	msgRepo := &testMessageRepo{
		getMessageByEventIDFn: func(_ context.Context, _ int64) (*model.MessageContent, error) {
			return freshMessage("bob", "s_1"), nil
		},
	}
	svc := NewChatService(twoMembersSessionRepo(), msgRepo, &testGenerator{next: 2001}, &testSequencer{}, &testMQ{}, testLogger())

	_, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload:   &logicv1.SendEventRequest_Recall{Recall: &commonv1.MessageRecall{TargetEventId: 999}},
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Nil(t, msgRepo.savedRecallOutbox)
}

func TestChatService_Recall_WrongSession(t *testing.T) {
	msgRepo := &testMessageRepo{
		getMessageByEventIDFn: func(_ context.Context, _ int64) (*model.MessageContent, error) {
			return freshMessage("alice", "s_other"), nil
		},
	}
	svc := NewChatService(twoMembersSessionRepo(), msgRepo, &testGenerator{next: 2001}, &testSequencer{}, &testMQ{}, testLogger())

	_, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload:   &logicv1.SendEventRequest_Recall{Recall: &commonv1.MessageRecall{TargetEventId: 999}},
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestChatService_Recall_AlreadyRecalled(t *testing.T) {
	already := freshMessage("alice", "s_1")
	recalledTime := time.Now().Add(-10 * time.Second)
	already.RecalledAt = &recalledTime

	msgRepo := &testMessageRepo{
		getMessageByEventIDFn: func(_ context.Context, _ int64) (*model.MessageContent, error) {
			return already, nil
		},
	}
	svc := NewChatService(twoMembersSessionRepo(), msgRepo, &testGenerator{next: 2001}, &testSequencer{}, &testMQ{}, testLogger())

	_, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload:   &logicv1.SendEventRequest_Recall{Recall: &commonv1.MessageRecall{TargetEventId: 999}},
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestChatService_Recall_TimeWindowExpired(t *testing.T) {
	old := freshMessage("alice", "s_1")
	old.CreatedAt = time.Now().Add(-3 * time.Minute) // 超过 2 分钟窗口

	msgRepo := &testMessageRepo{
		getMessageByEventIDFn: func(_ context.Context, _ int64) (*model.MessageContent, error) {
			return old, nil
		},
	}
	svc := NewChatService(twoMembersSessionRepo(), msgRepo, &testGenerator{next: 2001}, &testSequencer{}, &testMQ{}, testLogger())

	_, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload:   &logicv1.SendEventRequest_Recall{Recall: &commonv1.MessageRecall{TargetEventId: 999}},
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestChatService_Recall_Success(t *testing.T) {
	msgRepo := &testMessageRepo{
		getMessageByEventIDFn: func(_ context.Context, _ int64) (*model.MessageContent, error) {
			return freshMessage("alice", "s_1"), nil
		},
	}
	seq := &testSequencer{
		nextFn: func(_ context.Context, _ string) (int64, error) { return 6, nil },
	}
	svc := NewChatService(twoMembersSessionRepo(), msgRepo, &testGenerator{next: 2001}, seq, &testMQ{}, testLogger())

	resp, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload:   &logicv1.SendEventRequest_Recall{Recall: &commonv1.MessageRecall{TargetEventId: 999}},
	})
	require.NoError(t, err)
	require.Equal(t, int64(2001), resp.EventId)
	require.Equal(t, int64(6), resp.SeqId)
	require.NotZero(t, resp.TimestampMs)

	outbox := msgRepo.savedRecallOutbox
	require.NotNil(t, outbox)
	require.Equal(t, int64(2001), outbox.EventID)
	require.Equal(t, model.OutboxStatusPending, outbox.Status)
	require.NotEmpty(t, outbox.Topic)

	mqEvent := &mqv1.MQEvent{}
	require.NoError(t, proto.Unmarshal(outbox.Payload, mqEvent))
	require.Equal(t, []string{"alice", "bob"}, mqEvent.TargetUsernames)
	ev := mqEvent.GetEvent()
	require.Equal(t, int64(2001), ev.GetEventId())
	require.Equal(t, int64(6), ev.GetSeqId())
	require.Equal(t, "alice", ev.GetFromUsername())
	require.Equal(t, int64(999), ev.GetRecall().GetTargetEventId())
}

func TestChatService_Recall_AlreadyRecalledRaceCondition(t *testing.T) {
	msgRepo := &testMessageRepo{
		getMessageByEventIDFn: func(_ context.Context, _ int64) (*model.MessageContent, error) {
			return freshMessage("alice", "s_1"), nil
		},
		recallMessageWithOutboxFn: func(_ context.Context, _ int64, _ time.Time, _ *model.MessageOutbox) error {
			return repo.ErrMessageAlreadyRecalled
		},
	}
	seq := &testSequencer{
		nextFn: func(_ context.Context, _ string) (int64, error) { return 6, nil },
	}
	svc := NewChatService(twoMembersSessionRepo(), msgRepo, &testGenerator{next: 2001}, seq, &testMQ{}, testLogger())

	_, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload:   &logicv1.SendEventRequest_Recall{Recall: &commonv1.MessageRecall{TargetEventId: 999}},
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}
