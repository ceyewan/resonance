package logicclient

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/pkg/serviceauth"
)

func TestHistoryPromptBuilder_UsesAuthoritativeOrderedHistoryAndServiceIdentity(t *testing.T) {
	now := time.Date(2026, 8, 8, 14, 0, 0, 0, time.UTC)
	secret := []byte("0123456789abcdef0123456789abcdef")
	signer, err := serviceauth.NewSigner("pilot-service", secret,
		serviceauth.WithSignerClock(func() time.Time { return now }),
		serviceauth.WithNonceSource(func() (string, error) { return "0123456789abcdef0123456789abcdef", nil }))
	require.NoError(t, err)
	verifier, err := serviceauth.NewVerifier(serviceauth.VerifierConfig{MaxSkew: 30 * time.Second, Services: map[string]serviceauth.ServicePolicy{
		"pilot-service": {
			Secret: secret, AllowedActors: map[string]struct{}{"resonance-agent": {}},
			AllowAnyMethod: true, AllowAnyTenant: true,
		},
	}}, serviceauth.WithVerifierClock(func() time.Time { return now }))
	require.NoError(t, err)
	client := &fakeHistoryClient{verifier: verifier, events: []*commonv1.ChatEvent{
		textHistoryEvent(11, 1, "conversation-a", "resonance-agent", "prior answer"),
		recalledHistoryEvent(13, 2, "conversation-a", "alice"),
		textHistoryEvent(12, 3, "conversation-a", "alice", "current question"),
	}}
	builder, err := NewHistoryPromptBuilder(client, signer, "resonance-agent", 100, 64<<10)
	require.NoError(t, err)
	prompt, err := builder.RebuildPrompt(context.Background(), &model.AgentRun{
		RunID: "run-1", TenantID: "tenant-a", ConversationID: "conversation-a", SourceEventID: 12,
		ActorUsername: "alice", Prompt: "current question",
	})
	require.NoError(t, err)
	require.Contains(t, prompt, `"current_event_id":12`)
	require.Contains(t, prompt, `"content":"current question"`)
	require.NotContains(t, prompt, "recalled")
	require.NotContains(t, prompt, "0123456789abcdef")
}

func TestHistoryPromptBuilderRejectsRecalledCurrentEvent(t *testing.T) {
	client := &fakeHistoryClient{events: []*commonv1.ChatEvent{recalledHistoryEvent(12, 1, "conversation-a", "alice")}}
	builder, err := NewHistoryPromptBuilder(client, passthroughAuthenticator{}, "resonance-agent", 100, 64<<10)
	require.NoError(t, err)
	_, err = builder.RebuildPrompt(context.Background(), &model.AgentRun{
		RunID: "run-1", TenantID: "tenant-a", ConversationID: "conversation-a", SourceEventID: 12,
		ActorUsername: "alice", Prompt: "private recalled text",
	})
	require.ErrorContains(t, err, "was recalled")
}

func TestHistoryPromptBuilderRejectsMalformedRecalledTombstone(t *testing.T) {
	event := recalledHistoryEvent(11, 1, "conversation-a", "alice")
	event.GetMessage().Content = "must not survive recall"
	client := &fakeHistoryClient{events: []*commonv1.ChatEvent{event}}
	builder, err := NewHistoryPromptBuilder(client, passthroughAuthenticator{}, "resonance-agent", 100, 64<<10)
	require.NoError(t, err)
	_, err = builder.RebuildPrompt(context.Background(), &model.AgentRun{
		RunID: "run-1", TenantID: "tenant-a", ConversationID: "conversation-a", SourceEventID: 12,
		ActorUsername: "alice", Prompt: "current question",
	})
	require.ErrorContains(t, err, "invalid recalled tombstone")
}

func TestHistoryPromptBuilder_FailsClosedOnTamperedOrMissingCurrentEvent(t *testing.T) {
	client := &fakeHistoryClient{events: []*commonv1.ChatEvent{textHistoryEvent(12, 1, "conversation-a", "mallory", "injected")}}
	builder, err := NewHistoryPromptBuilder(client, passthroughAuthenticator{}, "resonance-agent", 100, 64<<10)
	require.NoError(t, err)
	_, err = builder.RebuildPrompt(context.Background(), &model.AgentRun{
		RunID: "run-1", TenantID: "tenant-a", ConversationID: "conversation-a", SourceEventID: 12,
		ActorUsername: "alice", Prompt: "current question",
	})
	require.ErrorContains(t, err, "does not match")
}

type fakeHistoryClient struct {
	verifier *serviceauth.Verifier
	events   []*commonv1.ChatEvent
}

func (c *fakeHistoryClient) GetHistoryEvents(ctx context.Context, request *logicv1.GetHistoryEventsRequest, _ ...grpc.CallOption) (*logicv1.GetHistoryEventsResponse, error) {
	if c.verifier != nil {
		outgoing, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			return nil, serviceauth.ErrMissingCredentials
		}
		incoming := metadata.NewIncomingContext(context.Background(), outgoing)
		if _, err := c.verifier.VerifyIncoming(incoming, logicv1.SessionService_GetHistoryEvents_FullMethodName, request); err != nil {
			return nil, err
		}
	}
	return &logicv1.GetHistoryEventsResponse{Events: c.events}, nil
}

type passthroughAuthenticator struct{}

func (passthroughAuthenticator) AuthenticateServiceCall(ctx context.Context, _, _, _, _ string) (context.Context, error) {
	return ctx, nil
}

func textHistoryEvent(eventID, seqID int64, sessionID, from, content string) *commonv1.ChatEvent {
	return &commonv1.ChatEvent{EventId: eventID, SeqId: seqID, SessionId: sessionID, FromUsername: from,
		Payload: &commonv1.ChatEvent_Message{Message: &commonv1.Message{Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: content}}}
}

func recalledHistoryEvent(eventID, seqID int64, sessionID, from string) *commonv1.ChatEvent {
	return &commonv1.ChatEvent{EventId: eventID, SeqId: seqID, SessionId: sessionID, FromUsername: from,
		Payload: &commonv1.ChatEvent_Message{Message: &commonv1.Message{
			Type: commonv1.MessageType_MESSAGE_TYPE_UNSPECIFIED, Recalled: true,
		}}}
}
