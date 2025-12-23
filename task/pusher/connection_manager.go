package pusher

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/registry"
	"github.com/ceyewan/genesis/xerrors"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	// ErrGatewayNotFound Gateway 服务未找到
	ErrGatewayNotFound = xerrors.New("gateway not found")
	// ErrNoAvailableEndpoint 没有可用的服务端点
	ErrNoAvailableEndpoint = xerrors.New("no available endpoint")
)

// GatewayConn 单个 Gateway 的连接
type GatewayConn struct {
	gatewayID string
	instance  *registry.ServiceInstance // 注册的实例信息
	conn      *grpc.ClientConn
	client    gatewayv1.PushServiceClient
	stream    grpc.BidiStreamingClient[gatewayv1.PushMessageRequest, gatewayv1.PushMessageResponse]
	mu        sync.Mutex
	logger    clog.Logger
	createdAt time.Time
	lastUsed  time.Time
}

// ConnectionManager 管理 gatewayID -> gRPC 连接的映射
type ConnectionManager struct {
	registry registry.Registry       // 服务发现
	service  string                  // Gateway 服务名
	clients  map[string]*GatewayConn // gatewayID -> 连接
	mu       sync.RWMutex
	logger   clog.Logger
}

// NewConnectionManager 创建连接管理器
func NewConnectionManager(reg registry.Registry, service string, logger clog.Logger) *ConnectionManager {
	return &ConnectionManager{
		registry: reg,
		service:  service,
		clients:  make(map[string]*GatewayConn),
		logger:   logger.WithNamespace("conn_mgr"),
	}
}

// Push 推送消息到指定 Gateway
func (cm *ConnectionManager) Push(ctx context.Context, gatewayID, username string, msg *gatewayv1.PushMessage) error {
	conn, err := cm.getOrCreateConn(ctx, gatewayID)
	if err != nil {
		return err
	}

	return conn.push(ctx, username, msg)
}

// getOrCreateConn 获取或创建到 Gateway 的连接
func (cm *ConnectionManager) getOrCreateConn(ctx context.Context, gatewayID string) (*GatewayConn, error) {
	// 先尝试从缓存获取
	cm.mu.RLock()
	conn, ok := cm.clients[gatewayID]
	cm.mu.RUnlock()

	if ok && conn.isHealthy() {
		conn.lastUsed = time.Now()
		return conn, nil
	}

	// 需要创建新连接
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 双重检查
	if conn, ok := cm.clients[gatewayID]; ok && conn.isHealthy() {
		conn.lastUsed = time.Now()
		return conn, nil
	}

	// 查找 Gateway 实例
	instance, err := cm.findGatewayInstance(ctx, gatewayID)
	if err != nil {
		return nil, err
	}

	// 创建新连接
	newConn, err := cm.createConn(instance)
	if err != nil {
		return nil, xerrors.Wrapf(err, "failed to create connection to gateway %s", gatewayID)
	}

	// 关闭旧连接（如果存在）
	if oldConn, ok := cm.clients[gatewayID]; ok {
		go oldConn.close()
	}

	cm.clients[gatewayID] = newConn
	return newConn, nil
}

// findGatewayInstance 在注册中心查找指定 gatewayID 的实例
func (cm *ConnectionManager) findGatewayInstance(ctx context.Context, gatewayID string) (*registry.ServiceInstance, error) {
	instances, err := cm.registry.GetService(ctx, cm.service)
	if err != nil {
		return nil, xerrors.Wrapf(err, "failed to get service %s from registry", cm.service)
	}

	if len(instances) == 0 {
		return nil, xerrors.Wrapf(ErrGatewayNotFound, "no instances found for service %s", cm.service)
	}

	// 遍历实例，查找匹配的 gateway_id
	for _, inst := range instances {
		if inst.Metadata != nil {
			if gid, ok := inst.Metadata["gateway_id"]; ok && gid == gatewayID {
				return inst, nil
			}
		}
	}

	return nil, xerrors.Wrapf(ErrGatewayNotFound, "gateway %s not found in service %s", gatewayID, cm.service)
}

