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

func editableMessage(sender, sessionID string) *model.MessageContent {
	return &model.MessageContent{
		EventID:        999,
		SessionID:      sessionID,
		SenderUsername: sender,
		SeqID:          5,
		Content:        "hello",
		MsgType:        int(commonv1.MessageType_MESSAGE_TYPE_TEXT),
		CreatedAt:      time.Now().Add(-30 * time.Second),
	}
}

func TestChatService_Edit_MessageNotFound(t *testing.T) {
	msgRepo := &testMessageRepo{
		getMessageByEventIDFn: func(_ context.Context, _ int64) (*model.MessageContent, error) {
			return nil, repo.ErrMessageNotFound
		},
	}
	svc := NewChatService(twoMembersSessionRepo(), msgRepo, &testGenerator{next: 3001}, &testSequencer{}, &testMQ{}, testLogger())

	_, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload:   &logicv1.SendEventRequest_Edit{Edit: &commonv1.MessageEdit{TargetEventId: 999, NewContent: "updated"}},
	})
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Nil(t, msgRepo.savedEditOutbox)
}

func TestChatService_Edit_NotOwner(t *testing.T) {
	msgRepo := &testMessageRepo{
		getMessageByEventIDFn: func(_ context.Context, _ int64) (*model.MessageContent, error) {
			return editableMessage("bob", "s_1"), nil
		},
	}
	svc := NewChatService(twoMembersSessionRepo(), msgRepo, &testGenerator{next: 3001}, &testSequencer{}, &testMQ{}, testLogger())

	_, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload:   &logicv1.SendEventRequest_Edit{Edit: &commonv1.MessageEdit{TargetEventId: 999, NewContent: "updated"}},
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestChatService_Edit_WrongSession(t *testing.T) {
	msgRepo := &testMessageRepo{
		getMessageByEventIDFn: func(_ context.Context, _ int64) (*model.MessageContent, error) {
			return editableMessage("alice", "s_other"), nil
		},
	}
	svc := NewChatService(twoMembersSessionRepo(), msgRepo, &testGenerator{next: 3001}, &testSequencer{}, &testMQ{}, testLogger())

	_, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload:   &logicv1.SendEventRequest_Edit{Edit: &commonv1.MessageEdit{TargetEventId: 999, NewContent: "updated"}},
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestChatService_Edit_RecalledMessage(t *testing.T) {
	msg := editableMessage("alice", "s_1")
	now := time.Now()
	msg.RecalledAt = &now
	msgRepo := &testMessageRepo{
		getMessageByEventIDFn: func(_ context.Context, _ int64) (*model.MessageContent, error) {
			return msg, nil
		},
	}
	svc := NewChatService(twoMembersSessionRepo(), msgRepo, &testGenerator{next: 3001}, &testSequencer{}, &testMQ{}, testLogger())

	_, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload:   &logicv1.SendEventRequest_Edit{Edit: &commonv1.MessageEdit{TargetEventId: 999, NewContent: "updated"}},
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestChatService_Edit_TimeWindowExpired(t *testing.T) {
	msg := editableMessage("alice", "s_1")
	msg.CreatedAt = time.Now().Add(-3 * time.Minute)
	msgRepo := &testMessageRepo{
		getMessageByEventIDFn: func(_ context.Context, _ int64) (*model.MessageContent, error) {
			return msg, nil
		},
	}
	svc := NewChatService(twoMembersSessionRepo(), msgRepo, &testGenerator{next: 3001}, &testSequencer{}, &testMQ{}, testLogger())

	_, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload:   &logicv1.SendEventRequest_Edit{Edit: &commonv1.MessageEdit{TargetEventId: 999, NewContent: "updated"}},
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestChatService_Edit_UnchangedContent(t *testing.T) {
	msgRepo := &testMessageRepo{
		getMessageByEventIDFn: func(_ context.Context, _ int64) (*model.MessageContent, error) {
			return editableMessage("alice", "s_1"), nil
		},
	}
	svc := NewChatService(twoMembersSessionRepo(), msgRepo, &testGenerator{next: 3001}, &testSequencer{}, &testMQ{}, testLogger())

	_, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload:   &logicv1.SendEventRequest_Edit{Edit: &commonv1.MessageEdit{TargetEventId: 999, NewContent: " hello  "}},
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestChatService_Edit_Success(t *testing.T) {
	msgRepo := &testMessageRepo{
		getMessageByEventIDFn: func(_ context.Context, _ int64) (*model.MessageContent, error) {
			return editableMessage("alice", "s_1"), nil
		},
	}
	seq := &testSequencer{nextFn: func(_ context.Context, _ string) (int64, error) { return 6, nil }}
	svc := NewChatService(twoMembersSessionRepo(), msgRepo, &testGenerator{next: 3001}, seq, &testMQ{}, testLogger())

	resp, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload:   &logicv1.SendEventRequest_Edit{Edit: &commonv1.MessageEdit{TargetEventId: 999, NewContent: "updated"}},
	})
	require.NoError(t, err)
	require.Equal(t, int64(3001), resp.EventId)
	require.Equal(t, int64(6), resp.SeqId)
	require.Equal(t, "updated", msgRepo.editedContent)
	require.NotNil(t, msgRepo.savedEditOutbox)

	mqEvent := &mqv1.MQEvent{}
	require.NoError(t, proto.Unmarshal(msgRepo.savedEditOutbox.Payload, mqEvent))
	require.Equal(t, []string{"alice", "bob"}, mqEvent.TargetUsernames)
	require.Equal(t, int64(999), mqEvent.GetEvent().GetEdit().GetTargetEventId())
	require.Equal(t, "updated", mqEvent.GetEvent().GetEdit().GetNewContent())
}

func TestChatService_Edit_AlreadyRecalledRaceCondition(t *testing.T) {
	msgRepo := &testMessageRepo{
		getMessageByEventIDFn: func(_ context.Context, _ int64) (*model.MessageContent, error) {
			return editableMessage("alice", "s_1"), nil
		},
		editMessageWithOutboxFn: func(_ context.Context, _ int64, _ string, _ time.Time, _ *model.MessageOutbox) error {
			return repo.ErrMessageAlreadyRecalled
		},
	}
	seq := &testSequencer{nextFn: func(_ context.Context, _ string) (int64, error) { return 6, nil }}
	svc := NewChatService(twoMembersSessionRepo(), msgRepo, &testGenerator{next: 3001}, seq, &testMQ{}, testLogger())

	_, err := svc.SendEvent(newTestIncomingContext("alice"), &logicv1.SendEventRequest{
		SessionId: "s_1",
		Payload:   &logicv1.SendEventRequest_Edit{Edit: &commonv1.MessageEdit{TargetEventId: 999, NewContent: "updated"}},
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}
