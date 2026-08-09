package ws

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
)

func TestManager_MultipleDevicesSharePresenceAndPush(t *testing.T) {
	var connects atomic.Int32
	var disconnects atomic.Int32
	manager := NewManager(
		clog.Discard(),
		nil,
		func(string, string) error { connects.Add(1); return nil },
		func(string) error { disconnects.Add(1); return nil },
	)

	first, firstClient, firstCleanup := newManagerTestConn(t, "alice")
	defer firstCleanup()
	second, secondClient, secondCleanup := newManagerTestConn(t, "alice")
	defer secondCleanup()

	require.NoError(t, manager.AddConnection("alice", first))
	require.NoError(t, manager.AddConnection("alice", second))
	require.Equal(t, int32(1), connects.Load())
	require.Equal(t, 2, manager.OnlineCount())
	require.Len(t, manager.GetConnections("alice"), 2)

	packet := &gatewayv1.WsPacket{Payload: &gatewayv1.WsPacket_Typing{Typing: &gatewayv1.TypingSignal{
		SessionId: "session-1", FromUsername: "bob",
	}}}
	require.NoError(t, manager.SendToUser("alice", packet))
	requirePacket(t, firstClient)
	requirePacket(t, secondClient)

	require.NoError(t, first.Close())
	require.Eventually(t, func() bool { return manager.OnlineCount() == 1 }, time.Second, 10*time.Millisecond)
	require.Zero(t, disconnects.Load())
	require.NoError(t, manager.SendToUser("alice", packet))
	requirePacket(t, secondClient)

	require.NoError(t, second.Close())
	require.Eventually(t, func() bool { return manager.OnlineCount() == 0 }, time.Second, 10*time.Millisecond)
	require.Equal(t, int32(1), disconnects.Load())
}

func newManagerTestConn(t *testing.T, username string) (*Conn, *websocket.Conn, func()) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	serverConn := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			serverConn <- conn
		}
	}))
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	serverWebSocket := <-serverConn
	conn := NewConn(username, "trace-test", serverWebSocket, clog.Discard(), NewDefaultHandler(clog.Discard(), nil, nil, nil), 1<<20, time.Minute, 2*time.Minute)
	conn.Run()
	return conn, client, func() {
		_ = conn.Close()
		_ = client.Close()
		server.Close()
	}
}

func requirePacket(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	messageType, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, messageType)
	_, err = DecodePacket(payload)
	require.NoError(t, err)
}
