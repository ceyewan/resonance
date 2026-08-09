// Package logicclient contains Pilot-specific, narrow Logic service adapters.
package logicclient

import (
	"context"
	"fmt"
	"unicode/utf8"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/pilot/coordinator"
	"github.com/ceyewan/resonance/pkg/serviceauth"
)

type ContextAuthenticator interface {
	AuthenticateServiceCall(ctx context.Context, tenantID, actorUsername, fullMethod, payloadHash string) (context.Context, error)
}

type FinalMessageWriter struct {
	client        logicv1.ChatServiceClient
	authenticator ContextAuthenticator
	botUsername   string
	maxBytes      int
}

func NewFinalMessageWriter(
	client logicv1.ChatServiceClient,
	authenticator ContextAuthenticator,
	botUsername string,
	maxBytes int,
) (*FinalMessageWriter, error) {
	if client == nil || authenticator == nil || botUsername == "" || maxBytes < 1 {
		return nil, fmt.Errorf("final message writer configuration is incomplete")
	}
	return &FinalMessageWriter{client: client, authenticator: authenticator, botUsername: botUsername, maxBytes: maxBytes}, nil
}

func (w *FinalMessageWriter) CommitFinalMessage(ctx context.Context, request coordinator.FinalMessageRequest) (coordinator.FinalMessageAck, error) {
	if request.TenantID == "" || request.RunID == "" || request.ConversationID == "" || request.Content == "" {
		return coordinator.FinalMessageAck{}, fmt.Errorf("final message request is incomplete")
	}
	expectedClientID := "agent:" + request.RunID + ":final"
	if request.ClientMsgID != expectedClientID || len(request.ClientMsgID) > 64 {
		return coordinator.FinalMessageAck{}, fmt.Errorf("invalid final message idempotency key")
	}
	if len(request.Content) > w.maxBytes || !utf8.ValidString(request.Content) {
		return coordinator.FinalMessageAck{}, fmt.Errorf("final message is oversized or invalid UTF-8")
	}
	logicRequest := &logicv1.SendEventRequest{
		SessionId: request.ConversationID,
		Payload: &logicv1.SendEventRequest_Message{Message: &commonv1.Message{
			Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: request.Content, ClientMsgId: request.ClientMsgID,
		}},
	}
	payloadHash, err := serviceauth.PayloadHash(logicRequest)
	if err != nil {
		return coordinator.FinalMessageAck{}, fmt.Errorf("hash final message request: %w", err)
	}
	authenticated, err := w.authenticator.AuthenticateServiceCall(
		ctx, request.TenantID, w.botUsername, logicv1.ChatService_SendEvent_FullMethodName, payloadHash,
	)
	if err != nil {
		return coordinator.FinalMessageAck{}, fmt.Errorf("authenticate final message call: %w", err)
	}
	response, err := w.client.SendEvent(authenticated, logicRequest)
	if err != nil {
		return coordinator.FinalMessageAck{}, err
	}
	if response.EventId <= 0 || response.SeqId <= 0 || response.TimestampMs <= 0 {
		return coordinator.FinalMessageAck{}, fmt.Errorf("logic returned an incomplete final message acknowledgement")
	}
	return coordinator.FinalMessageAck{EventID: response.EventId, SeqID: response.SeqId, TimestampMs: response.TimestampMs}, nil
}
