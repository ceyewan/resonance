package connection

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/gateway/protocol"
	"github.com/gorilla/websocket"
)

// Conn 表示一个 WebSocket 连接
type Conn struct {
	username   string
	conn       *websocket.Conn
	send       chan *gatewayv1.WsPacket
	logger     clog.Logger
	handler    protocol.Handler
	ctx        context.Context
	cancel     context.CancelFunc
	closeOnce  sync.Once
	remoteAddr string

	// 配置
	maxMessageSize int64
	pingInterval   time.Duration
	pongTimeout    time.Duration
}

// NewConn 创建新的连接
func NewConn(
	username string,
	conn *websocket.Conn,
	logger clog.Logger,
	handler protocol.Handler,
	maxMessageSize int64,
	pingInterval time.Duration,
	pongTimeout time.Duration,
) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	return &Conn{
		username:       username,
		conn:           conn,
		send:           make(chan *gatewayv1.WsPacket, 256),
		logger:         logger,
		handler:        handler,
		ctx:            ctx,
		cancel:         cancel,
		remoteAddr:     conn.RemoteAddr().String(),
		maxMessageSize: maxMessageSize,
		pingInterval:   pingInterval,
		pongTimeout:    pongTimeout,
	}
}

// Username 实现 protocol.Connection 接口
func (c *Conn) Username() string {
	return c.username
}

// RemoteAddr 实现 protocol.Connection 接口
func (c *Conn) RemoteAddr() string {
	return c.remoteAddr
}

// Send 实现 protocol.Connection 接口
func (c *Conn) Send(packet *gatewayv1.WsPacket) error {
	select {
	case c.send <- packet:
		return nil
	case <-c.ctx.Done():
		return fmt.Errorf("connection closed")
	default:
		return fmt.Errorf("send buffer full")
	}
}

// Close 实现 protocol.Connection 接口
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		c.cancel()
		close(c.send)
		c.conn.Close()
	})
	return nil
}

// Run 启动连接的读写协程
func (c *Conn) Run() {
	go c.writePump()
	go c.readPump()
}

// readPump 从 WebSocket 读取消息
func (c *Conn) readPump() {
	defer c.Close()

	c.conn.SetReadLimit(c.maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(c.pongTimeout))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(c.pongTimeout))
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
		packet, err := protocol.DecodePacket(message)
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
		c.Close()
	}()

	for {
		select {
		case packet, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			// 编码消息
			data, err := protocol.EncodePacket(packet)
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
