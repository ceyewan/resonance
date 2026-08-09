package dispatcher

import (
	"context"
	"errors"
	"testing"

	"github.com/ceyewan/genesis/clog"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/task/pusher"
)

func TestDispatcher_Handle_EmptyEvent(t *testing.T) {
	d := NewDispatcher(&testMessageRepo{}, &testRouterRepo{}, &testPusherManager{}, clog.Discard())

	err := d.Handle(context.Background(), &mqv1.MQEvent{})
	require.NoError(t, err)
}

func TestDispatcher_Handle_Message_SaveInboxBatch(t *testing.T) {
	messageRepo := &testMessageRepo{}
	d := NewDispatcher(
		messageRepo,
		&testRouterRepo{
			batchGetUsersGatewayFn: func(ctx context.Context, usernames []string) ([]*model.Router, error) {
				// 本用例关注入库，不关注推送成功路径；返回空路由即可。
				return nil, nil
			},
		},
		&testPusherManager{},
		clog.Discard(),
	)

	ev := &commonv1.ChatEvent{
		EventId:      101,
		SeqId:        9,
		SessionId:    "s_1",
		FromUsername: "alice",
		Payload: &commonv1.ChatEvent_Message{
			Message: &commonv1.Message{
				Type:    commonv1.MessageType_MESSAGE_TYPE_TEXT,
				Content: "hello",
			},
		},
	}
	err := d.Handle(context.Background(), &mqv1.MQEvent{
		Event:           ev,
		TargetUsernames: []string{"alice", "bob", "carol"},
	})
	require.NoError(t, err)

	require.Len(t, messageRepo.savedInboxes, 3)
	require.Equal(t, "alice", messageRepo.savedInboxes[0].OwnerUsername)
	require.Equal(t, "bob", messageRepo.savedInboxes[1].OwnerUsername)
	require.Equal(t, int64(101), messageRepo.savedInboxes[0].EventID)
	require.Equal(t, int64(9), messageRepo.savedInboxes[0].SeqID)
	require.Equal(t, model.InboxEventTypeMessage, messageRepo.savedInboxes[0].EventType)

	payload := &commonv1.ChatEvent{}
	require.NoError(t, proto.Unmarshal(messageRepo.savedInboxes[0].Payload, payload))
	require.Equal(t, int64(101), payload.EventId)
	require.Equal(t, "hello", payload.GetMessage().GetContent())
}

