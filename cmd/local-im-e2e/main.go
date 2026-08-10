package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/api/gen/go/gateway/v1/gatewayv1connect"
)

const e2ePassword = "Resonance-Local-IM-E2E-2026!"

type report struct {
	SchemaVersion         int    `json:"schema_version"`
	GeneratedAt           string `json:"generated_at"`
	Prefix                string `json:"prefix"`
	AliceUsername         string `json:"alice_username"`
	BobUsername           string `json:"bob_username"`
	SessionID             string `json:"session_id"`
	MessageEventID        int64  `json:"message_event_id"`
	MessageSeqID          int64  `json:"message_seq_id"`
	DuplicateEventID      int64  `json:"duplicate_event_id"`
	EditEventID           int64  `json:"edit_event_id"`
	ReadReceiptSeqID      int64  `json:"read_receipt_seq_id"`
	OfflineMessageEvent   int64  `json:"offline_message_event_id"`
	OfflineMessageSeqID   int64  `json:"offline_message_seq_id"`
	RecallEventID         int64  `json:"recall_event_id"`
	InboxRecoveryVerified bool   `json:"inbox_recovery_verified"`
	MultiDeviceReadSeen   bool   `json:"multi_device_read_seen"`
	IdempotencyVerified   bool   `json:"idempotency_verified"`
}

