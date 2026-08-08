package pushserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/gateway/transport/ws"
)

func TestService_PushEvent_PartialSuccess(t *testing.T) {
	connMgr := ws.NewManager(clog.Discard(), nil, nil, nil)
	onlineConn, cleanup := newWSConnForTest(t, "alice")
	defer cleanup()
	require.NoError(t, connMgr.AddConnection("alice", onlineConn))

	svc := NewService(connMgr, clog.Discard())
	resp, err := svc.PushEvent(context.Background(), &gatewayv1.PushEventRequest{
		ToUsernames: []string{"alice", "bob"},
		Event: &commonv1.ChatEvent{
			EventId:   101,
			SessionId: "s_1",
			Payload: &commonv1.ChatEvent_Message{
				Message: &commonv1.Message{Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: "hello"},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, int64(101), resp.GetEventId())
	require.Equal(t, []string{"bob"}, resp.GetFailedUsernames())
}

func TestService_PushStream_UnsupportedPayload(t *testing.T) {
	connMgr := ws.NewManager(clog.Discard(), nil, nil, nil)
	svc := NewService(connMgr, clog.Discard())

	resp, err := svc.PushStream(context.Background(), &gatewayv1.PushStreamRequest{
		ToUsernames: []string{"alice"},
	})
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestService_PushStream_RejectsLegacyOnlyChunk(t *testing.T) {
	connMgr := ws.NewManager(clog.Discard(), nil, nil, nil)
	svc := NewService(connMgr, clog.Discard())
	_, err := svc.PushStream(context.Background(), &gatewayv1.PushStreamRequest{
		ToUsernames: []string{"alice"},
		Payload: &gatewayv1.PushStreamRequest_StreamChunk{StreamChunk: &gatewayv1.StreamChunk{
			ParentEventId: 1, Sequence: 1, Delta: "unsafe without run correlation",
		}},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func newWSConnForTest(t *testing.T, username string) (*ws.Conn, func()) {
	t.Helper()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	var serverConn *websocket.Conn
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConn = c
	}))

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return serverConn != nil
	}, 2*time.Second, 20*time.Millisecond)

	conn := ws.NewConn(
		username,
		"trace-test",
		serverConn,
		clog.Discard(),
		ws.NewDefaultHandler(clog.Discard(), nil, nil, nil),
		1024*1024,
		30*time.Second,
		60*time.Second,
	)
	return conn, func() {
		_ = conn.Close()
		_ = clientConn.Close()
		srv.Close()
	}
}
