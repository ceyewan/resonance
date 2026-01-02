package client

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/ceyewan/genesis/clog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// bidiStreamManager 管理长连接双向流，封装建连、重连与接收循环。
type bidiStreamManager[Req any, Resp any] struct {
	name      string
	logger    clog.Logger
	newStream func(ctx context.Context) (grpc.BidiStreamingClient[Req, Resp], error)
	onReceive func(resp *Resp)
	onBroken  func(err error)

	mu          sync.Mutex
	stream      grpc.BidiStreamingClient[Req, Resp]
	streamClose context.CancelFunc
}

func newBidiStreamManager[Req any, Resp any](
	name string,
	logger clog.Logger,
	newStream func(ctx context.Context) (grpc.BidiStreamingClient[Req, Resp], error),
	onReceive func(resp *Resp),
	onBroken func(err error),
) *bidiStreamManager[Req, Resp] {
	return &bidiStreamManager[Req, Resp]{
		name:      name,
		logger:    logger,
		newStream: newStream,
		onReceive: onReceive,
		onBroken:  onBroken,
	}
}

// Send 在保证流可用的前提下发送请求，异常时会重置流。
func (m *bidiStreamManager[Req, Resp]) Send(ctx context.Context, req *Req) error {
	stream, err := m.ensureStream(ctx)
	if err != nil {
		return err
	}

	if err := stream.Send(req); err != nil {
		m.resetStream(stream, fmt.Errorf("send failed: %w", err))
		return err
	}
	return nil
}

// Close 主动关闭当前流。
func (m *bidiStreamManager[Req, Resp]) Close() {
	m.resetStream(nil, context.Canceled)
}

func (m *bidiStreamManager[Req, Resp]) ensureStream(ctx context.Context) (grpc.BidiStreamingClient[Req, Resp], error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.stream != nil {
		return m.stream, nil
	}

	streamCtx, cancel := context.WithCancel(persistentStreamContext(ctx))
	stream, err := m.newStream(streamCtx)
	if err != nil {
		cancel()
		return nil, err
	}

	m.stream = stream
	m.streamClose = cancel

	go m.receiveLoop(stream)

	return stream, nil
}

func (m *bidiStreamManager[Req, Resp]) receiveLoop(stream grpc.BidiStreamingClient[Req, Resp]) {
	for {
		resp, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				m.resetStream(stream, fmt.Errorf("%s stream closed", m.name))
			} else {
				m.resetStream(stream, fmt.Errorf("%s stream receive error: %w", m.name, err))
			}
			return
		}
		if m.onReceive != nil {
			m.onReceive(resp)
		}
	}
}

func (m *bidiStreamManager[Req, Resp]) resetStream(stream grpc.BidiStreamingClient[Req, Resp], reason error) {
	m.mu.Lock()
	if m.stream == nil {
		m.mu.Unlock()
		return
	}
	if stream != nil && m.stream != stream {
		m.mu.Unlock()
		return
	}

	cancel := m.streamClose
	m.stream = nil
	m.streamClose = nil
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	if m.logger != nil && reason != nil {
		m.logger.Warn("bidi stream reset",
			clog.String("stream", m.name),
			clog.String("reason", reason.Error()))
	}

	if m.onBroken != nil {
		m.onBroken(reason)
	}
}

// persistentStreamContext 构造一个不受调用方超时/取消影响的 Context，但保留 trace-id。
func persistentStreamContext(ctx context.Context) context.Context {
	base := context.Background()
	if traceID := ctx.Value(traceIDKey); traceID != nil {
		base = context.WithValue(base, traceIDKey, traceID)
	}
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		base = metadata.NewOutgoingContext(base, md)
	}
	return base
}
