package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/gorilla/websocket"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/api/gen/go/gateway/v1/gatewayv1connect"
	wswire "github.com/ceyewan/resonance/gateway/transport/ws"
)

type distribution struct {
	Samples int     `json:"samples"`
	P50MS   float64 `json:"p50_ms"`
	P95MS   float64 `json:"p95_ms"`
	P99MS   float64 `json:"p99_ms"`
}

type result struct {
	SchemaVersion int          `json:"schema_version"`
	GeneratedAt   string       `json:"generated_at"`
	Seed          int64        `json:"seed"`
	Concurrency   int          `json:"concurrency"`
	MessageCount  int          `json:"message_count"`
	DurationMS    float64      `json:"duration_ms"`
	Throughput    float64      `json:"messages_per_second"`
	Host          string       `json:"host"`
	GoVersion     string       `json:"go_version"`
	Prefix        string       `json:"prefix"`
	AliceUsername string       `json:"alice_username"`
	BobUsername   string       `json:"bob_username"`
	OnlinePush    distribution `json:"online_push"`
	OfflineInbox  distribution `json:"offline_inbox_recovery"`
	WSConnect     distribution `json:"websocket_connect"`
	SuccessRate   float64      `json:"message_delivery_success_rate"`
	WSFailureRate float64      `json:"websocket_connect_failure_rate"`
}

func main() {
	var baseURL, output, requestedPrefix string
	var count int
	flag.StringVar(&baseURL, "base-url", "http://127.0.0.1:8080", "Gateway base URL")
	flag.StringVar(&output, "output", "", "JSON output path")
	flag.IntVar(&count, "count", 20, "fixed online message sample count")
	flag.StringVar(&requestedPrefix, "prefix", "", "unique benchmark prefix")
	flag.Parse()
	if output == "" || count < 1 {
		fatal(fmt.Errorf("output is required and count must be positive"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	auth := gatewayv1connect.NewAuthServiceClient(http.DefaultClient, baseURL)
	sessions := gatewayv1connect.NewSessionServiceClient(http.DefaultClient, baseURL)
	suffix := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	prefix := requestedPrefix
	if prefix == "" {
		prefix = "v1bench_" + suffix
	}
	if !validPrefix(prefix) {
		fatal(fmt.Errorf("prefix must contain only letters, numbers, underscores, and hyphens"))
	}
	alice, aliceToken := register(ctx, auth, prefix+"-a")
	bob, bobToken := register(ctx, auth, prefix+"-b")

	request := connect.NewRequest(&gatewayv1.CreateSessionRequest{Members: []string{bob}, Type: commonv1.SessionType_SESSION_TYPE_DIRECT})
	request.Header().Set("Authorization", "Bearer "+aliceToken)
	created, err := sessions.CreateSession(ctx, request)
	if err != nil {
		fatal(err)
	}

	aliceWS, aliceConnect := dialWS(baseURL, aliceToken)
	bobWS, bobConnect := dialWS(baseURL, bobToken)
	wsSamples := []time.Duration{aliceConnect, bobConnect}

	benchmarkStarted := time.Now()
	online := make([]time.Duration, 0, count)
	succeeded := 0
	for i := 0; i < count; i++ {
		clientSeq := fmt.Sprintf("bench-%s-%d", suffix, i)
		clientMessageID := fmt.Sprintf("%s-message-%d", prefix, i)
		packet := &gatewayv1.WsPacket{ClientSeq: clientSeq, Payload: &gatewayv1.WsPacket_ChatRequest{ChatRequest: &gatewayv1.ChatRequest{SessionId: created.Msg.GetSessionId(), Message: &commonv1.Message{Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: "local-v1-benchmark", ClientMsgId: clientMessageID}}}}
		data, err := wswire.EncodePacket(packet)
		if err != nil {
			fatal(err)
		}
		started := time.Now()
		if err := aliceWS.WriteMessage(websocket.BinaryMessage, data); err != nil {
			fatal(err)
		}
		if waitForEvent(bobWS, clientMessageID, 10*time.Second) {
			online = append(online, time.Since(started))
			succeeded++
		}
	}

	if err := bobWS.Close(); err != nil {
		fatal(err)
	}
	offlineStart := time.Now()
	offlineClientSequence := "offline-" + suffix
	offlineClientMessageID := prefix + "-offline-message"
	offlinePacket := &gatewayv1.WsPacket{ClientSeq: offlineClientSequence, Payload: &gatewayv1.WsPacket_ChatRequest{ChatRequest: &gatewayv1.ChatRequest{SessionId: created.Msg.GetSessionId(), Message: &commonv1.Message{Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: "local-v1-offline-benchmark", ClientMsgId: offlineClientMessageID}}}}
	data, err := wswire.EncodePacket(offlinePacket)
	if err != nil {
		fatal(err)
	}
	if err := aliceWS.WriteMessage(websocket.BinaryMessage, data); err != nil {
		fatal(err)
	}
	offlineEventID := waitForAck(aliceWS, offlineClientSequence, 10*time.Second)
	bobReconnected, reconnectDuration := dialWS(baseURL, bobToken)
	wsSamples = append(wsSamples, reconnectDuration)
	if err := bobReconnected.Close(); err != nil {
		fatal(err)
	}
	if !waitForInboxEvent(ctx, sessions, bobToken, offlineEventID, 10*time.Second) {
		fatal(fmt.Errorf("offline event %d was not recovered from Inbox", offlineEventID))
	}
	if err := aliceWS.Close(); err != nil {
		fatal(err)
	}

	host, _ := os.Hostname()
	duration := time.Since(benchmarkStarted)
	report := result{SchemaVersion: 1, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Seed: 20260809, Concurrency: 1, MessageCount: count,
		DurationMS: float64(duration.Microseconds()) / 1000, Throughput: float64(succeeded) / duration.Seconds(), Host: host, GoVersion: runtime.Version(),
		Prefix: prefix, AliceUsername: alice, BobUsername: bob, OnlinePush: summarize(online), OfflineInbox: summarize([]time.Duration{time.Since(offlineStart)}),
		WSConnect: summarize(wsSamples), SuccessRate: float64(succeeded) / float64(count), WSFailureRate: 0}
	f, err := os.Create(output)
	if err != nil {
		fatal(err)
	}
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		_ = f.Close()
		fatal(err)
	}
	if err := f.Close(); err != nil {
		fatal(err)
	}
	if report.SuccessRate != 1 {
		fatal(fmt.Errorf("delivery success rate %.3f", report.SuccessRate))
	}
}

func register(ctx context.Context, client gatewayv1connect.AuthServiceClient, username string) (string, string) {
	resp, err := client.Register(ctx, connect.NewRequest(&gatewayv1.RegisterRequest{Username: username, Password: "V1-benchmark-pass-2026!", Nickname: username}))
	if err != nil {
		fatal(err)
	}
	return resp.Msg.GetUser().GetUsername(), resp.Msg.GetAccessToken()
}

func dialWS(baseURL, token string) (*websocket.Conn, time.Duration) {
	u, err := url.Parse(baseURL)
	if err != nil {
		fatal(err)
	}
	u.Scheme = map[bool]string{true: "wss", false: "ws"}[u.Scheme == "https"]
	u.Path = "/ws"
	query := u.Query()
	query.Set("token", token)
	u.RawQuery = query.Encode()
	started := time.Now()
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		fatal(err)
	}
	return conn, time.Since(started)
}

func waitForEvent(conn *websocket.Conn, clientMessageID string, timeout time.Duration) bool {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return false
		}
		if kind != websocket.BinaryMessage {
			continue
		}
		packet, err := wswire.DecodePacket(data)
		if err == nil && packet.GetEvent().GetMessage().GetClientMsgId() == clientMessageID {
			return true
		}
	}
}

