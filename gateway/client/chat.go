package client

import (
	"context"
	"fmt"
	"sync"

	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
)

type chatStreamManager struct {
	manager *bidiStreamManager[logicv1.SendMessageRequest, logicv1.SendMessageResponse]
	logger  clog.Logger

	pendingMu sync.Mutex
	pending   []chan *logicv1.SendMessageResponse
}

func newChatStreamManager(logger clog.Logger, svc logicv1.ChatServiceClient) *chatStreamManager {
	mgr := &chatStreamManager{
		logger: logger,
	}
	mgr.manager = newBidiStreamManager(
		"chat",
		logger,
		func(ctx context.Context) (logicv1.ChatService_SendMessageClient, error) {
			return svc.SendMessage(ctx)
		},
		mgr.handleResponse,
		mgr.handleStreamError,
	)
	return mgr
}

func (m *chatStreamManager) Close() {
	m.manager.Close()
	m.failAllPending(fmt.Errorf("chat stream closed"))
}

func (c *Client) SendMessage(ctx context.Context, msg *gatewayv1.ChatRequest) (*logicv1.SendMessageResponse, error) {
	if c.chatManager == nil {
		return nil, fmt.Errorf("chat manager not initialized")
	}

	req := &logicv1.SendMessageRequest{
		SessionId:    msg.SessionId,
		FromUsername: msg.FromUsername,
		ToUsername:   msg.ToUsername,
		Content:      msg.Content,
		Type:         msg.Type,
		Timestamp:    msg.Timestamp,
	}

	return c.chatManager.send(ctx, req)
}

func (m *chatStreamManager) send(ctx context.Context, req *logicv1.SendMessageRequest) (*logicv1.SendMessageResponse, error) {
	respCh := make(chan *logicv1.SendMessageResponse, 1)
	m.enqueue(respCh)

	if err := m.manager.Send(ctx, req); err != nil {
		m.drop(respCh)
		return nil, err
	}

	select {
	case resp := <-respCh:
		return resp, nil
	case <-ctx.Done():
		go func() {
			<-respCh
		}()
		return nil, ctx.Err()
	}
}

func (m *chatStreamManager) handleResponse(resp *logicv1.SendMessageResponse) {
	ch := m.pop()
	if ch == nil {
		if m.logger != nil {
			m.logger.Warn("chat response dropped: no pending request")
		}
		return
	}
	select {
	case ch <- resp:
	default:
	}
	close(ch)
}

func (m *chatStreamManager) handleStreamError(err error) {
	m.failAllPending(err)
}

func (m *chatStreamManager) enqueue(ch chan *logicv1.SendMessageResponse) {
	m.pendingMu.Lock()
	m.pending = append(m.pending, ch)
	m.pendingMu.Unlock()
}

func (m *chatStreamManager) pop() chan *logicv1.SendMessageResponse {
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()
	if len(m.pending) == 0 {
		return nil
	}
	ch := m.pending[0]
	m.pending = m.pending[1:]
	return ch
}

func (m *chatStreamManager) drop(target chan *logicv1.SendMessageResponse) {
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()
	for i, ch := range m.pending {
		if ch == target {
			m.pending = append(m.pending[:i], m.pending[i+1:]...)
			break
		}
	}
}

func (m *chatStreamManager) failAllPending(err error) {
	m.pendingMu.Lock()
	pending := m.pending
	m.pending = nil
	m.pendingMu.Unlock()

	if len(pending) == 0 {
		return
	}

	resp := &logicv1.SendMessageResponse{}
	if err != nil {
		resp.Error = err.Error()
	} else {
		resp.Error = "chat stream closed"
	}

	for _, ch := range pending {
		select {
		case ch <- resp:
		default:
		}
		close(ch)
	}
}