// createConn 创建到 Gateway 的连接
func (cm *ConnectionManager) createConn(instance *registry.ServiceInstance) (*GatewayConn, error) {
	// 获取可用的 gRPC 端点
	grpcAddr, err := extractGRPCAddress(instance.Endpoints)
	if err != nil {
		return nil, err
	}

	// 建立 gRPC 连接
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, xerrors.Wrapf(err, "failed to create gRPC client to %s", grpcAddr)
	}

	gatewayID := instance.Metadata["gateway_id"]
	gatewayConn := &GatewayConn{
		gatewayID: gatewayID,
		instance:  instance,
		conn:      conn,
		client:    gatewayv1.NewPushServiceClient(conn),
		createdAt: time.Now(),
		lastUsed:  time.Now(),
		logger:    cm.logger.With(clog.String("gateway", gatewayID), clog.String("addr", grpcAddr)),
	}

	// 初始化双向流
	if err := gatewayConn.initStream(context.Background()); err != nil {
		conn.Close()
		return nil, err
	}

	// 启动接收响应的协程
	go gatewayConn.receiveResponses()

	cm.logger.Info("created new gateway connection",
		clog.String("gateway_id", gatewayConn.gatewayID),
		clog.String("addr", grpcAddr))

	return gatewayConn, nil
}

// initStream 初始化双向流
func (c *GatewayConn) initStream(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	stream, err := c.client.PushMessage(ctx)
	if err != nil {
		return err
	}
	c.stream = stream
	return nil
}

// receiveResponses 接收推送响应
func (c *GatewayConn) receiveResponses() {
	for {
		c.mu.Lock()
		stream := c.stream
		c.mu.Unlock()

		if stream == nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		resp, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				c.logger.Error("gateway closed stream", clog.Error(err))
			} else {
				c.logger.Error("failed to receive push response", clog.Error(err))
			}
			// 尝试重连
			c.initStream(context.Background())
			continue
		}

		if resp.Error != "" {
			c.logger.Warn("push failed",
				clog.Int64("msg_id", resp.MsgId),
				clog.String("error", resp.Error))
		} else {
			c.logger.Debug("push success", clog.Int64("msg_id", resp.MsgId))
		}
	}
}

// push 推送消息
func (c *GatewayConn) push(ctx context.Context, username string, msg *gatewayv1.PushMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	req := &gatewayv1.PushMessageRequest{
		ToUsername: username,
		Message:    msg,
	}

	if c.stream == nil {
		// 流已断开，尝试重新建立
		if err := c.initStream(ctx); err != nil {
			return xerrors.Wrapf(err, "failed to recreate stream")
		}
	}

	if err := c.stream.Send(req); err != nil {
		c.logger.Error("failed to send push request", clog.Error(err))
		return xerrors.Wrapf(err, "failed to send message to gateway %s", c.gatewayID)
	}

	c.lastUsed = time.Now()
	return nil
}

// isHealthy 检查连接是否健康
func (c *GatewayConn) isHealthy() bool {
	if c.conn == nil || c.client == nil || c.stream == nil {
		return false
	}
	// 检查连接是否超时未使用（5分钟）
	return time.Since(c.lastUsed) < 5*time.Minute
}

// close 关闭连接
func (c *GatewayConn) close() error {
	c.logger.Info("closing gateway connection", clog.String("gateway_id", c.gatewayID))

	if c.stream != nil {
		c.stream.CloseSend()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Close 关闭所有连接
func (cm *ConnectionManager) Close() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for _, conn := range cm.clients {
		go conn.close()
	}

	cm.clients = make(map[string]*GatewayConn)
	return nil
}

// extractGRPCAddress 从端点列表中提取 gRPC 地址
func extractGRPCAddress(endpoints []string) (string, error) {
	for _, ep := range endpoints {
		// 支持 grpc:// 前缀或纯地址
		addr := strings.TrimPrefix(ep, "grpc://")
		if addr != "" && addr != ep {
			return addr, nil
		}
		// 简单的地址格式检查
		if strings.Contains(addr, ":") {
			return addr, nil
		}
	}
	return "", ErrNoAvailableEndpoint
}
