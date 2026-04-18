package ws

import (
	"context"
	"testing"

	"github.com/ceyewan/genesis/clog"
	"github.com/stretchr/testify/require"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
)

type testConn struct {
	username string
	remote   string
	sent     []*gatewayv1.WsPacket
}

func (c *testConn) Send(packet *gatewayv1.WsPacket) error {
	c.sent = append(c.sent, packet)
	return nil
}
func (c *testConn) Close() error       { return nil }
func (c *testConn) Username() string   { return c.username }
func (c *testConn) RemoteAddr() string { return c.remote }

func TestEncodeDecodePacket_RoundTrip(t *testing.T) {
	origin := &gatewayv1.WsPacket{
		ClientSeq: "c1",
		Payload: &gatewayv1.WsPacket_ChatRequest{
			ChatRequest: &gatewayv1.ChatRequest{
				SessionId: "s_1",
				Message: &commonv1.Message{
					Type:    commonv1.MessageType_MESSAGE_TYPE_TEXT,
					Content: "hello",
				},
			},
		},
	}

	raw, err := EncodePacket(origin)
	require.NoError(t, err)
	decoded, err := DecodePacket(raw)
	require.NoError(t, err)
	require.Equal(t, "c1", decoded.GetClientSeq())
	require.Equal(t, "s_1", decoded.GetChatRequest().GetSessionId())
	require.Equal(t, "hello", decoded.GetChatRequest().GetMessage().GetContent())
}

func TestDefaultHandler_Dispatch(t *testing.T) {
	var pulseCalled bool
	var chatSeq string
	var chatSession string
	var ackSeq string

	h := NewDefaultHandler(
		clog.Discard(),
		func(ctx context.Context, conn Connection) error {
			pulseCalled = true
			return nil
		},
		func(ctx context.Context, conn Connection, seq string, chat *gatewayv1.ChatRequest) error {
			chatSeq = seq
			chatSession = chat.GetSessionId()
			return nil
		},
		func(ctx context.Context, conn Connection, ack *gatewayv1.Ack) error {
			ackSeq = ack.GetRefClientSeq()
			return nil
		},
	)

	conn := &testConn{username: "alice", remote: "127.0.0.1"}

	require.NoError(t, h.HandlePacket(context.Background(), conn, &gatewayv1.WsPacket{
		Payload: &gatewayv1.WsPacket_Pulse{Pulse: &gatewayv1.Pulse{}},
	}))
	require.True(t, pulseCalled)

	require.NoError(t, h.HandlePacket(context.Background(), conn, &gatewayv1.WsPacket{
		ClientSeq: "c-2",
		Payload: &gatewayv1.WsPacket_ChatRequest{
			ChatRequest: &gatewayv1.ChatRequest{SessionId: "s_chat"},
		},
	}))
	require.Equal(t, "c-2", chatSeq)
	require.Equal(t, "s_chat", chatSession)

	require.NoError(t, h.HandlePacket(context.Background(), conn, &gatewayv1.WsPacket{
		Payload: &gatewayv1.WsPacket_Ack{
			Ack: &gatewayv1.Ack{RefClientSeq: "c-ack"},
		},
	}))
	require.Equal(t, "c-ack", ackSeq)
}

func TestDefaultHandler_UnknownPayload(t *testing.T) {
	h := NewDefaultHandler(clog.Discard(), nil, nil, nil)
	conn := &testConn{username: "alice", remote: "127.0.0.1"}
	err := h.HandlePacket(context.Background(), conn, &gatewayv1.WsPacket{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown packet type")
}
