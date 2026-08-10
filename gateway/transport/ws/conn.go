package ws

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/gorilla/websocket"

	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/gateway/middleware"
	"github.com/ceyewan/resonance/pkg/userauth"
)

type ConnOption func(*Conn)

func WithParentContext(parent context.Context) ConnOption {
	return func(conn *Conn) {
		if parent != nil {
			// The HTTP upgrade request ends immediately after the socket is handed
			// off. Preserve its trace/principal values without inheriting request
			// cancellation into the long-lived WebSocket lifecycle.
			conn.ctx = context.WithoutCancel(parent)
		}
	}
}

func WithUserPrincipal(principal *userauth.Principal) ConnOption {
	return func(conn *Conn) {
		conn.ctx = userauth.WithPrincipal(conn.ctx, principal)
	}
}

// Conn 表示一个 WebSocket 连接
type Conn struct {
	username   string
	traceID    string // 会话级 trace_id
	conn       *websocket.Conn
	send       chan *gatewayv1.WsPacket
	logger     clog.Logger
	handler    Handler
	ctx        context.Context
	cancel     context.CancelFunc
	closeOnce  sync.Once
	onClose    func()
	remoteAddr string

	// 配置
	maxMessageSize int64
	pingInterval   time.Duration
	pongTimeout    time.Duration
}

// NewConn 创建新的连接
func NewConn(
	username string,
	traceID string,
	conn *websocket.Conn,
	logger clog.Logger,
	handler Handler,
	maxMessageSize int64,
	pingInterval time.Duration,
	pongTimeout time.Duration,
	options ...ConnOption,
) *Conn {
	wrapped := &Conn{
		username:       username,
		traceID:        traceID,
		conn:           conn,
		send:           make(chan *gatewayv1.WsPacket, 256),
		logger:         logger,
		handler:        handler,
		ctx:            context.Background(),
		remoteAddr:     conn.RemoteAddr().String(),
		maxMessageSize: maxMessageSize,
		pingInterval:   pingInterval,
		pongTimeout:    pongTimeout,
	}
	for _, option := range options {
		option(wrapped)
	}
	ctx, cancel := context.WithCancel(wrapped.ctx)
	if traceID != "" {
		ctx = middleware.WithTraceID(ctx, traceID)
	}
	wrapped.ctx = ctx
	wrapped.cancel = cancel
	return wrapped
}

// Username 实现 ws.Connection 接口
func (c *Conn) Username() string {
	return c.username
}

// RemoteAddr 实现 ws.Connection 接口
func (c *Conn) RemoteAddr() string {
	return c.remoteAddr
}

// Send 实现 ws.Connection 接口
func (c *Conn) Send(packet *gatewayv1.WsPacket) error {
	if c.ctx.Err() != nil {
		return fmt.Errorf("connection closed")
	}

	select {
	case c.send <- packet:
		return nil
	case <-c.ctx.Done():
		return fmt.Errorf("connection closed")
	default:
		return fmt.Errorf("send buffer full")
	}
}

// Close 实现 ws.Connection 接口
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		c.cancel()
		_ = c.conn.Close()
		if c.onClose != nil {
			c.onClose()
		}
	})
	return nil
}

func (c *Conn) setOnClose(onClose func()) {
	c.onClose = onClose
}

// Run 启动连接的读写协程
func (c *Conn) Run() {
	go c.writePump()
	go c.readPump()
}

// readPump 从 WebSocket 读取消息
func (c *Conn) readPump() {
	defer func() { _ = c.Close() }()

	c.conn.SetReadLimit(c.maxMessageSize)
	if err := c.conn.SetReadDeadline(time.Now().Add(c.pongTimeout)); err != nil {
		c.logger.Warn("failed to set initial read deadline", clog.String("username", c.username), clog.Error(err))
	}
	c.conn.SetPongHandler(func(string) error {
		if err := c.conn.SetReadDeadline(time.Now().Add(c.pongTimeout)); err != nil {
			c.logger.Warn("failed to extend read deadline", clog.String("username", c.username), clog.Error(err))
			return err
		}
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.logger.Error("websocket read error",
					clog.String("username", c.username),
					clog.Error(err))
			}
			break
		}

		// 解码消息
		packet, err := DecodePacket(message)
		if err != nil {
			c.logger.Error("failed to decode packet",
				clog.String("username", c.username),
				clog.Error(err))
			continue
		}

		// 处理消息
		if err := c.handler.HandlePacket(c.ctx, c, packet); err != nil {
			c.logger.Error("failed to handle packet",
				clog.String("username", c.username),
				clog.Error(err))
		}
	}
}

// writePump 向 WebSocket 写入消息
func (c *Conn) writePump() {
	ticker := time.NewTicker(c.pingInterval)
	defer func() {
		ticker.Stop()
		_ = c.Close()
	}()

	for {
		select {
		case packet, ok := <-c.send:
			if !ok {
				if err := c.conn.WriteMessage(websocket.CloseMessage, []byte{}); err != nil {
					c.logger.Debug("failed to write close frame", clog.String("username", c.username), clog.Error(err))
				}
				return
			}

			// 编码消息
			data, err := EncodePacket(packet)
			if err != nil {
				c.logger.Error("failed to encode packet",
					clog.String("username", c.username),
					clog.Error(err))
				continue
			}

			// 发送消息
			if err := c.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
				c.logger.Error("failed to write message",
					clog.String("username", c.username),
					clog.Error(err))
				return
			}

		case <-ticker.C:
			// 发送心跳
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}

		case <-c.ctx.Done():
			return
		}
	}
}
