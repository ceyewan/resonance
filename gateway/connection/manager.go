package connection

import (
	"fmt"
	"sync"

	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/gorilla/websocket"
)

// Manager 管理所有 WebSocket 连接
type Manager struct {
	connections sync.Map // username -> *Conn
	logger      clog.Logger
	upgrader    *websocket.Upgrader

	// 回调函数
	onConnect    func(username string, remoteIP string) error
	onDisconnect func(username string) error
}

// NewManager 创建连接管理器
func NewManager(
	logger clog.Logger,
	upgrader *websocket.Upgrader,
	onConnect func(username string, remoteIP string) error,
	onDisconnect func(username string) error,
) *Manager {
	return &Manager{
		logger:       logger,
		upgrader:     upgrader,
		onConnect:    onConnect,
		onDisconnect: onDisconnect,
	}
}

// AddConnection 添加连接
func (m *Manager) AddConnection(username string, conn *Conn) error {
	// 检查是否已存在连接，如果存在则关闭旧连接
	if oldConn, ok := m.connections.Load(username); ok {
		m.logger.Warn("user already connected, closing old connection",
			clog.String("username", username))
		oldConn.(*Conn).Close()
	}

	m.connections.Store(username, conn)
	m.logger.Info("user connected",
		clog.String("username", username),
		clog.String("remote_addr", conn.RemoteAddr()))

	// 触发上线回调
	if m.onConnect != nil {
		if err := m.onConnect(username, conn.RemoteAddr()); err != nil {
			m.logger.Error("failed to notify user online",
				clog.String("username", username),
				clog.Error(err))
			return err
		}
	}

	return nil
}

// RemoveConnection 移除连接
func (m *Manager) RemoveConnection(username string) {
	if conn, ok := m.connections.LoadAndDelete(username); ok {
		conn.(*Conn).Close()
		m.logger.Info("user disconnected", clog.String("username", username))

		// 触发下线回调
		if m.onDisconnect != nil {
			if err := m.onDisconnect(username); err != nil {
				m.logger.Error("failed to notify user offline",
					clog.String("username", username),
					clog.Error(err))
			}
		}
	}
}

// GetConnection 获取连接
func (m *Manager) GetConnection(username string) (*Conn, bool) {
	if conn, ok := m.connections.Load(username); ok {
		return conn.(*Conn), true
	}
	return nil, false
}

// SendToUser 发送消息给指定用户
func (m *Manager) SendToUser(username string, packet *gatewayv1.WsPacket) error {
	conn, ok := m.GetConnection(username)
	if !ok {
		return fmt.Errorf("user not connected: %s", username)
	}
	return conn.Send(packet)
}

// Broadcast 广播消息给所有在线用户
func (m *Manager) Broadcast(packet *gatewayv1.WsPacket) {
	m.connections.Range(func(key, value interface{}) bool {
		conn := value.(*Conn)
		if err := conn.Send(packet); err != nil {
			m.logger.Error("failed to broadcast message",
				clog.String("username", key.(string)),
				clog.Error(err))
		}
		return true
	})
}

// OnlineCount 获取在线用户数
func (m *Manager) OnlineCount() int {
	count := 0
	m.connections.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}

// Close 关闭所有连接
func (m *Manager) Close() error {
	m.connections.Range(func(key, value interface{}) bool {
		conn := value.(*Conn)
		conn.Close()
		return true
	})
	return nil
}

// Upgrader 获取 WebSocket 升级器
func (m *Manager) Upgrader() *websocket.Upgrader {
	return m.upgrader
}
