package dispatcher

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	"github.com/ceyewan/resonance/model"
)

func TestBuildInboxesForEvent_EmptyInput(t *testing.T) {
	items, err := buildInboxesForEvent(nil, []string{"alice"})
	require.NoError(t, err)
	require.Nil(t, items)

	items, err = buildInboxesForEvent(&commonv1.ChatEvent{}, nil)
	require.NoError(t, err)
	require.Nil(t, items)
}

func TestBuildInboxesForEvent_EventTypeMapping(t *testing.T) {
	cases := []struct {
		name      string
		build     func() *commonv1.ChatEvent
		eventType int
	}{
		{
			name: "message",
			build: func() *commonv1.ChatEvent {
				return &commonv1.ChatEvent{
					EventId:      100,
					SeqId:        7,
					SessionId:    "s_1",
					FromUsername: "alice",
					Payload: &commonv1.ChatEvent_Message{
						Message: &commonv1.Message{Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: "x"},
					},
				}
			},
			eventType: model.InboxEventTypeMessage,
		},
		{
			name: "recall",
			build: func() *commonv1.ChatEvent {
				return &commonv1.ChatEvent{
					EventId:      100,
					SeqId:        7,
					SessionId:    "s_1",
					FromUsername: "alice",
					Payload: &commonv1.ChatEvent_Recall{
						Recall: &commonv1.MessageRecall{TargetEventId: 1},
					},
				}
			},
			eventType: model.InboxEventTypeMessageRecall,
		},
		{
			name: "edit",
			build: func() *commonv1.ChatEvent {
				return &commonv1.ChatEvent{
					EventId:      100,
					SeqId:        7,
					SessionId:    "s_1",
					FromUsername: "alice",
					Payload: &commonv1.ChatEvent_Edit{
						Edit: &commonv1.MessageEdit{TargetEventId: 1, NewContent: "new"},
					},
				}
			},
			eventType: model.InboxEventTypeMessageEdit,
		},
		{
			name: "read_receipt",
			build: func() *commonv1.ChatEvent {
				return &commonv1.ChatEvent{
					EventId:      100,
					SeqId:        7,
					SessionId:    "s_1",
					FromUsername: "alice",
					Payload: &commonv1.ChatEvent_ReadReceipt{
						ReadReceipt: &commonv1.ReadReceipt{ReadUptoSeqId: 3},
					},
				}
			},
			eventType: model.InboxEventTypeReadReceipt,
		},
		{
			name: "session_update",
			build: func() *commonv1.ChatEvent {
				return &commonv1.ChatEvent{
					EventId:      100,
					SeqId:        7,
					SessionId:    "s_1",
					FromUsername: "alice",
					Payload: &commonv1.ChatEvent_SessionUpdate{
						SessionUpdate: &commonv1.SessionUpdate{Kind: commonv1.SessionUpdateKind_SESSION_UPDATE_KIND_NAME},
					},
				}
			},
			eventType: model.InboxEventTypeSessionUpdate,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := tc.build()
			items, err := buildInboxesForEvent(ev, []string{"alice", "bob"})
			require.NoError(t, err)
			require.Len(t, items, 2)
			require.Equal(t, tc.eventType, items[0].EventType)
			require.Equal(t, "alice", items[0].OwnerUsername)
			require.Equal(t, "bob", items[1].OwnerUsername)

			decoded := &commonv1.ChatEvent{}
			require.NoError(t, proto.Unmarshal(items[0].Payload, decoded))
			require.Equal(t, int64(100), decoded.EventId)
			require.Equal(t, int64(7), decoded.SeqId)
			require.Equal(t, "s_1", decoded.SessionId)
		})
	}
}
