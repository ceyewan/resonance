// Package live contains opt-in paid smoke tests, never part of the default test run.
package live

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/api/gen/go/gateway/v1/gatewayv1connect"
)

func TestAgentServiceDashScope(t *testing.T) {
	if os.Getenv("RESONANCE_LIVE_AGENT_E2E") != "1" {
		t.Skip("requires a local deployment and paid Provider access")
	}
	base := strings.TrimRight(os.Getenv("RESONANCE_LIVE_BASE_URL"), "/")
	if base == "" {
		base = "http://127.0.0.1:8080"
	}
	endpoint, err := url.Parse(base)
	require.NoError(t, err)
	require.Contains(t, []string{"127.0.0.1", "localhost", "::1"}, endpoint.Hostname(), "paid smoke tests must target a local deployment")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	auth := gatewayv1connect.NewAuthServiceClient(http.DefaultClient, base)
	registered, err := auth.Register(ctx, connect.NewRequest(&gatewayv1.RegisterRequest{Username: fmt.Sprintf("pilot-smoke-%d", time.Now().UnixNano()), Password: "Resonance-Smoke-2026!", Nickname: "Pilot smoke"}))
	require.NoError(t, err)
	token := registered.Msg.AccessToken
	sessions := gatewayv1connect.NewSessionServiceClient(http.DefaultClient, base)
	open := func() string {
		request := connect.NewRequest(&gatewayv1.CreateAgentSessionRequest{})
		request.Header().Set("Authorization", "Bearer "+token)
		response, e := sessions.CreateAgentSession(ctx, request)
		require.NoError(t, e)
		return response.Msg.SessionId
	}
	conversation := open()
	require.Equal(t, conversation, open(), "opening Pilot must reuse the same conversation")
	endpoint.Scheme = "ws"
	if strings.HasPrefix(base, "https:") {
		endpoint.Scheme = "wss"
	}
	endpoint.Path = "/ws"
	query := endpoint.Query()
	query.Set("token", token)
	endpoint.RawQuery = query.Encode()
	socket, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint.String(), nil)
	require.NoError(t, err)
	defer socket.Close()
	ask := func(prompt string) string {
		id := fmt.Sprintf("smoke-%d", time.Now().UnixNano())
		data, e := proto.Marshal(&gatewayv1.WsPacket{ClientSeq: id, Payload: &gatewayv1.WsPacket_ChatRequest{ChatRequest: &gatewayv1.ChatRequest{SessionId: conversation, Message: &commonv1.Message{Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: prompt, ClientMsgId: id}}}})
		require.NoError(t, e)
		require.NoError(t, socket.WriteMessage(websocket.BinaryMessage, data))
		require.NoError(t, socket.SetReadDeadline(time.Now().Add(90*time.Second)))
		for {
			_, data, e := socket.ReadMessage()
			require.NoError(t, e)
			packet := &gatewayv1.WsPacket{}
			require.NoError(t, proto.Unmarshal(data, packet))
			event := packet.GetEvent()
			if event != nil && event.SessionId == conversation && event.FromUsername == "resonance-agent" && event.GetMessage() != nil {
				return event.GetMessage().Content
			}
		}
	}
	require.NotEmpty(t, ask("请记住我的暗号是 pine-742，回复确认。"))
	require.Contains(t, ask("刚才我的暗号是什么？只回复暗号。"), "pine-742")
}
