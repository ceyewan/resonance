package live_test

import (
	"context"
	"encoding/json"
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

// TestAgentServiceDeterministicCompose exercises the real deployed Agent/Pilot
// path with the loopback Provider from deploy/services.local.yaml. It uses no
// cloud endpoint or paid model credential.
func TestAgentServiceDeterministicCompose(t *testing.T) {
	requireDeterministicAgentE2E(t)

	ordinary := newLiveAgent(t, "Deterministic Tool E2E")
	ordinary.ask(t, "[deterministic:get_my_profile]", "resonance-deterministic-tool-complete")
	if ordinary.lastRunID == "" {
		t.Fatal("deterministic Tool run did not expose a run identity")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	baseURL := liveBaseURL(t)
	authClient := gatewayv1connect.NewAuthServiceClient(http.DefaultClient, baseURL)
	sessionClient := gatewayv1connect.NewSessionServiceClient(http.DefaultClient, baseURL)
	targetUsername := e2eUsername("agent-e2e-target")
	targetPassword := "Resonance-Deterministic-E2E-2026!"
	target, err := authClient.Register(ctx, connect.NewRequest(&gatewayv1.RegisterRequest{
		Username: targetUsername, Password: targetPassword, Nickname: "Deterministic Mutation Target",
	}))
	if err != nil {
		t.Fatalf("register mutation target: %v", err)
	}
	if _, err := authClient.Login(ctx, connect.NewRequest(&gatewayv1.LoginRequest{
		Username: targetUsername, Password: targetPassword, TenantId: "default",
	})); err != nil {
		t.Fatalf("new mutation target must be active before approval: %v", err)
	}
	unauthorizedSession := connect.NewRequest(&gatewayv1.CreateAgentSessionRequest{Profile: commonv1.AgentProfile_AGENT_PROFILE_IAM_ADMIN})
	unauthorizedSession.Header().Set("Authorization", "Bearer "+target.Msg.GetAccessToken())
	if _, err := sessionClient.CreateAgentSession(ctx, unauthorizedSession); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("ordinary user IAM Agent session must be denied, got: %v", err)
	}

	adminUsername := os.Getenv("RESONANCE_E2E_ADMIN_USERNAME")
	adminPassword := os.Getenv("RESONANCE_E2E_ADMIN_PASSWORD")
	requesterUsername := os.Getenv("RESONANCE_E2E_IAM_REQUESTER_USERNAME")
	requesterPassword := os.Getenv("RESONANCE_E2E_IAM_REQUESTER_PASSWORD")
	if adminUsername == "" || adminPassword == "" || requesterUsername == "" || requesterPassword == "" {
		t.Fatal("deterministic IAM E2E requires separate requester and approver credentials")
	}
	requesterLogin, err := authClient.Login(ctx, connect.NewRequest(&gatewayv1.LoginRequest{
		Username: requesterUsername, Password: requesterPassword, TenantId: "default",
	}))
	if err != nil {
		t.Fatalf("login temporary IAM requester: %v", err)
	}
	requesterToken := requesterLogin.Msg.GetAccessToken()
	createAdminSession := connect.NewRequest(&gatewayv1.CreateAgentSessionRequest{Profile: commonv1.AgentProfile_AGENT_PROFILE_IAM_ADMIN})
	createAdminSession.Header().Set("Authorization", "Bearer "+requesterToken)
	requesterSession, err := sessionClient.CreateAgentSession(ctx, createAdminSession)
	if err != nil {
		t.Fatalf("create requester IAM Agent session: %v", err)
	}
	mutationAgent := connectLiveAgent(t, requesterUsername, requesterToken, requesterSession.Msg.GetSessionId())
	mutationStarted := time.Now()
	mutationAgent.ask(t,
		fmt.Sprintf("[deterministic:set_tenant_member_status username=%s status=DISABLED]", targetUsername),
		"resonance-deterministic-tool-complete",
	)

	adminLogin, err := authClient.Login(ctx, connect.NewRequest(&gatewayv1.LoginRequest{
		Username: adminUsername, Password: adminPassword, TenantId: "default",
	}))
	if err != nil {
		t.Fatalf("login bootstrap administrator: %v", err)
	}
	adminToken := adminLogin.Msg.GetAccessToken()

	approvalClient := gatewayv1connect.NewAgentApprovalServiceClient(http.DefaultClient, baseURL)
	listRequest := connect.NewRequest(&gatewayv1.ListApprovalsRequest{Status: gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_PENDING, PageSize: 100})
	listRequest.Header().Set("Authorization", "Bearer "+adminToken)
	var approval *gatewayv1.AgentApproval
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && approval == nil {
		listed, listErr := approvalClient.ListApprovals(ctx, listRequest)
		if listErr == nil {
			for _, candidate := range listed.Msg.GetApprovals() {
				if candidate.GetRunId() == mutationAgent.lastRunID && candidate.GetToolName() == "set_tenant_member_status" {
					approval = candidate
					break
				}
			}
		}
		if approval == nil {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if approval == nil || approval.GetRequesterId() != requesterUsername || approval.GetArgsHash() == "" {
		t.Fatal("mutation Tool did not create a bound persistent approval")
	}
	decideRequest := connect.NewRequest(&gatewayv1.DecideApprovalRequest{
		CallId: approval.GetCallId(), ArgsHash: approval.GetArgsHash(), ExpectedVersion: approval.GetVersion(),
		Decision: gatewayv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_APPROVE,
		Reason:   "local deterministic E2E",
	})
	decideRequest.Header().Set("Authorization", "Bearer "+adminToken)
	decided, err := approvalClient.DecideApproval(ctx, decideRequest)
	if err != nil || !decided.Msg.GetChanged() || decided.Msg.GetApproval().GetStatus() != gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_APPROVED {
		t.Fatalf("approve deterministic mutation: response=%v error=%v", decided, err)
	}

	loginTarget := func() error {
		_, loginErr := authClient.Login(ctx, connect.NewRequest(&gatewayv1.LoginRequest{
			Username: targetUsername, Password: targetPassword, TenantId: "default",
		}))
		return loginErr
	}
	for deadline := time.Now().Add(10 * time.Second); ; {
		loginErr := loginTarget()
		if connect.CodeOf(loginErr) == connect.CodePermissionDenied || connect.CodeOf(loginErr) == connect.CodeUnauthenticated {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("approved mutation did not disable target membership: %v", loginErr)
		}
		time.Sleep(100 * time.Millisecond)
	}
	approvedMutationDuration := time.Since(mutationStarted)

	mutationAgent.ask(t,
		fmt.Sprintf("[deterministic:set_tenant_member_status username=%s status=ACTIVE]", targetUsername),
		"resonance-deterministic-tool-complete",
	)
	rejectedListRequest := connect.NewRequest(&gatewayv1.ListApprovalsRequest{Status: gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_PENDING, PageSize: 100})
	rejectedListRequest.Header().Set("Authorization", "Bearer "+adminToken)
	var rejectedApproval *gatewayv1.AgentApproval
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && rejectedApproval == nil; {
		listed, listErr := approvalClient.ListApprovals(ctx, rejectedListRequest)
		if listErr == nil {
			for _, candidate := range listed.Msg.GetApprovals() {
				if candidate.GetRunId() == mutationAgent.lastRunID && candidate.GetToolName() == "set_tenant_member_status" {
					rejectedApproval = candidate
					break
				}
			}
		}
		if rejectedApproval == nil {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if rejectedApproval == nil {
		t.Fatal("rejection path did not create a pending approval")
	}
	rejectRequest := connect.NewRequest(&gatewayv1.DecideApprovalRequest{
		CallId: rejectedApproval.GetCallId(), ArgsHash: rejectedApproval.GetArgsHash(), ExpectedVersion: rejectedApproval.GetVersion(),
		Decision: gatewayv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_REJECT,
		Reason:   "local deterministic rejection E2E",
	})
	rejectRequest.Header().Set("Authorization", "Bearer "+adminToken)
	rejected, err := approvalClient.DecideApproval(ctx, rejectRequest)
	if err != nil || !rejected.Msg.GetChanged() || rejected.Msg.GetApproval().GetStatus() != gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REJECTED {
		t.Fatalf("reject deterministic mutation: response=%v error=%v", rejected, err)
	}
	if loginErr := loginTarget(); connect.CodeOf(loginErr) != connect.CodePermissionDenied && connect.CodeOf(loginErr) != connect.CodeUnauthenticated {
		t.Fatalf("rejected mutation unexpectedly re-enabled target membership: %v", loginErr)
	}
	// Run quota-consuming failures last so their retries cannot starve the
	// successful Tool and approval paths in the same deterministic E2E.
	runtimeFailureAgent := newLiveAgent(t, "Deterministic Runtime Failure E2E")
	runtimeFailureRunID := runtimeFailureAgent.startFailure(t, "[deterministic:runtime_failure]")
	timeoutAgent := newLiveAgent(t, "Deterministic Timeout E2E")
	timeoutRunID := timeoutAgent.startFailure(t, "[deterministic:timeout]")
	writeDeterministicAgentReport(t, deterministicAgentReport{
		SchemaVersion: 1, GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		ReadToolRunID: ordinary.lastRunID, MutationRunID: mutationAgent.lastRunID, ApprovalCallID: approval.GetCallId(),
		RejectedApprovalCallID: rejectedApproval.GetCallId(), RuntimeFailureRunID: runtimeFailureRunID,
		TimeoutRunID: timeoutRunID,
		FirstTokenMS: durationMS(ordinary.lastFirstTokenDuration), ReadToolRunMS: durationMS(ordinary.lastRunDuration),
		ApprovalMutationMS: durationMS(approvedMutationDuration),
	})
	t.Logf("deterministic Agent E2E passed: tool_run=%s mutation_run=%s approval=%s target=%s",
		ordinary.lastRunID, mutationAgent.lastRunID, approval.GetCallId(), targetUsername)
}

type liveAgent struct {
	username               string
	sessionID              string
	token                  string
	connection             *websocket.Conn
	lastRunID              string
	lastFirstTokenDuration time.Duration
	lastRunDuration        time.Duration
}

type deterministicAgentReport struct {
	SchemaVersion          int     `json:"schema_version"`
	GeneratedAt            string  `json:"generated_at"`
	ReadToolRunID          string  `json:"read_tool_run_id"`
	MutationRunID          string  `json:"mutation_run_id"`
	ApprovalCallID         string  `json:"approval_call_id"`
	RejectedApprovalCallID string  `json:"rejected_approval_call_id"`
	RuntimeFailureRunID    string  `json:"runtime_failure_run_id"`
	TimeoutRunID           string  `json:"timeout_run_id"`
	FirstTokenMS           float64 `json:"first_token_ms"`
	ReadToolRunMS          float64 `json:"read_tool_run_ms"`
	ApprovalMutationMS     float64 `json:"approval_mutation_ms"`
}

func requireLiveAgentE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("RESONANCE_LIVE_AGENT_E2E") != "1" {
		t.Skip("set RESONANCE_LIVE_AGENT_E2E=1 to run the real Provider test")
	}
}

func requireDeterministicAgentE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("RESONANCE_DETERMINISTIC_AGENT_E2E") != "1" {
		t.Skip("set RESONANCE_DETERMINISTIC_AGENT_E2E=1 to run the local deterministic Compose Agent test")
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
	baseURL := liveBaseURL(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	username := e2eUsername("agent-e2e")
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

	return connectLiveAgent(t, username, registered.Msg.GetAccessToken(), agentSessionID)
}

func e2eUsername(kind string) string {
	prefix := strings.TrimSpace(os.Getenv("RESONANCE_E2E_PREFIX"))
	if prefix == "" {
		prefix = kind
	}
	return fmt.Sprintf("%s-%s-%d", prefix, kind, time.Now().UnixNano())
}

func liveBaseURL(t *testing.T) string {
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
		t.Fatal("refusing Agent E2E against a non-loopback deployment without RESONANCE_LIVE_ALLOW_REMOTE=1")
	}
	return baseURL
}

func connectLiveAgent(t *testing.T, username, token, sessionID string) *liveAgent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	parsedBaseURL, err := url.Parse(liveBaseURL(t))
	if err != nil {
		t.Fatalf("parse live base URL: %v", err)
	}
	wsURL := *parsedBaseURL
	if wsURL.Scheme == "https" {
		wsURL.Scheme = "wss"
	} else {
		wsURL.Scheme = "ws"
	}
	wsURL.Path = "/ws"
	query := wsURL.Query()
	query.Set("token", token)
	wsURL.RawQuery = query.Encode()

	connection, response, err := websocket.DefaultDialer.DialContext(ctx, wsURL.String(), nil)
	if err != nil {
		if response != nil {
			t.Fatalf("connect websocket: %v (HTTP %d)", err, response.StatusCode)
		}
		t.Fatalf("connect websocket: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	return &liveAgent{username: username, sessionID: sessionID, token: token, connection: connection}
}

func (agent *liveAgent) ask(t *testing.T, prompt, expected string) string {
	t.Helper()
	startedAt := time.Now()
	agent.lastFirstTokenDuration = 0
	agent.lastRunDuration = 0
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
				if agent.lastFirstTokenDuration == 0 {
					agent.lastFirstTokenDuration = time.Since(startedAt)
				}
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

	agent.lastRunID = runID
	agent.lastRunDuration = time.Since(startedAt)
	return streamed.String()
}

func (agent *liveAgent) startFailure(t *testing.T, prompt string) string {
	t.Helper()
	if err := agent.connection.SetReadDeadline(time.Now().Add(2 * time.Minute)); err != nil {
		t.Fatalf("set websocket deadline: %v", err)
	}
	clientSequence := fmt.Sprintf("failure-%d", time.Now().UnixNano())
	packet := &gatewayv1.WsPacket{
		ClientSeq: clientSequence,
		Payload: &gatewayv1.WsPacket_ChatRequest{ChatRequest: &gatewayv1.ChatRequest{
			SessionId: agent.sessionID,
			Message: &commonv1.Message{Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: prompt,
				ClientMsgId: fmt.Sprintf("failure-message-%d", time.Now().UnixNano())},
		}},
	}
	payload, err := proto.Marshal(packet)
	if err != nil {
		t.Fatalf("marshal failure-path packet: %v", err)
	}
	if err := agent.connection.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatalf("send failure-path packet: %v", err)
	}
	var ackSeen bool
	var sourceEventID int64
	var runID string
	for {
		messageType, data, readErr := agent.connection.ReadMessage()
		if readErr != nil {
			t.Fatalf("read Agent failure response: %v (ack=%t run=%q)", readErr, ackSeen, runID)
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		incoming := &gatewayv1.WsPacket{}
		if err := proto.Unmarshal(data, incoming); err != nil {
			t.Fatalf("unmarshal Agent failure response: %v", err)
		}
		switch value := incoming.GetPayload().(type) {
		case *gatewayv1.WsPacket_Ack:
			if value.Ack.GetRefClientSeq() == clientSequence && value.Ack.GetEventId() > 0 {
				ackSeen = true
				sourceEventID = value.Ack.GetEventId()
			}
		case *gatewayv1.WsPacket_StreamBegin:
			if ackSeen && value.StreamBegin.GetSourceEventId() == sourceEventID {
				runID := value.StreamBegin.GetRunId()
				if runID != "" {
					return runID
				}
			}
		}
	}
}

func writeDeterministicAgentReport(t *testing.T, report deterministicAgentReport) {
	t.Helper()
	path := strings.TrimSpace(os.Getenv("RESONANCE_AGENT_E2E_REPORT"))
	if path == "" {
		return
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create deterministic Agent report: %v", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		_ = file.Close()
		t.Fatalf("encode deterministic Agent report: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close deterministic Agent report: %v", err)
	}
}

func durationMS(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000
}
