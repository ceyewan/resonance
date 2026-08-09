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
	Host          string       `json:"host"`
	GoVersion     string       `json:"go_version"`
	OnlinePush    distribution `json:"online_push"`
	OfflineInbox  distribution `json:"offline_inbox_recovery"`
	WSConnect     distribution `json:"websocket_connect"`
	SuccessRate   float64      `json:"message_delivery_success_rate"`
}

func main() {
	var baseURL, output string
	var count int
	flag.StringVar(&baseURL, "base-url", "http://127.0.0.1:8080", "Gateway base URL")
	flag.StringVar(&output, "output", "", "JSON output path")
	flag.IntVar(&count, "count", 20, "fixed online message sample count")
	flag.Parse()
	if output == "" || count < 1 {
		fatal(fmt.Errorf("output is required and count must be positive"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	auth := gatewayv1connect.NewAuthServiceClient(http.DefaultClient, baseURL)
	sessions := gatewayv1connect.NewSessionServiceClient(http.DefaultClient, baseURL)
	suffix := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	alice, aliceToken := register(ctx, auth, "v1bench_a_"+suffix)
	bob, bobToken := register(ctx, auth, "v1bench_b_"+suffix)
	_ = alice

	request := connect.NewRequest(&gatewayv1.CreateSessionRequest{Members: []string{bob}, Type: commonv1.SessionType_SESSION_TYPE_DIRECT})
	request.Header().Set("Authorization", "Bearer "+aliceToken)
	created, err := sessions.CreateSession(ctx, request)
	if err != nil {
		fatal(err)
	}

	aliceWS, aliceConnect := dialWS(baseURL, aliceToken)
	bobWS, bobConnect := dialWS(baseURL, bobToken)
	wsSamples := []time.Duration{aliceConnect, bobConnect}

	online := make([]time.Duration, 0, count)
	succeeded := 0
	for i := 0; i < count; i++ {
		clientSeq := fmt.Sprintf("bench-%s-%d", suffix, i)
		packet := &gatewayv1.WsPacket{ClientSeq: clientSeq, Payload: &gatewayv1.WsPacket_ChatRequest{ChatRequest: &gatewayv1.ChatRequest{SessionId: created.Msg.GetSessionId(), Message: &commonv1.Message{Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: "local-v1-benchmark"}}}}
		data, err := wswire.EncodePacket(packet)
		if err != nil {
			fatal(err)
		}
		started := time.Now()
		if err := aliceWS.WriteMessage(websocket.BinaryMessage, data); err != nil {
			fatal(err)
		}
		if waitForEvent(bobWS, 10*time.Second) {
			online = append(online, time.Since(started))
			succeeded++
		}
	}

	if err := bobWS.Close(); err != nil {
		fatal(err)
	}
	offlineStart := time.Now()
	offlinePacket := &gatewayv1.WsPacket{ClientSeq: "offline-" + suffix, Payload: &gatewayv1.WsPacket_ChatRequest{ChatRequest: &gatewayv1.ChatRequest{SessionId: created.Msg.GetSessionId(), Message: &commonv1.Message{Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: "local-v1-offline-benchmark"}}}}
	data, err := wswire.EncodePacket(offlinePacket)
	if err != nil {
		fatal(err)
	}
	if err := aliceWS.WriteMessage(websocket.BinaryMessage, data); err != nil {
		fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	pull := connect.NewRequest(&gatewayv1.PullInboxDeltaRequest{Limit: 100})
	pull.Header().Set("Authorization", "Bearer "+bobToken)
	if _, err := sessions.PullInboxDelta(ctx, pull); err != nil {
		fatal(err)
	}
	if err := aliceWS.Close(); err != nil {
		fatal(err)
	}

	host, _ := os.Hostname()
	report := result{SchemaVersion: 1, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Seed: 20260809, Concurrency: 1, MessageCount: count, Host: host, GoVersion: runtime.Version(), OnlinePush: summarize(online), OfflineInbox: summarize([]time.Duration{time.Since(offlineStart)}), WSConnect: summarize(wsSamples), SuccessRate: float64(succeeded) / float64(count)}
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

func waitForEvent(conn *websocket.Conn, timeout time.Duration) bool {
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
		if err == nil && packet.GetEvent() != nil {
			return true
		}
	}
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
