package ingress

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	"github.com/ceyewan/resonance/model"
)

type fakeSessionAuthority struct {
	session *model.Session
	err     error
	calls   int
}

func (a *fakeSessionAuthority) GetSession(context.Context, string) (*model.Session, error) {
	a.calls++
	return a.session, a.err
}

// Pilot 私聊之外的会话属于正常 IM 流量，必须直接忽略，不产生任何 Run。
func TestConversationAdmission_IgnoresConversationsWithoutTheBot(t *testing.T) {
	for name, session := range map[string]*model.Session{
		"direct chat between humans": {
			SessionID: "direct:alice:bob", TenantID: "tenant-a", Kind: model.SessionKindStandard,
			Type: int(commonv1.SessionType_SESSION_TYPE_DIRECT),
		},
		"group chat": {
			SessionID: "group:1", TenantID: "tenant-a", Kind: model.SessionKindStandard,
			Type: int(commonv1.SessionType_SESSION_TYPE_GROUP),
		},
	} {
		t.Run(name, func(t *testing.T) {
			admission, err := NewConversationAdmission("agent-bot", &fakeSessionAuthority{session: session})
			require.NoError(t, err)
			conversation, err := admission.Admit(context.Background(), pilotChatEvent("alice"))
			require.NoError(t, err)
			require.False(t, conversation.Found)
		})
	}
}

func TestConversationAdmission_AdmitsOnlyTheOwnerOfAPilotChat(t *testing.T) {
	admission, err := NewConversationAdmission("agent-bot", &fakeSessionAuthority{session: &model.Session{
		SessionID: "agent:alice", TenantID: "tenant-a", Kind: model.SessionKindAI,
		Type: int(commonv1.SessionType_SESSION_TYPE_DIRECT), OwnerUsername: "alice",
	}})
	require.NoError(t, err)

	conversation, err := admission.Admit(context.Background(), pilotChatEvent("alice"))
	require.NoError(t, err)
	require.Equal(t, Conversation{
		Found: true, TenantID: "tenant-a", ConversationID: "agent:alice", ActorUsername: "alice",
	}, conversation)
}

func TestConversationAdmission_RejectsThirdPartyAndBotAuthoredEvents(t *testing.T) {
	admission, err := NewConversationAdmission("agent-bot", &fakeSessionAuthority{session: &model.Session{
		SessionID: "agent:alice", TenantID: "tenant-a", Kind: model.SessionKindAI,
		Type: int(commonv1.SessionType_SESSION_TYPE_DIRECT), OwnerUsername: "alice",
	}})
	require.NoError(t, err)

	conversation, err := admission.Admit(context.Background(), pilotChatEvent("bob"))
	require.ErrorIs(t, err, ErrAdmissionDenied)
	require.False(t, conversation.Found)

	conversation, err = admission.Admit(context.Background(), pilotChatEvent("agent-bot"))
	require.ErrorIs(t, err, ErrAdmissionDenied)
	require.False(t, conversation.Found)
}

func TestConversationAdmission_PropagatesSessionReadFailure(t *testing.T) {
	admission, err := NewConversationAdmission("agent-bot", &fakeSessionAuthority{err: errors.New("database unavailable")})
	require.NoError(t, err)
	_, err = admission.Admit(context.Background(), pilotChatEvent("alice"))
	require.ErrorContains(t, err, "database unavailable")
}

// Bot 不是目标收件人时，事件在查库之前就被忽略了：普通聊天不经过 Pilot 的数据库。
func TestHandler_IgnoresEventsWithoutTheBotInTargetsBeforeAnyLookup(t *testing.T) {
	enqueuer := &fakeEnqueuer{}
	handler, err := NewHandler(
		HandlerConfig{BotUsername: "agent-bot", Assistant: testAssistant()},
		enqueuer, &fakeAdmissionController{err: errors.New("admission must not be consulted")},
	)
	require.NoError(t, err)

	event := testMQEvent()
	event.TargetUsernames = []string{"alice", "bob"}
	result, err := handler.Handle(context.Background(), event)
	require.NoError(t, err)
	require.True(t, result.Ignored)
	require.Empty(t, enqueuer.runs)
}

func pilotChatEvent(from string) *commonv1.ChatEvent {
	return &commonv1.ChatEvent{
		EventId: 1, SeqId: 1, SessionId: "agent:alice", FromUsername: from, TimestampMs: 1,
		Payload: &commonv1.ChatEvent_Message{Message: &commonv1.Message{
			Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: "hello",
		}},
	}
}
