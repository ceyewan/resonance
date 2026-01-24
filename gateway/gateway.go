package gateway

import (
	"context"
	"fmt"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/ratelimit"
	"github.com/ceyewan/genesis/registry"
	"github.com/ceyewan/genesis/trace"
	"github.com/ceyewan/resonance/gateway/client"
	"github.com/ceyewan/resonance/gateway/config"
	"github.com/ceyewan/resonance/gateway/connection"
	"github.com/ceyewan/resonance/gateway/handler"
	"github.com/ceyewan/resonance/gateway/push"
	"github.com/ceyewan/resonance/gateway/server"
	"github.com/ceyewan/resonance/gateway/socket"
)

// Gateway 网关服务生命周期管理器
type Gateway struct {
	config    *config.Config
	logger    clog.Logger
	registry  registry.Registry
	gatewayID string // 唯一服务实例 ID，例如 gateway-service-001
	workerID  int64  // 唯一 worker 实例 ID，例如 001, 002 等

	// 服务实例
	httpServer *server.HTTPServer
	grpcServer *server.GRPCServer

	// 核心资源
	resources *resources
	ctx       context.Context
	cancel    context.CancelFunc

	// workerID 保活停止函数
	stopWorkerIDKeepAlive func()

	// trace 关闭函数
	traceShutdown func(context.Context) error
}

// resources 内部资源聚合，方便统一管理
type resources struct {
	redisConn   connector.RedisConnector
	etcdConn    connector.EtcdConnector
	logicClient *client.Client
	connMgr     *connection.Manager
}

// New 创建 Gateway 实例
func New() (*Gateway, error) {
	// 加载配置
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	// 创建上下文
	ctx, cancel := context.WithCancel(context.Background())
	g := &Gateway{
		config: cfg,
		ctx:    ctx,
		cancel: cancel,
	}
	// 初始化各个组件
	if err := g.initComponents(); err != nil {
		g.Close()
		return nil, err
	}

	return g, nil
}

// initComponents 初始化所有组件
func (g *Gateway) initComponents() error {
	// 1. 基础组件
	logger, _ := clog.New(&g.config.Log, clog.WithStandardContext(), clog.WithNamespace("gateway"))
	g.logger = logger

	// 2. 初始化核心资源 (Redis, Etcd, Registry)
	res, err := g.initBaseResources()
	if err != nil {
		return err
	}
	g.resources = res

	// 3. 初始化 Trace (使用 Discard，仅生成 TraceID 不上报)
	traceShutdown, err := trace.Discard(g.config.Service.Name)
	if err != nil {
		return fmt.Errorf("init trace: %w", err)
	}
	g.traceShutdown = traceShutdown

	// 4. 使用 Allocator 从 Redis 获取唯一的 workerID
	allocator, err := idgen.NewAllocator(&idgen.AllocatorConfig{
		Driver: "redis",
		MaxID:  g.config.WorkerID.GetMaxID(),
	}, idgen.WithRedisConnector(res.redisConn))
	if err != nil {
		return fmt.Errorf("create allocator: %w", err)
	}
	workerID, err := allocator.Allocate(g.ctx)
	if err != nil {
		return fmt.Errorf("allocate workerID: %w", err)
	}
	g.workerID = workerID

	// 监听 workerID 保活失败
	go func() {
		if err := <-allocator.KeepAlive(g.ctx); err != nil {
			g.logger.Error("workerID keepalive failed, shutting down", clog.String("error", err.Error()))
			g.cancel()
		}
	}()

	// 5. 拼接唯一服务 ID (基于 workerID)
	g.gatewayID = fmt.Sprintf("%s-%03d", g.config.Service.Name, g.workerID)

	// 6. 初始化逻辑客户端与连接管理器（依赖 gatewayID）
	if err := g.initLogicDependencies(); err != nil {
		return err
	}

	// 7. 创建 ID 生成器 (供其他组件使用)
	idGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: workerID})
	if err != nil {
		return fmt.Errorf("create id generator: %w", err)
	}

	// 8. 初始化服务接口 (Servers)
	g.initServers(idGen)

	return nil
}

