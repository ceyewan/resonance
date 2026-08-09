package live_test

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
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/api/gen/go/gateway/v1/gatewayv1connect"
)

// TestAgentServiceDashScope is a paid smoke test of the deployed path rather than a mocked
// Provider: Gateway -> Logic -> Pilot -> isolated Runtime -> DashScope, and
// then back through the durable message and streaming paths.
//
// It is opt-in because it creates a user and consumes real Provider quota:
//
//	RESONANCE_LIVE_AGENT_E2E=1 go test ./test/live -run TestAgentServiceDashScope -v
func TestAgentServiceDashScope(t *testing.T) {
	requireLiveAgentE2E(t)
	agent := newLiveAgent(t, "Agent Live E2E")
	agent.ask(t, "只回复 resonance-agent-e2e-ok，不要添加其他内容。", "resonance-agent-e2e-ok")
	t.Logf("live Agent path passed for %s (model=%s session=%s)",
		agent.username, liveExpectedModel(), agent.sessionID)
}

func TestAgentServiceDashScopeMultiTurnAndTool(t *testing.T) {
	requireLiveAgentE2E(t)
	nickname := fmt.Sprintf("Agent Tool %d", time.Now().UnixNano()%1_000_000)
	agent := newLiveAgent(t, nickname)
	nonce := fmt.Sprintf("MEM-%d", time.Now().UnixNano()%1_000_000_000)

	agent.ask(t, "请记住验证码 "+nonce+"，然后只回复：已记住", "已记住")
	agent.ask(t, "我刚才让你记住的验证码是什么？只回复验证码。", nonce)
	agent.ask(t, "请调用 get_my_profile 查询我的资料，然后只回复我的昵称。不要猜测。", nickname)

	t.Logf("live multi-turn and Tool path passed for %s (model=%s session=%s)",
		agent.username, liveExpectedModel(), agent.sessionID)
}

type liveAgent struct {
	username   string
	sessionID  string
	connection *websocket.Conn
}

func requireLiveAgentE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("RESONANCE_LIVE_AGENT_E2E") != "1" {
		t.Skip("set RESONANCE_LIVE_AGENT_E2E=1 to run the real Provider test")
	}
}

func liveExpectedModel() string {
	if model := os.Getenv("RESONANCE_LIVE_EXPECTED_MODEL"); model != "" {
		return model
	}
	return "configured DashScope model"
}