func TestDispatcher_Handle_ReadReceipt_SaveInboxFailed(t *testing.T) {
	messageRepo := &testMessageRepo{
		saveInboxBatchFn: func(ctx context.Context, inboxes []*model.Inbox) error {
			return errors.New("db failed")
		},
	}
	d := NewDispatcher(messageRepo, &testRouterRepo{}, &testPusherManager{}, clog.Discard())

	err := d.Handle(context.Background(), &mqv1.MQEvent{
		Event: &commonv1.ChatEvent{
			EventId:      201,
			SeqId:        15,
			SessionId:    "s_2",
			FromUsername: "bob",
			Payload: &commonv1.ChatEvent_ReadReceipt{
				ReadReceipt: &commonv1.ReadReceipt{ReadUptoSeqId: 15},
			},
		},
		TargetUsernames: []string{"bob", "alice"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "save inbox for read_receipt event")
}

func TestDispatcher_Handle_Recall_InvalidPayload(t *testing.T) {
	messageRepo := &testMessageRepo{}
	d := NewDispatcher(messageRepo, &testRouterRepo{}, &testPusherManager{}, clog.Discard())

	err := d.Handle(context.Background(), &mqv1.MQEvent{
		Event: &commonv1.ChatEvent{
			EventId:      301,
			SeqId:        3,
			SessionId:    "s_3",
			FromUsername: "alice",
			Payload: &commonv1.ChatEvent_Recall{
				Recall: &commonv1.MessageRecall{TargetEventId: 0},
			},
		},
		TargetUsernames: []string{"alice", "bob"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid recall payload")
	require.Nil(t, messageRepo.savedInboxes)
}

func TestDispatcher_Handle_UnsupportedPayload_Skip(t *testing.T) {
	messageRepo := &testMessageRepo{
		saveInboxBatchFn: func(ctx context.Context, inboxes []*model.Inbox) error {
			t.Fatalf("unsupported payload should not save inbox")
			return nil
		},
	}
	routerRepo := &testRouterRepo{
		batchGetUsersGatewayFn: func(ctx context.Context, usernames []string) ([]*model.Router, error) {
			t.Fatalf("unsupported payload should not push")
			return nil, nil
		},
	}
	d := NewDispatcher(messageRepo, routerRepo, &testPusherManager{}, clog.Discard())

	err := d.Handle(context.Background(), &mqv1.MQEvent{
		Event: &commonv1.ChatEvent{
			EventId:      401,
			SeqId:        1,
			SessionId:    "s_4",
			FromUsername: "alice",
		},
		TargetUsernames: []string{"alice", "bob"},
	})
	require.NoError(t, err)
}

func TestDispatcher_Handle_PushRouteQueryFailed_DoNotFailAck(t *testing.T) {
	d := NewDispatcher(
		&testMessageRepo{},
		&testRouterRepo{
			batchGetUsersGatewayFn: func(ctx context.Context, usernames []string) ([]*model.Router, error) {
				return nil, errors.New("redis down")
			},
		},
		&testPusherManager{},
		clog.Discard(),
	)

	err := d.Handle(context.Background(), &mqv1.MQEvent{
		Event: &commonv1.ChatEvent{
			EventId:      501,
			SeqId:        20,
			SessionId:    "s_5",
			FromUsername: "alice",
			Payload: &commonv1.ChatEvent_Message{
				Message: &commonv1.Message{
					Type:    commonv1.MessageType_MESSAGE_TYPE_TEXT,
					Content: "ping",
				},
			},
		},
		TargetUsernames: []string{"alice", "bob"},
	})
	require.NoError(t, err)
}

func TestDispatcherPersistsW3CContextInPushTask(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })
	client := &testPusherClient{}
	d := NewDispatcher(
		&testMessageRepo{},
		&testRouterRepo{batchGetUsersGatewayFn: func(context.Context, []string) ([]*model.Router, error) {
			return []*model.Router{{Username: "bob", GatewayID: "gateway-1"}}, nil
		}},
		&testPusherManager{getFn: func(string) (pusher.Client, error) { return client, nil }},
		clog.Discard(),
	)
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	event := &commonv1.ChatEvent{
		EventId: 801, SeqId: 1, SessionId: "s_8", FromUsername: "alice",
		Payload: &commonv1.ChatEvent_Message{Message: &commonv1.Message{Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: "hello"}},
	}

	require.NoError(t, d.Handle(ctx, &mqv1.MQEvent{Event: event, TargetUsernames: []string{"alice", "bob"}}))
	require.Len(t, client.tasks, 1)
	require.NotEmpty(t, client.tasks[0].TraceHeaders["traceparent"])
	require.Len(t, client.tasks[0].TraceHeaders, 1)
}

func TestDispatcher_Handle_Edit_SaveInboxBatch(t *testing.T) {
	messageRepo := &testMessageRepo{}
	d := NewDispatcher(
		messageRepo,
		&testRouterRepo{
			batchGetUsersGatewayFn: func(ctx context.Context, usernames []string) ([]*model.Router, error) {
				return nil, nil
			},
		},
		&testPusherManager{},
		clog.Discard(),
	)

	err := d.Handle(context.Background(), &mqv1.MQEvent{
		Event: &commonv1.ChatEvent{
			EventId:      601,
			SeqId:        31,
			SessionId:    "s_6",
			FromUsername: "alice",
			Payload: &commonv1.ChatEvent_Edit{
				Edit: &commonv1.MessageEdit{
					TargetEventId: 101,
					NewContent:    "edited",
				},
			},
		},
		TargetUsernames: []string{"alice", "bob"},
	})
	require.NoError(t, err)
	require.Len(t, messageRepo.savedInboxes, 2)
	require.Equal(t, model.InboxEventTypeMessageEdit, messageRepo.savedInboxes[0].EventType)
}

func TestDispatcher_Handle_SessionUpdate_SaveInboxBatch(t *testing.T) {
	messageRepo := &testMessageRepo{}
	d := NewDispatcher(
		messageRepo,
		&testRouterRepo{
			batchGetUsersGatewayFn: func(ctx context.Context, usernames []string) ([]*model.Router, error) {
				return nil, nil
			},
		},
		&testPusherManager{},
		clog.Discard(),
	)

	err := d.Handle(context.Background(), &mqv1.MQEvent{
		Event: &commonv1.ChatEvent{
			EventId:      701,
			SeqId:        41,
			SessionId:    "s_7",
			FromUsername: "alice",
			Payload: &commonv1.ChatEvent_SessionUpdate{
				SessionUpdate: &commonv1.SessionUpdate{
					Kind: commonv1.SessionUpdateKind_SESSION_UPDATE_KIND_NAME,
				},
			},
		},
		TargetUsernames: []string{"alice", "bob", "carol"},
	})
	require.NoError(t, err)
	require.Len(t, messageRepo.savedInboxes, 3)
	require.Equal(t, model.InboxEventTypeSessionUpdate, messageRepo.savedInboxes[0].EventType)
}
