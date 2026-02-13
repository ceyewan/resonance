package pusher

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/registry"
)

// Manager 管理所有 Gateway 的连接
type Manager struct {
	logger             clog.Logger
	registry           registry.Registry
	gatewayServiceName string
	queueSize          int           // 每个 Gateway 的队列大小
	pusherCount        int           // 每个 Gateway 的并发推送协程数
	pollInterval       time.Duration // 服务发现轮询间隔

	clients map[string]*GatewayClient // gatewayID -> Client
	mu      sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
}

// NewManager 创建 Pusher Manager
func NewManager(logger clog.Logger, reg registry.Registry, serviceName string, queueSize int, pusherCount int, pollInterval time.Duration) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		logger:             logger,
		registry:           reg,
		gatewayServiceName: serviceName,
		queueSize:          queueSize,
		pusherCount:        pusherCount,
		pollInterval:       pollInterval,
		clients:            make(map[string]*GatewayClient),
		ctx:                ctx,
		cancel:             cancel,
	}
}

// Start 开始监听服务变动
func (m *Manager) Start() error {
	// 1. 首次获取所有服务实例
	if err := m.syncServices(); err != nil {
		return err
	}

	// 2. 启动轮询
	go m.poll()

	return nil
}

// poll 定期轮询服务变动
// TODO: P3 - 考虑使用 registry.Watch 替代轮询
// Genesis registry.Registry 接口提供了 Watch 方法可以实时监听服务变化：
//
//	Watch(ctx context.Context, serviceName string) (<-chan ServiceEvent, error)
//
// 优势：
//   - 实时响应服务上下线，无需等待 pollInterval
//   - 减少不必要的 Etcd 查询
//
// 劣势：
//   - 需要处理断线重连、事件乱序等复杂场景
//   - 轮询模式更简单、更可靠
//
// 当前使用轮询的原因：
//   - Gateway 下线容忍度较高（用户可能重连）
//   - 轮询间隔 10s 可接受
//   - 实现简单，不容易出 bug
//
// 未来改进：可以使用混合模式
//   - 主 Watch + 兜底轮询（如 30s 无事件则主动查询一次）
func (m *Manager) poll() {
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if err := m.syncServices(); err != nil {
				m.logger.Error("failed to sync services", clog.Error(err))
			}
		}
	}
}

// syncServices 同步服务列表
func (m *Manager) syncServices() error {
	services, err := m.registry.GetService(m.ctx, m.gatewayServiceName)
	if err != nil {
		if err == registry.ErrServiceNotFound {
			return nil
		}
		return fmt.Errorf("failed to get services: %w", err)
	}

	// 收集当前活跃的 ID
	activeIDs := make(map[string]bool)
	for _, svc := range services {
		activeIDs[svc.ID] = true
		m.addClient(svc)
	}

	// 清理已下线的连接
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, client := range m.clients {
		if !activeIDs[id] {
			m.logger.Info("removing offline gateway client", clog.String("id", id))
			if err := client.Close(); err != nil {
				m.logger.Error("failed to close client", clog.String("id", id), clog.Error(err))
			}
			delete(m.clients, id)
		}
	}

	return nil
}

// addClient 添加或更新 Gateway 客户端
func (m *Manager) addClient(svc *registry.ServiceInstance) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查是否已存在
	if _, exists := m.clients[svc.ID]; exists {
		return
	}

	// 解析地址 (假设第一个 Endpoint 是 gRPC 地址)
	if len(svc.Endpoints) == 0 {
		m.logger.Warn("gateway service has no endpoints", clog.String("id", svc.ID))
		return
	}
	// endpoints: ["grpc://127.0.0.1:9091"]
	addr := strings.TrimPrefix(svc.Endpoints[0], "grpc://")

	client, err := NewClient(addr, svc.ID, m.queueSize, m.pusherCount, m.logger)
	if err != nil {
		m.logger.Error("failed to create gateway client",
			clog.String("id", svc.ID),
			clog.String("addr", addr),
			clog.Error(err))
		return
	}

	m.clients[svc.ID] = client
	m.logger.Info("gateway client connected",
		clog.String("id", svc.ID),
		clog.String("addr", addr),
		clog.Int("queue_size", m.queueSize),
		clog.Int("pusher_count", m.pusherCount))
}

// GetClient 获取指定 Gateway 的客户端
func (m *Manager) GetClient(gatewayID string) (*GatewayClient, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	client, ok := m.clients[gatewayID]
	if !ok {
		return nil, fmt.Errorf("gateway client not found: %s", gatewayID)
	}
	return client, nil
}

// Close 关闭所有连接
func (m *Manager) Close() {
	m.cancel()

	m.mu.Lock()
	defer m.mu.Unlock()

	for id, client := range m.clients {
		if err := client.Close(); err != nil {
			m.logger.Error("failed to close client", clog.String("id", id), clog.Error(err))
		}
	}
	m.clients = nil
}