func newLiveAgent(t *testing.T, nickname string) *liveAgent {
	t.Helper()

	baseURL := strings.TrimRight(os.Getenv("RESONANCE_LIVE_BASE_URL"), "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080"
	}
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil || parsedBaseURL.Hostname() == "" {
		t.Fatalf("parse live base URL: %v", err)
	}
	if parsedBaseURL.Hostname() != "127.0.0.1" && parsedBaseURL.Hostname() != "localhost" && parsedBaseURL.Hostname() != "::1" &&
		os.Getenv("RESONANCE_LIVE_ALLOW_REMOTE") != "1" {
		t.Fatal("refusing paid live E2E against a non-loopback deployment without RESONANCE_LIVE_ALLOW_REMOTE=1")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	username := fmt.Sprintf("agent-e2e-%d", time.Now().UnixNano())
	authClient := gatewayv1connect.NewAuthServiceClient(http.DefaultClient, baseURL)
	registered, err := authClient.Register(ctx, connect.NewRequest(&gatewayv1.RegisterRequest{
		Username: username,
		Password: "Resonance-Live-E2E-2026!",
		Nickname: nickname,
	}))
	if err != nil {
		t.Fatalf("register live user: %v", err)
	}
	if registered.Msg.GetAccessToken() == "" {
		t.Fatal("register response did not include an access token")
	}

	sessionClient := gatewayv1connect.NewSessionServiceClient(http.DefaultClient, baseURL)
	listRequest := connect.NewRequest(&gatewayv1.GetSessionListRequest{})
	listRequest.Header().Set("Authorization", "Bearer "+registered.Msg.GetAccessToken())
	listed, err := sessionClient.GetSessionList(ctx, listRequest)
	if err != nil {
		t.Fatalf("list default sessions: %v", err)
	}

	var agentSessionID string
	for _, session := range listed.Msg.GetSessions() {
		if session.GetKind() == commonv1.SessionKind_SESSION_KIND_AI &&
			session.GetAgentProfile() == commonv1.AgentProfile_AGENT_PROFILE_USER_ASSISTANT &&
			session.GetAgentProfileVersion() > 0 {
			agentSessionID = session.GetSessionId()
			break
		}
	}
	if agentSessionID == "" {
		t.Fatal("registration did not provision a pinned user-assistant Bot session")
	}

	wsURL := parsedBaseURL
	if wsURL.Scheme == "https" {
		wsURL.Scheme = "wss"
	} else {
		wsURL.Scheme = "ws"
	}
	wsURL.Path = "/ws"
	query := wsURL.Query()
	query.Set("token", registered.Msg.GetAccessToken())
	wsURL.RawQuery = query.Encode()

	connection, response, err := websocket.DefaultDialer.DialContext(ctx, wsURL.String(), nil)
	if err != nil {
		if response != nil {
			t.Fatalf("connect websocket: %v (HTTP %d)", err, response.StatusCode)
		}
		t.Fatalf("connect websocket: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	return &liveAgent{username: username, sessionID: agentSessionID, connection: connection}
}

func (agent *liveAgent) ask(t *testing.T, prompt, expected string) string {
	t.Helper()
	if err := agent.connection.SetReadDeadline(time.Now().Add(2 * time.Minute)); err != nil {
		t.Fatalf("set websocket deadline: %v", err)
	}

	clientSequence := fmt.Sprintf("live-%d", time.Now().UnixNano())
	clientMessageID := fmt.Sprintf("live-message-%d", time.Now().UnixNano())
	packet := &gatewayv1.WsPacket{
		ClientSeq: clientSequence,
		Payload: &gatewayv1.WsPacket_ChatRequest{ChatRequest: &gatewayv1.ChatRequest{
			SessionId: agent.sessionID,
			Message: &commonv1.Message{
				Type:        commonv1.MessageType_MESSAGE_TYPE_TEXT,
				Content:     prompt,
				ClientMsgId: clientMessageID,
			},
		}},
	}
	payload, err := proto.Marshal(packet)
	if err != nil {
		t.Fatalf("marshal chat packet: %v", err)
	}
	if err := agent.connection.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatalf("send chat packet: %v", err)
	}

	var ackSeen, beginSeen, endSeen, finalEventSeen bool
	var sourceEventID int64
	var runID, streamID, finalClientMessageID string
	var lastStreamSequence uint64
	var streamed strings.Builder
	for !ackSeen || !beginSeen || !endSeen || !finalEventSeen {
		messageType, data, readErr := agent.connection.ReadMessage()
		if readErr != nil {
			t.Fatalf("read Agent response: %v (ack=%t begin=%t end=%t final=%t streamed=%q)",
				readErr, ackSeen, beginSeen, endSeen, finalEventSeen, streamed.String())
		}
		if messageType != websocket.BinaryMessage {
			continue
		}

		incoming := &gatewayv1.WsPacket{}
		if err := proto.Unmarshal(data, incoming); err != nil {
			t.Fatalf("unmarshal Agent response: %v", err)
		}
		switch value := incoming.GetPayload().(type) {
		case *gatewayv1.WsPacket_Ack:
			if value.Ack.GetRefClientSeq() == clientSequence && value.Ack.GetEventId() > 0 {
				ackSeen = true
				sourceEventID = value.Ack.GetEventId()
			}
		case *gatewayv1.WsPacket_StreamBegin:
			if value.StreamBegin.GetSessionId() == agent.sessionID && value.StreamBegin.GetRunId() != "" &&
				value.StreamBegin.GetStreamId() != "" && value.StreamBegin.GetSourceEventId() == sourceEventID &&
				value.StreamBegin.GetFromUsername() == "resonance-agent" && value.StreamBegin.GetFinalClientMsgId() != "" {
				beginSeen = true
				runID, streamID = value.StreamBegin.GetRunId(), value.StreamBegin.GetStreamId()
				finalClientMessageID = value.StreamBegin.GetFinalClientMsgId()
			}
		case *gatewayv1.WsPacket_StreamChunk:
			if value.StreamChunk.GetSessionId() == agent.sessionID && value.StreamChunk.GetRunId() == runID &&
				value.StreamChunk.GetStreamId() == streamID && value.StreamChunk.GetStreamSequence() > lastStreamSequence {
				lastStreamSequence = value.StreamChunk.GetStreamSequence()
				streamed.WriteString(value.StreamChunk.GetDelta())
			}
		case *gatewayv1.WsPacket_StreamEnd:
			if value.StreamEnd.GetSessionId() == agent.sessionID && value.StreamEnd.GetRunId() == runID &&
				value.StreamEnd.GetStreamId() == streamID && value.StreamEnd.GetStreamSequence() > lastStreamSequence &&
				value.StreamEnd.GetFinalClientMsgId() == finalClientMessageID {
				if value.StreamEnd.GetReason() != gatewayv1.StreamFinishReason_STREAM_FINISH_REASON_STOP {
					t.Fatalf("Agent stream ended unsuccessfully: %s", value.StreamEnd.GetReason())
				}
				endSeen = true
			}
		case *gatewayv1.WsPacket_Event:
			event := value.Event
			message := event.GetMessage()
			if event.GetSessionId() == agent.sessionID && event.GetFromUsername() == "resonance-agent" &&
				message != nil && message.GetClientMsgId() == finalClientMessageID && strings.Contains(message.GetContent(), expected) {
				finalEventSeen = true
			}
		}
	}

	if !strings.Contains(streamed.String(), expected) {
		t.Fatalf("stream did not contain expected model output: %q", streamed.String())
	}
	return streamed.String()
}
