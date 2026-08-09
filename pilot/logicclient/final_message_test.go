package logicclient

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/pilot/coordinator"
	"github.com/ceyewan/resonance/pkg/serviceauth"
)

func TestFinalMessageWriter_SendsDeterministicIdempotentMessageWithServiceIdentity(t *testing.T) {
	now := time.Date(2026, 8, 8, 14, 0, 0, 0, time.UTC)
	secret := []byte("0123456789abcdef0123456789abcdef")
	signer, err := serviceauth.NewSigner("pilot-service", secret,
		serviceauth.WithSignerClock(func() time.Time { return now }),
		serviceauth.WithNonceSource(func() (string, error) { return "0123456789abcdef0123456789abcdef", nil }),
	)
	require.NoError(t, err)
	verifier, err := serviceauth.NewVerifier(serviceauth.VerifierConfig{
		MaxSkew: 30 * time.Second,
		Services: map[string]serviceauth.ServicePolicy{
			"pilot-service": {
				Secret: secret, AllowedActors: map[string]struct{}{"resonance-agent": {}},
				AllowAnyMethod: true, AllowAnyTenant: true,
			},
		},
	}, serviceauth.WithVerifierClock(func() time.Time { return now }))
	require.NoError(t, err)
	client := &fakeChatClient{verifier: verifier}
	writer, err := NewFinalMessageWriter(client, signer, "resonance-agent", 1024)
	require.NoError(t, err)

	ack, err := writer.CommitFinalMessage(context.Background(), coordinator.FinalMessageRequest{
		TenantID: "tenant-a", RunID: "run-1", ConversationID: "conversation-a",
		ClientMsgID: "agent:run-1:final", Content: "settled answer",
	})
	require.NoError(t, err)
	require.Equal(t, coordinator.FinalMessageAck{EventID: 7001, SeqID: 9, TimestampMs: 1786155000000}, ack)
	require.NotNil(t, client.request)
	require.Equal(t, "agent:run-1:final", client.request.GetMessage().GetClientMsgId())
	require.Equal(t, "settled answer", client.request.GetMessage().GetContent())
}

type fakeChatClient struct {
	verifier *serviceauth.Verifier
	request  *logicv1.SendEventRequest
}

func (c *fakeChatClient) SendEvent(ctx context.Context, request *logicv1.SendEventRequest, _ ...grpc.CallOption) (*logicv1.SendEventResponse, error) {
	outgoing, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return nil, serviceauth.ErrMissingCredentials
	}
	incoming := metadata.NewIncomingContext(context.Background(), outgoing)
	if _, err := c.verifier.VerifyIncoming(incoming, logicv1.ChatService_SendEvent_FullMethodName, request); err != nil {
		return nil, err
	}
	c.request = request
	return &logicv1.SendEventResponse{EventId: 7001, SeqId: 9, TimestampMs: 1786155000000}, nil
}