func waitForAck(conn *websocket.Conn, clientSequence string, timeout time.Duration) int64 {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			fatal(err)
		}
		if kind != websocket.BinaryMessage {
			continue
		}
		packet, decodeErr := wswire.DecodePacket(data)
		if decodeErr == nil && packet.GetAck().GetRefClientSeq() == clientSequence && packet.GetAck().GetEventId() > 0 {
			return packet.GetAck().GetEventId()
		}
	}
}

func waitForInboxEvent(ctx context.Context, sessions gatewayv1connect.SessionServiceClient, token string, eventID int64, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pull := connect.NewRequest(&gatewayv1.PullInboxDeltaRequest{Limit: 100})
		pull.Header().Set("Authorization", "Bearer "+token)
		response, err := sessions.PullInboxDelta(ctx, pull)
		if err == nil {
			for _, item := range response.Msg.GetEvents() {
				if item.GetEvent().GetEventId() == eventID {
					return true
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func validPrefix(value string) bool {
	if value == "" || len(value) > 80 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func summarize(values []time.Duration) distribution {
	slices.Sort(values)
	percentile := func(p float64) float64 {
		if len(values) == 0 {
			return 0
		}
		index := int(float64(len(values)-1)*p + 0.5)
		return float64(values[index].Microseconds()) / 1000
	}
	return distribution{Samples: len(values), P50MS: percentile(.50), P95MS: percentile(.95), P99MS: percentile(.99)}
}

func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
