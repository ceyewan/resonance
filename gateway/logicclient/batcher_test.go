package logicclient

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
)

type recordingPresenceClient struct {
	mu       sync.Mutex
	requests []*logicv1.SyncStatusRequest
}

func (c *recordingPresenceClient) SyncStatus(_ context.Context, request *logicv1.SyncStatusRequest, _ ...grpc.CallOption) (*logicv1.SyncStatusResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, proto.Clone(request).(*logicv1.SyncStatusRequest))
	return &logicv1.SyncStatusResponse{SeqId: request.GetSeqId()}, nil
}

func TestStatusBatcherCoalescesRapidReconnectToOnline(t *testing.T) {
	client := &recordingPresenceClient{}
	batcher := NewStatusBatcher(client, "gateway-1", clog.Discard(), WithFlushInterval(time.Hour))

	batcher.SyncUserOffline("alice")
	batcher.SyncUserOnline("alice", "127.0.0.1:2000")
	batcher.flush()

	require.Len(t, client.requests, 1)
	require.Empty(t, client.requests[0].GetOfflineBatch())
	require.Len(t, client.requests[0].GetOnlineBatch(), 1)
	require.Equal(t, "127.0.0.1:2000", client.requests[0].GetOnlineBatch()[0].GetRemoteIp())
}

func TestStatusBatcherCoalescesLatestStatePerUser(t *testing.T) {
	client := &recordingPresenceClient{}
	batcher := NewStatusBatcher(client, "gateway-1", clog.Discard(), WithFlushInterval(time.Hour))

	batcher.SyncUserOnline("alice", "127.0.0.1:1000")
	batcher.SyncUserOnline("alice", "127.0.0.1:2000")
	batcher.SyncUserOffline("alice")
	batcher.SyncUserOnline("bob", "127.0.0.1:3000")
	batcher.flush()

	require.Len(t, client.requests, 1)
	require.Len(t, client.requests[0].GetOfflineBatch(), 1)
	require.Equal(t, "alice", client.requests[0].GetOfflineBatch()[0].GetUsername())
	require.Len(t, client.requests[0].GetOnlineBatch(), 1)
	require.Equal(t, "bob", client.requests[0].GetOnlineBatch()[0].GetUsername())
}