// initBaseResources 初始化外部连接 (Redis、Etcd、Registry)
func (g *Gateway) initBaseResources() (*resources, error) {
	// Redis
	redisConn, err := connector.NewRedis(&g.config.Redis, connector.WithLogger(g.logger))
	if err != nil {
		return nil, fmt.Errorf("redis init: %w", err)
	}
	if err := redisConn.Connect(g.ctx); err != nil {
		return nil, fmt.Errorf("redis connect: %w", err)
	}

	// Etcd
	etcdConn, err := connector.NewEtcd(&g.config.Etcd, connector.WithLogger(g.logger))
	if err != nil {
		redisConn.Close()
		return nil, fmt.Errorf("etcd init: %w", err)
	}
	if err := etcdConn.Connect(g.ctx); err != nil {
		redisConn.Close()
		return nil, fmt.Errorf("etcd connect: %w", err)
	}

	// Registry
	reg, err := registry.New(etcdConn, g.config.Registry.ToRegistryConfig(), registry.WithLogger(g.logger))
	if err != nil {
		redisConn.Close()
		etcdConn.Close()
		return nil, fmt.Errorf("registry init: %w", err)
	}
	g.registry = reg

	return &resources{
		redisConn: redisConn,
		etcdConn:  etcdConn,
	}, nil
}

// initLogicDependencies 基于 gatewayID 初始化 Logic Client 与连接管理
func (g *Gateway) initLogicDependencies() error {
	if g.gatewayID == "" {
		return fmt.Errorf("gatewayID not initialized")
	}

	logicClient, err := client.NewClient(g.config.GetLogicServiceName(), g.gatewayID, g.logger, g.registry)
	if err != nil {
		return fmt.Errorf("logic client init: %w", err)
	}

	presence := connection.NewPresenceCallback(logicClient, g.logger)
	connMgr := connection.NewManager(g.logger, nil, presence.OnUserOnline, presence.OnUserOffline)

	g.resources.logicClient = logicClient
	g.resources.connMgr = connMgr

	return nil
}

// initServers 初始化各个协议的服务端
func (g *Gateway) initServers(idGen idgen.Generator) {
	// WebSocket Handler
	dispatcher := socket.NewDispatcher(g.logger, g.resources.logicClient)
	wsHandler := socket.NewHandler(g.logger, g.resources.connMgr, dispatcher, idGen, g.config.WSConfig)
	g.resources.connMgr.SetUpgrader(wsHandler.Upgrader())

	// HTTP Handler & Middlewares
	limiter, _ := ratelimit.New(&ratelimit.Config{
		Driver: ratelimit.DriverStandalone,
	}, ratelimit.WithLogger(g.logger))
	middlewares := handler.NewMiddlewares(g.logger, limiter, idGen)
	apiHandler := handler.NewHandler(g.resources.logicClient, g.logger)

	// Push Service
	pushService := push.NewService(g.resources.connMgr, g.logger)

	// Servers
	g.httpServer = server.NewHTTPServer(g.config, g.logger, apiHandler, middlewares, wsHandler)
	g.grpcServer = server.NewGRPCServer(fmt.Sprintf(":%d", g.config.GetGRPCPort()), g.logger, pushService)
}

// Run 启动所有服务并注册
func (g *Gateway) Run() error {
	g.logger.Info("starting gateway servers...")

	go g.grpcServer.Start()
	go g.httpServer.Start()

	return g.registerService()
}

// registerService 注册服务实例
func (g *Gateway) registerService() error {
	host := g.config.GetHost()
	grpcEndpoint := fmt.Sprintf("grpc://%s:%d", host, g.config.GetGRPCPort())

	service := &registry.ServiceInstance{
		ID:      g.gatewayID,
		Name:    g.config.Service.Name,
		Version: "1.0.0",
		Endpoints: []string{
			grpcEndpoint,
		},
		Metadata: map[string]string{
			"http_addr": fmt.Sprintf("%s:%d", host, g.config.GetHTTPPort()),
			"ws_addr":   fmt.Sprintf("%s:%d", host, g.config.GetHTTPPort()),
		},
	}

	return g.registry.Register(g.ctx, service, g.config.Registry.DefaultTTL)
}

// Close 优雅关闭资源
func (g *Gateway) Close() error {
	g.logger.Info("shutting down gateway...")
	g.cancel()

	// 1. 停止 workerID 保活
	if g.stopWorkerIDKeepAlive != nil {
		g.stopWorkerIDKeepAlive()
	}

	// 2. 注销服务
	if g.registry != nil {
		g.registry.Deregister(context.Background(), g.gatewayID)
		g.registry.Close()
	}

	// 3. 停止服务实例
	if g.grpcServer != nil {
		g.grpcServer.Stop()
	}

	ctx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()

	if g.httpServer != nil {
		g.httpServer.Stop(ctx)
	}

	// 4. 释放核心资源
	if g.resources != nil {
		g.resources.connMgr.Close()
		g.resources.logicClient.Close()
		g.resources.redisConn.Close()
		g.resources.etcdConn.Close()
	}

	return nil
}
