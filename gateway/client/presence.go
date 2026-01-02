package client

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ceyewan/genesis/clog"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
)

// presenceStreamManager 管理与 Logic 服务的 Presence 状态同步双向流
type presenceStreamManager struct {
	manager   *bidiStreamManager[logicv1.SyncStatusRequest, logicv1.SyncStatusResponse]
	logger    clog.Logger
	gatewayID string

	pending sync.Map // key: seqID, value: chan *logicv1.SyncStatusResponse
	seq     atomic.Int64
}

func newPresenceStreamManager(logger clog.Logger, gatewayID string, svc logicv1.PresenceServiceClient) *presenceStreamManager {
	mgr := &presenceStreamManager{
		logger:    logger,
		gatewayID: gatewayID,
	}
	mgr.manager = newBidiStreamManager(
		"presence",
		logger,
		func(ctx context.Context) (logicv1.PresenceService_SyncStatusClient, error) {
			return svc.SyncStatus(ctx)
		},
		mgr.handleResponse,
		mgr.handleStreamError,
	)
	return mgr
}

func (m *presenceStreamManager) Close() {
	m.manager.Close()
	m.failAllPending(fmt.Errorf("presence stream closed"))
}

func (c *Client) SyncUserOnline(ctx context.Context, username string, remoteIP string) error {
	if c.presenceManager == nil {
		return fmt.Errorf("presence manager not initialized")
	}

	req := &logicv1.SyncStatusRequest{
		SeqId:     c.presenceManager.nextSeq(),
		GatewayId: c.gatewayID,
		OnlineBatch: []*logicv1.UserOnline{
			{
				Username:  username,
				RemoteIp:  remoteIP,
				Timestamp: time.Now().Unix(),
			},
		},
	}
	return c.presenceManager.send(ctx, req)
}

func (c *Client) SyncUserOffline(ctx context.Context, username string) error {
	if c.presenceManager == nil {
		return fmt.Errorf("presence manager not initialized")
	}

	req := &logicv1.SyncStatusRequest{
		SeqId:     c.presenceManager.nextSeq(),
		GatewayId: c.gatewayID,
		OfflineBatch: []*logicv1.UserOffline{
			{
				Username:  username,
				Timestamp: time.Now().Unix(),
			},
		},
	}
	return c.presenceManager.send(ctx, req)
}

func (m *presenceStreamManager) send(ctx context.Context, req *logicv1.SyncStatusRequest) error {
	respCh := make(chan *logicv1.SyncStatusResponse, 1)
	m.pending.Store(req.SeqId, respCh)

	if err := m.manager.Send(ctx, req); err != nil {
		m.pending.Delete(req.SeqId)
		return err
	}

	select {
	case resp := <-respCh:
		if resp.Error != "" {
			return fmt.Errorf("presence sync failed: %s", resp.Error)
		}
		return nil
	case <-ctx.Done():
		m.pending.Delete(req.SeqId)
		go func() {
			<-respCh
		}()
		return ctx.Err()
	}
}

func (m *presenceStreamManager) handleResponse(resp *logicv1.SyncStatusResponse) {
	value, ok := m.pending.Load(resp.SeqId)
	if !ok {
		if m.logger != nil {
			m.logger.Warn("presence ack dropped: unknown seq",
				clog.Int64("seq_id", resp.SeqId))
		}
		return
	}

	ch := value.(chan *logicv1.SyncStatusResponse)
	m.pending.Delete(resp.SeqId)

	select {
	case ch <- resp:
	default:
	}
	close(ch)
}

func (m *presenceStreamManager) handleStreamError(err error) {
	m.failAllPending(err)
}

func (m *presenceStreamManager) failAllPending(err error) {
	errMsg := "presence stream closed"
	if err != nil {
		errMsg = err.Error()
	}

	m.pending.Range(func(key, value any) bool {
		ch := value.(chan *logicv1.SyncStatusResponse)
		seq, _ := key.(int64)
		resp := &logicv1.SyncStatusResponse{
			SeqId: seq,
			Error: errMsg,
		}
		select {
		case ch <- resp:
		default:
		}
		close(ch)
		m.pending.Delete(key)
		return true
	})
}

func (m *presenceStreamManager) nextSeq() int64 {
	return m.seq.Add(1)
}
