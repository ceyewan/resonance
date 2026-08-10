package integration

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/gateway/pushserver"
	"github.com/ceyewan/resonance/gateway/server"
	"github.com/ceyewan/resonance/gateway/transport/ws"
)

func TestGatewayIntegration_PushEvent_GrpcToWebSocket(t *testing.T) {
	logger := clog.Discard()
	connMgr := ws.NewManager(logger, nil, nil, nil)
	wsConn, wsClient, cleanup := newWSConnForTest(t, "alice")
	defer cleanup()
	require.NoError(t, connMgr.AddConnection("alice", wsConn))

	pushSvc := pushserver.NewService(connMgr, logger)
	addr := mustFreeAddr(t)
	grpcServer := server.NewGRPCServer(addr, logger, pushSvc)
	go func() { _ = grpcServer.Start() }()
	t.Cleanup(grpcServer.Stop)

	conn := dialWithRetry(t, addr, 5*time.Second)
	t.Cleanup(func() { _ = conn.Close() })
	client := gatewayv1.NewPushServiceClient(conn)

	resp, err := client.PushEvent(context.Background(), &gatewayv1.PushEventRequest{
		ToUsernames: []string{"alice", "bob"},
		Event: &commonv1.ChatEvent{
			EventId:   10001,
			SessionId: "s_gateway_it_1",
			Payload: &commonv1.ChatEvent_Message{
				Message: &commonv1.Message{Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: "gateway integration"},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, int64(10001), resp.GetEventId())
	require.Equal(t, []string{"bob"}, resp.GetFailedUsernames())

	require.NoError(t, wsClient.SetReadDeadline(time.Now().Add(2*time.Second)))
	msgType, data, err := wsClient.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, msgType)
	packet, err := ws.DecodePacket(data)
	require.NoError(t, err)
	require.Equal(t, "gateway integration", packet.GetEvent().GetMessage().GetContent())
	require.Equal(t, int64(10001), packet.GetEvent().GetEventId())
}

func newWSConnForTest(t *testing.T, username string) (*ws.Conn, *websocket.Conn, func()) {
	t.Helper()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	serverConnCh := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConnCh <- c
	}))

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)

	var serverConn *websocket.Conn
	select {
	case serverConn = <-serverConnCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server WebSocket connection")
	}

	conn := ws.NewConn(
		username,
		"trace-gateway-it",
		serverConn,
		clog.Discard(),
		ws.NewDefaultHandler(clog.Discard(), nil, nil, nil),
		1024*1024,
		30*time.Second,
		60*time.Second,
	)
	conn.Run()
	return conn, clientConn, func() {
		_ = conn.Close()
		_ = clientConn.Close()
		srv.Close()
	}
}

func mustFreeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func dialWithRetry(t *testing.T, addr string, timeout time.Duration) *grpc.ClientConn {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		conn.Connect()
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		state := conn.GetState()
		if state == connectivity.Ready || state == connectivity.Idle || conn.WaitForStateChange(ctx, state) {
			cancel()
			return conn
		}
		cancel()
		lastErr = fmt.Errorf("grpc not ready, state=%s", conn.GetState())
		_ = conn.Close()
		time.Sleep(100 * time.Millisecond)
	}
	require.NoError(t, lastErr)
	return nil
}
