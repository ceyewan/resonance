package ws

import (
	"context"
	"fmt"
	"sync"

	"github.com/ceyewan/genesis/clog"
	"github.com/gorilla/websocket"

	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/gateway/observability"
)

// Manager 管理所有 WebSocket 连接
type Manager struct {
	mu          sync.RWMutex
	connections map[string]map[*Conn]struct{}
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
		connections:  make(map[string]map[*Conn]struct{}),
		logger:       logger,
		upgrader:     upgrader,
		onConnect:    onConnect,
		onDisconnect: onDisconnect,
	}
}

// AddConnection 添加连接
func (m *Manager) AddConnection(username string, conn *Conn) error {
	m.mu.Lock()
	userConnections, ok := m.connections[username]
	if !ok {
		userConnections = make(map[*Conn]struct{})
		m.connections[username] = userConnections
	}
	_, exists := userConnections[conn]
	userConnections[conn] = struct{}{}
	m.mu.Unlock()
	if exists {
		return nil
	}
	conn.setOnClose(func() { m.RemoveConnection(username, conn) })
	m.logger.Info("user connected",
		clog.String("username", username),
		clog.String("remote_addr", conn.RemoteAddr()))

	// 记录新连接并更新在线数
	observability.RecordWebSocketConnectionEstablished(context.Background())
	m.OnlineCount()

	// A user is online while any of their devices is connected. Only publish the
	// presence transition for the first device.
	if !ok && m.onConnect != nil {
		if err := m.onConnect(username, conn.RemoteAddr()); err != nil {
			m.logger.Error("failed to notify user online",
				clog.String("username", username),
				clog.Error(err))
			m.removeConnection(username, conn, false)
			_ = conn.Close()
			return err
		}
	}

	return nil
}

// RemoveConnection 移除连接
func (m *Manager) RemoveConnection(username string, conn *Conn) {
	m.removeConnection(username, conn, true)
}

func (m *Manager) removeConnection(username string, conn *Conn, notify bool) {
	m.mu.Lock()
	userConnections, ok := m.connections[username]
	if !ok {
		m.mu.Unlock()
		return
	}
	if _, ok = userConnections[conn]; !ok {
		m.mu.Unlock()
		return
	}
	delete(userConnections, conn)
	lastConnection := len(userConnections) == 0
	if lastConnection {
		delete(m.connections, username)
	}
	m.mu.Unlock()

	m.logger.Info("user device disconnected", clog.String("username", username))
	m.OnlineCount()
	if notify && lastConnection && m.onDisconnect != nil {
		if err := m.onDisconnect(username); err != nil {
			m.logger.Error("failed to notify user offline",
				clog.String("username", username),
				clog.Error(err))
		}
	}
}

// GetConnection 获取连接
func (m *Manager) GetConnection(username string) (*Conn, bool) {
	connections := m.GetConnections(username)
	if len(connections) > 0 {
		return connections[0], true
	}
	return nil, false
}

// GetConnections returns a stable snapshot of all connected devices for a user.
func (m *Manager) GetConnections(username string) []*Conn {
	m.mu.RLock()
	defer m.mu.RUnlock()
	userConnections := m.connections[username]
	connections := make([]*Conn, 0, len(userConnections))
	for conn := range userConnections {
		connections = append(connections, conn)
	}
	return connections
}

// SendToUser 发送消息给指定用户
func (m *Manager) SendToUser(username string, packet *gatewayv1.WsPacket) error {
	connections := m.GetConnections(username)
	if len(connections) == 0 {
		return fmt.Errorf("user not connected: %s", username)
	}
	var sendErr error
	for _, conn := range connections {
		if err := conn.Send(packet); err != nil {
			sendErr = fmt.Errorf("send to device: %w", err)
		}
	}
	return sendErr
}

// Broadcast 广播消息给所有在线用户
func (m *Manager) Broadcast(packet *gatewayv1.WsPacket) {
	m.mu.RLock()
	connections := make(map[string][]*Conn, len(m.connections))
	for username, userConnections := range m.connections {
		for conn := range userConnections {
			connections[username] = append(connections[username], conn)
		}
	}
	m.mu.RUnlock()
	for username, userConnections := range connections {
		for _, conn := range userConnections {
			if err := conn.Send(packet); err != nil {
				m.logger.Error("failed to broadcast message",
					clog.String("username", username),
					clog.Error(err))
			}
		}
	}
}

// OnlineCount 获取在线用户数
func (m *Manager) OnlineCount() int {
	m.mu.RLock()
	count := 0
	for _, userConnections := range m.connections {
		count += len(userConnections)
	}
	m.mu.RUnlock()
	// 更新可观测性指标
	observability.SetWebSocketConnectionsActive(context.Background(), count)
	return count
}

// Close 关闭所有连接
func (m *Manager) Close() error {
	m.mu.RLock()
	connections := make([]*Conn, 0)
	for _, userConnections := range m.connections {
		for conn := range userConnections {
			connections = append(connections, conn)
		}
	}
	m.mu.RUnlock()
	for _, conn := range connections {
		_ = conn.Close()
	}
	return nil
}

// Upgrader 获取 WebSocket 升级器
func (m *Manager) Upgrader() *websocket.Upgrader {
	return m.upgrader
}

// SetUpgrader 设置 WebSocket 升级器
func (m *Manager) SetUpgrader(upgrader *websocket.Upgrader) {
	m.upgrader = upgrader
}
