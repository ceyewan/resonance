package pusher

import (
	"context"
	"sync"
	"testing"

	"github.com/ceyewan/genesis/clog"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
)

type testPushServiceClient struct {
	pushEventFn func(ctx context.Context, in *gatewayv1.PushEventRequest, opts ...grpc.CallOption) (*gatewayv1.PushEventResponse, error)
}

func (c *testPushServiceClient) PushEvent(ctx context.Context, in *gatewayv1.PushEventRequest, opts ...grpc.CallOption) (*gatewayv1.PushEventResponse, error) {
	if c.pushEventFn != nil {
		return c.pushEventFn(ctx, in, opts...)
	}
	return &gatewayv1.PushEventResponse{}, nil
}

func (c *testPushServiceClient) PushStream(ctx context.Context, in *gatewayv1.PushStreamRequest, opts ...grpc.CallOption) (*gatewayv1.PushStreamResponse, error) {
	return &gatewayv1.PushStreamResponse{}, nil
}

func newTestGatewayClient(t *testing.T, queueSize int) *GatewayClient {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	conn, err := grpc.NewClient("dns:///127.0.0.1:65535", grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	return &GatewayClient{
		conn:      conn,
		client:    &testPushServiceClient{},
		id:        "gw-test",
		pushQueue: make(chan *PushTask, queueSize),
		logger:    clog.Discard(),
		ctx:       ctx,
		cancel:    cancel,
		wg:        &sync.WaitGroup{},
	}
}

func TestGatewayClient_Enqueue_Success(t *testing.T) {
	c := newTestGatewayClient(t, 2)
	defer func() { _ = c.Close() }()

	err := c.Enqueue(&PushTask{
		ToUsernames: []string{"alice"},
		Event:       &commonv1.ChatEvent{EventId: 1},
	})
	require.NoError(t, err)
	require.Equal(t, 1, c.QueueSize())
}

func TestGatewayClient_Enqueue_QueueFull(t *testing.T) {
	c := newTestGatewayClient(t, 1)
	defer func() { _ = c.Close() }()

	require.NoError(t, c.Enqueue(&PushTask{
		ToUsernames: []string{"alice"},
		Event:       &commonv1.ChatEvent{EventId: 1},
	}))
	err := c.Enqueue(&PushTask{
		ToUsernames: []string{"bob"},
		Event:       &commonv1.ChatEvent{EventId: 2},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "queue full")
}

func TestGatewayClient_Enqueue_WhenClosing(t *testing.T) {
	c := newTestGatewayClient(t, 1)
	defer func() { _ = c.Close() }()
	c.closing.Store(true)

	err := c.Enqueue(&PushTask{
		ToUsernames: []string{"alice"},
		Event:       &commonv1.ChatEvent{EventId: 1},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "client closing")
}

func TestGatewayClient_Enqueue_WhenContextCanceled(t *testing.T) {
	c := newTestGatewayClient(t, 1)
	defer func() { _ = c.Close() }()
	c.cancel()

	err := c.Enqueue(&PushTask{
		ToUsernames: []string{"alice"},
		Event:       &commonv1.ChatEvent{EventId: 1},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "client closing")
}

func TestGatewayClient_EnqueueBlocking_SkipWhenClosing(t *testing.T) {
	c := newTestGatewayClient(t, 1)
	defer func() { _ = c.Close() }()
	c.closing.Store(true)

	c.EnqueueBlocking(&PushTask{
		ToUsernames: []string{"alice"},
		Event:       &commonv1.ChatEvent{EventId: 1},
	})
	require.Equal(t, 0, c.QueueSize())
}

func TestGatewayClient_EnqueueBlocking_SkipWhenCanceled(t *testing.T) {
	c := newTestGatewayClient(t, 1)
	defer func() { _ = c.Close() }()
	c.cancel()

	c.EnqueueBlocking(&PushTask{
		ToUsernames: []string{"alice"},
		Event:       &commonv1.ChatEvent{EventId: 1},
	})
	require.Equal(t, 0, c.QueueSize())
}

func TestGatewayClient_Close_Idempotent(t *testing.T) {
	c := newTestGatewayClient(t, 1)

	require.NoError(t, c.Close())
	require.NoError(t, c.Close())
	require.True(t, c.closing.Load())
}