func main() {
	var baseURL, output, requestedPrefix string
	flag.StringVar(&baseURL, "base-url", "http://127.0.0.1:8080", "Gateway base URL")
	flag.StringVar(&output, "output", "", "machine-readable report path")
	flag.StringVar(&requestedPrefix, "prefix", "", "unique test prefix (generated when empty)")
	flag.Parse()
	if output == "" {
		fatal(fmt.Errorf("output is required"))
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1") {
		fatal(fmt.Errorf("local IM E2E requires a loopback Gateway URL"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	auth := gatewayv1connect.NewAuthServiceClient(http.DefaultClient, baseURL)
	sessions := gatewayv1connect.NewSessionServiceClient(http.DefaultClient, baseURL)
	prefix := requestedPrefix
	if prefix == "" {
		prefix = fmt.Sprintf("local-e2e-%d", time.Now().UTC().UnixNano())
	}
	if !validPrefix(prefix) {
		fatal(fmt.Errorf("prefix must contain only letters, numbers, underscores, and hyphens"))
	}
	alice, aliceToken := register(ctx, auth, prefix+"-alice", "Local E2E Alice")
	bob, bobToken := register(ctx, auth, prefix+"-bob", "Local E2E Bob")

	create := authenticated(connect.NewRequest(&gatewayv1.CreateSessionRequest{
		Members: []string{bob}, Type: commonv1.SessionType_SESSION_TYPE_DIRECT,
	}), aliceToken)
	created, err := sessions.CreateSession(ctx, create)
	if err != nil {
		fatal(fmt.Errorf("create direct session: %w", err))
	}
	sessionID := created.Msg.GetSessionId()
	if sessionID == "" {
		fatal(fmt.Errorf("created session has no identity"))
	}
	step("connect three WebSocket devices")

	alicePrimary := dialWS(baseURL, aliceToken)
	defer closeWS(alicePrimary)
	aliceSecondary := dialWS(baseURL, aliceToken)
	defer closeWS(aliceSecondary)
	bobPrimary := dialWS(baseURL, bobToken)

	clientMessageID := prefix + "-message"
	step("message Ack and online push")
	message := &commonv1.Message{Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: "local IM E2E message", ClientMsgId: clientMessageID}
	messageAck := sendAndAck(alicePrimary, prefix+"-send", &gatewayv1.ChatRequest{SessionId: sessionID, Message: message})
	requireEvent(bobPrimary, messageAck.GetEventId(), func(event *commonv1.ChatEvent) bool {
		return event.GetSeqId() == messageAck.GetSeqId() && event.GetMessage().GetContent() == message.GetContent()
	})

	duplicateAck := sendAndAck(alicePrimary, prefix+"-duplicate", &gatewayv1.ChatRequest{SessionId: sessionID, Message: message})
	if duplicateAck.GetEventId() != messageAck.GetEventId() || duplicateAck.GetSeqId() != messageAck.GetSeqId() {
		fatal(fmt.Errorf("idempotent retry allocated a different event: first=%d/%d duplicate=%d/%d",
			messageAck.GetEventId(), messageAck.GetSeqId(), duplicateAck.GetEventId(), duplicateAck.GetSeqId()))
	}
	historyRequest := authenticated(connect.NewRequest(&gatewayv1.GetHistoryEventsRequest{SessionId: sessionID, Limit: 100}), aliceToken)
	history, err := sessions.GetHistoryEvents(ctx, historyRequest)
	if err != nil {
		fatal(fmt.Errorf("load history for idempotency: %w", err))
	}
	matchingMessages := 0
	for _, event := range history.Msg.GetEvents() {
		if event.GetMessage().GetClientMsgId() == clientMessageID {
			matchingMessages++
		}
	}
	if matchingMessages != 1 {
		fatal(fmt.Errorf("idempotent client message appears %d times in history", matchingMessages))
	}

	step("message edit push")
	editAck := sendAndAck(alicePrimary, prefix+"-edit", &gatewayv1.ChatRequest{
		SessionId: sessionID, Edit: &commonv1.MessageEdit{TargetEventId: messageAck.GetEventId(), NewContent: "local IM E2E edited"},
	})
	requireEvent(bobPrimary, editAck.GetEventId(), func(event *commonv1.ChatEvent) bool {
		return event.GetEdit().GetTargetEventId() == messageAck.GetEventId() && event.GetEdit().GetNewContent() == "local IM E2E edited"
	})

	step("multi-device read receipt")
	readRequest := authenticated(connect.NewRequest(&gatewayv1.UpdateReadPositionRequest{SessionId: sessionID, SeqId: editAck.GetSeqId()}), bobToken)
	if _, err := sessions.UpdateReadPosition(ctx, readRequest); err != nil {
		fatal(fmt.Errorf("update read position: %w", err))
	}
	readPredicate := func(event *commonv1.ChatEvent) bool {
		return event.GetFromUsername() == bob && event.GetReadReceipt().GetReadUptoSeqId() == editAck.GetSeqId()
	}
	requireEvent(alicePrimary, 0, readPredicate)
	requireEvent(aliceSecondary, 0, readPredicate)

	step("offline Inbox recovery")
	if err := bobPrimary.Close(); err != nil {
		fatal(fmt.Errorf("disconnect Bob before offline message: %w", err))
	}
	offlineAck := sendAndAck(alicePrimary, prefix+"-offline", &gatewayv1.ChatRequest{
		SessionId: sessionID,
		Message:   &commonv1.Message{Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: "local IM E2E offline", ClientMsgId: prefix + "-offline-message"},
	})
	bobReconnected := dialWS(baseURL, bobToken)
	defer closeWS(bobReconnected)
	if !waitForInbox(ctx, sessions, bobToken, offlineAck.GetEventId()) {
		fatal(fmt.Errorf("reconnected Bob did not recover offline event %d from Inbox", offlineAck.GetEventId()))
	}

	// Presence is intentionally batched by Gateway. Wait beyond two flush
	// intervals, then prove the reconnected route with a real pushed event before
	// using the same connection for the recall assertion.
	time.Sleep(250 * time.Millisecond)
	step("reconnected online push probe")
	probeAck := sendAndAck(alicePrimary, prefix+"-reconnect-probe", &gatewayv1.ChatRequest{
		SessionId: sessionID,
		Message: &commonv1.Message{Type: commonv1.MessageType_MESSAGE_TYPE_TEXT,
			Content: "local IM E2E reconnect probe", ClientMsgId: prefix + "-reconnect-probe-message"},
	})
	requireEvent(bobReconnected, probeAck.GetEventId(), func(event *commonv1.ChatEvent) bool {
		return event.GetMessage().GetContent() == "local IM E2E reconnect probe"
	})

	step("message recall push")
	recallAck := sendAndAck(alicePrimary, prefix+"-recall", &gatewayv1.ChatRequest{
		SessionId: sessionID, Recall: &commonv1.MessageRecall{TargetEventId: messageAck.GetEventId()},
	})
	requireEvent(bobReconnected, recallAck.GetEventId(), func(event *commonv1.ChatEvent) bool {
		return event.GetRecall().GetTargetEventId() == messageAck.GetEventId()
	})

	result := report{
		SchemaVersion: 1, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Prefix: prefix,
		AliceUsername: alice, BobUsername: bob, SessionID: sessionID,
		MessageEventID: messageAck.GetEventId(), MessageSeqID: messageAck.GetSeqId(), DuplicateEventID: duplicateAck.GetEventId(),
		EditEventID: editAck.GetEventId(), ReadReceiptSeqID: editAck.GetSeqId(),
		OfflineMessageEvent: offlineAck.GetEventId(), OfflineMessageSeqID: offlineAck.GetSeqId(), RecallEventID: recallAck.GetEventId(),
		InboxRecoveryVerified: true, MultiDeviceReadSeen: true, IdempotencyVerified: true,
	}
	writeReport(output, result)
}

func register(ctx context.Context, client gatewayv1connect.AuthServiceClient, username, nickname string) (string, string) {
	response, err := client.Register(ctx, connect.NewRequest(&gatewayv1.RegisterRequest{
		Username: username, Password: e2ePassword, Nickname: nickname,
	}))
	if err != nil {
		fatal(fmt.Errorf("register %s: %w", username, err))
	}
	if response.Msg.GetAccessToken() == "" || response.Msg.GetUser().GetUsername() != username {
		fatal(fmt.Errorf("registration for %s returned an invalid identity", username))
	}
	return username, response.Msg.GetAccessToken()
}

func authenticated[T any](request *connect.Request[T], token string) *connect.Request[T] {
	request.Header().Set("Authorization", "Bearer "+token)
	return request
}

func dialWS(baseURL, token string) *websocket.Conn {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		fatal(err)
	}
	if parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	} else {
		parsed.Scheme = "ws"
	}
	parsed.Path = "/ws"
	query := parsed.Query()
	query.Set("token", token)
	parsed.RawQuery = query.Encode()
	connection, response, err := websocket.DefaultDialer.Dial(parsed.String(), nil)
	if err != nil {
		if response != nil {
			fatal(fmt.Errorf("connect WebSocket: %w (HTTP %d)", err, response.StatusCode))
		}
		fatal(fmt.Errorf("connect WebSocket: %w", err))
	}
	return connection
}

func closeWS(connection *websocket.Conn) {
	if connection != nil {
		_ = connection.Close()
	}
}

func sendAndAck(connection *websocket.Conn, clientSequence string, request *gatewayv1.ChatRequest) *gatewayv1.Ack {
	packet := &gatewayv1.WsPacket{ClientSeq: clientSequence, Payload: &gatewayv1.WsPacket_ChatRequest{ChatRequest: request}}
	payload, err := proto.Marshal(packet)
	if err != nil {
		fatal(fmt.Errorf("marshal WebSocket request: %w", err))
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		fatal(fmt.Errorf("write WebSocket request: %w", err))
	}
	incoming := readUntil(connection, func(candidate *gatewayv1.WsPacket) bool {
		return candidate.GetAck().GetRefClientSeq() == clientSequence
	})
	ack := incoming.GetAck()
	if ack.GetEventId() == 0 || ack.GetSeqId() == 0 || ack.GetSessionId() != request.GetSessionId() {
		fatal(fmt.Errorf("invalid Ack for %s", clientSequence))
	}
	return ack
}

func requireEvent(connection *websocket.Conn, eventID int64, predicate func(*commonv1.ChatEvent) bool) {
	incoming := readUntil(connection, func(candidate *gatewayv1.WsPacket) bool {
		event := candidate.GetEvent()
		return event != nil && (eventID == 0 || event.GetEventId() == eventID) && predicate(event)
	})
	if incoming.GetEvent() == nil {
		fatal(fmt.Errorf("expected WebSocket event was not received"))
	}
}

func readUntil(connection *websocket.Conn, predicate func(*gatewayv1.WsPacket) bool) *gatewayv1.WsPacket {
	if err := connection.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		fatal(err)
	}
	for {
		messageType, data, err := connection.ReadMessage()
		if err != nil {
			fatal(fmt.Errorf("read WebSocket packet: %w", err))
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		packet := new(gatewayv1.WsPacket)
		if err := proto.Unmarshal(data, packet); err != nil {
			fatal(fmt.Errorf("decode WebSocket packet: %w", err))
		}
		if predicate(packet) {
			return packet
		}
	}
}

func waitForInbox(ctx context.Context, sessions gatewayv1connect.SessionServiceClient, token string, eventID int64) bool {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		request := authenticated(connect.NewRequest(&gatewayv1.PullInboxDeltaRequest{CursorId: 0, Limit: 100}), token)
		response, err := sessions.PullInboxDelta(ctx, request)
		if err == nil {
			for _, event := range response.Msg.GetEvents() {
				if event.GetEvent().GetEventId() == eventID {
					return true
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func writeReport(path string, result report) {
	file, err := os.Create(path)
	if err != nil {
		fatal(err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		_ = file.Close()
		fatal(err)
	}
	if err := file.Close(); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, strings.TrimSpace(err.Error()))
	os.Exit(1)
}

func step(name string) {
	_, _ = fmt.Fprintln(os.Stderr, "IM E2E:", name)
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
