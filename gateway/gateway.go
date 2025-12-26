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
	"github.com/ceyewan/resonance/gateway/client"
	"github.com/ceyewan/resonance/gateway/config"
	"github.com/ceyewan/resonance/gateway/connection"
	"github.com/ceyewan/resonance/gateway/handler"
	"github.com/ceyewan/resonance/gateway/push"
	"github.com/ceyewan/resonance/gateway/server"
	"github.com/ceyewan/resonance/gateway/socket"
	"github.com/ceyewan/resonance/gateway/utils"
)

// Gateway 网关服务生命周期管理器
type Gateway struct {
	config    *config.Config
	logger    clog.Logger
	registry  registry.Registry
	serviceID string

	// 服务实例
	httpServer *server.HTTPServer
	wsServer   *server.WSServer
	grpcServer *server.GRPCServer

	// 核心资源
	resources *resources
	ctx       context.Context
	cancel    context.CancelFunc
}

// resources 内部资源聚合，方便统一管理
type resources struct {
	etcdConn    connector.EtcdConnector
	logicClient *client.Client
	connMgr     *connection.Manager
}

// New 创建 Gateway 实例
func New() (*Gateway, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	g := &Gateway{
		config: cfg,
		ctx:    ctx,
		cancel: cancel,
	}

	if err := g.initComponents(); err != nil {
		g.Close()
		return nil, err
	}

	return g, nil
}

// initComponents 初始化所有组件
func (g *Gateway) initComponents() error {
	// 1. 基础组件
	logger, _ := clog.New(&g.config.Log, clog.WithStandardContext())
	g.logger = logger
	idGen := idgen.NewUUID(idgen.WithUUIDVersion("v7"))
	g.serviceID = g.config.Service.Name + "-001"

	// 2. 初始化核心资源 (Etcd, Clients, Managers)
	res, err := g.initResources()
	if err != nil {
		return err
	}
	g.resources = res

	// 3. 初始化服务接口 (Servers)
	g.initServers(idGen)

	return nil
}

// initResources 初始化外部连接和管理对象
func (g *Gateway) initResources() (*resources, error) {
	// Etcd
	etcdConn, err := connector.NewEtcd(&g.config.Etcd, connector.WithLogger(g.logger))
	if err != nil {
		return nil, fmt.Errorf("etcd init: %w", err)
	}
	if err := etcdConn.Connect(g.ctx); err != nil {
		return nil, fmt.Errorf("etcd connect: %w", err)
	}

	// Registry
	reg, err := registry.New(etcdConn, g.config.Registry.ToRegistryConfig(), registry.WithLogger(g.logger))
	if err != nil {
		return nil, fmt.Errorf("registry init: %w", err)
	}
	g.registry = reg

	// Logic Client
	logicClient, err := client.NewClient(g.config.LogicAddr, g.config.Service.Name, g.logger, reg)
	if err != nil {
		return nil, fmt.Errorf("logic client init: %w", err)
	}

	// Connection Manager
	presence := connection.NewPresenceCallback(logicClient, g.logger)
	connMgr := connection.NewManager(g.logger, nil, presence.OnUserOnline, presence.OnUserOffline)

	return &resources{
		etcdConn:    etcdConn,
		logicClient: logicClient,
		connMgr:     connMgr,
	}, nil
}

// initServers 初始化各个协议的服务端
func (g *Gateway) initServers(idGen idgen.Generator) {
	// WebSocket Handler
	dispatcher := socket.NewDispatcher(g.logger, g.resources.logicClient)
	wsHandler := socket.NewHandler(g.logger, g.resources.logicClient, g.resources.connMgr, dispatcher, idGen, g.config.WSConfig)
	g.resources.connMgr.SetUpgrader(wsHandler.Upgrader())

	// HTTP Handler & Middlewares
	limiter, _ := ratelimit.NewStandalone(nil, ratelimit.WithLogger(g.logger))
	middlewares := handler.NewMiddlewares(g.logger, limiter, idGen)
	apiHandler := handler.NewHandler(g.resources.logicClient, g.logger)

	// Push Service
	pushService := push.NewService(g.resources.connMgr, g.logger)

	// Servers
	g.httpServer = server.NewHTTPServer(g.config, g.logger, apiHandler, middlewares)
	g.wsServer = server.NewWSServer(g.config, g.logger, wsHandler)
	g.grpcServer = server.NewGRPCServer(":9091", g.logger, pushService)
}

// Run 启动所有服务并注册
func (g *Gateway) Run() error {
	g.logger.Info("starting gateway servers...")

	go g.grpcServer.Start()
	go g.httpServer.Start()
	go g.wsServer.Start()

	return g.registerService()
}

// registerService 注册服务实例
func (g *Gateway) registerService() error {
	service := &registry.ServiceInstance{
		ID:      g.serviceID,
		Name:    g.config.Service.Name,
		Version: "1.0.0",
		Endpoints: []string{
			"grpc://" + utils.GetLocalIP() + ":9091",
		},
		Metadata: map[string]string{
			"http_addr": g.config.Service.HTTPAddr,
			"ws_addr":   g.config.Service.WSAddr,
		},
	}

	return g.registry.Register(g.ctx, service, g.config.Registry.DefaultTTL)
}

// Close 优雅关闭资源
func (g *Gateway) Close() error {
	g.logger.Info("shutting down gateway...")
	g.cancel()

	// 1. 注销服务
	if g.registry != nil {
		g.registry.Deregister(context.Background(), g.serviceID)
		g.registry.Close()
	}

	// 2. 停止服务实例
	if g.grpcServer != nil {
		g.grpcServer.Stop()
	}

	ctx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()

	if g.httpServer != nil {
		g.httpServer.Stop(ctx)
	}
	if g.wsServer != nil {
		g.wsServer.Stop(ctx)
	}

	// 3. 释放核心资源
	if g.resources != nil {
		g.resources.connMgr.Close()
		g.resources.logicClient.Close()
		g.resources.etcdConn.Close()
	}

	return nil
}
