package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/registry"
	"github.com/ceyewan/resonance/gateway/api"
	"github.com/ceyewan/resonance/gateway/client"
	"github.com/ceyewan/resonance/gateway/connection"
	"github.com/ceyewan/resonance/gateway/push"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

// Gateway 网关服务
type Gateway struct {
	config       *Config                 // 配置
	logger       clog.Logger             // 日志
	logicClient  *client.Client          // Logic RPC 客户端
	connMgr      *connection.Manager     // 连接管理器
	apiHandler   *api.Handler            // API Handler
	middlewares  *api.Middlewares        // HTTP 中间件
	wsHandler    *api.WebSocket          // WebSocket 处理器
	pushService  *push.Service           // Push RPC 服务端
	httpServer   *http.Server            // HTTP 服务器
	wsServer     *http.Server            // WebSocket 服务器
	grpcServer   *grpc.Server            // gRPC 服务器
	etcdConn     connector.EtcdConnector  // Etcd 连接
	registry     registry.Registry        // 服务注册
	serviceID    string                  // 服务实例 ID
	ctx          context.Context         // 上下文
	cancel       context.CancelFunc       // 取消函数
}

// New 创建 Gateway 实例
func New() (*Gateway, error) {
	// 加载配置
	cfg, err := Load()
	if err != nil {
		return nil, err
	}

	// 初始化日志（启用标准 Context 提取，自动输出 trace_id）
	logger, err := clog.New(&cfg.Log, clog.WithStandardContext())
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	// 初始化 ID 生成器（UUID v7，时间有序适合链路追踪）
	idgen, err := idgen.NewUUID(&idgen.UUIDConfig{Version: "v7"}, idgen.WithLogger(logger))
	if err != nil {
		return nil, fmt.Errorf("failed to create idgen: %w", err)
	}

	// 创建上下文
	ctx, cancel := context.WithCancel(context.Background())

	// 初始化 Etcd 连接
	etcdConn, err := connector.NewEtcd(&cfg.Etcd, connector.WithLogger(logger))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create etcd connection: %w", err)
	}

	// 连接 Etcd
	if err := etcdConn.Connect(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to connect etcd: %w", err)
	}

	// 初始化服务注册
	reg, err := registry.New(etcdConn, cfg.Registry.ToRegistryConfig(), registry.WithLogger(logger))
	if err != nil {
		cancel()
		etcdConn.Close()
		return nil, fmt.Errorf("failed to create registry: %w", err)
	}

	// 初始化 Logic 客户端（使用服务发现）
	logicClient, err := client.NewClient(cfg.LogicAddr, cfg.Service.Name, logger, reg)
	if err != nil {
		cancel()
		reg.Close()
		etcdConn.Close()
		return nil, fmt.Errorf("failed to create logic client: %w", err)
	}

	// 初始化 API Handler 和中间件（传入 idgen）
	apiHandler, middlewares, err := api.NewHandlerWithMiddlewares(logicClient, logger, idgen)
	if err != nil {
		cancel()
		logicClient.Close()
		reg.Close()
		etcdConn.Close()
		return nil, fmt.Errorf("failed to create api handler: %w", err)
	}

	// 初始化 WebSocket 组件（包含连接管理器和上下线上报）
	wsHandler, connMgr := api.NewWebSocketComponents(logicClient, logger, idgen)

	// 初始化 Push Service
	pushService := push.NewService(connMgr, logger)

	// 服务实例 ID（使用固定编号 001）
	serviceID := cfg.Service.Name + "-001"

	return &Gateway{
		config:      cfg,
		logger:      logger,
		logicClient: logicClient,
		connMgr:     connMgr,
		apiHandler:  apiHandler,
		middlewares: middlewares,
		wsHandler:   wsHandler,
		pushService: pushService,
		etcdConn:    etcdConn,
		registry:    reg,
		serviceID:   serviceID,
		ctx:         ctx,
		cancel:      cancel,
	}, nil
}

// Run 启动 Gateway 服务
func (g *Gateway) Run() error {
	g.logger.Info("starting gateway service",
		clog.String("gateway_name", g.config.Service.Name),
		clog.String("http_addr", g.config.Service.HTTPAddr),
		clog.String("ws_addr", g.config.Service.WSAddr),
		clog.String("grpc_addr", ":9091"))

	// 启动服务
	go g.startGRPCServer()
	go g.startHTTPServer()
	go g.startWSServer()

	// 注册服务
	if err := g.registerService(); err != nil {
		g.logger.Error("failed to register service", clog.Error(err))
		return err
	}

	return nil
}

// registerService 注册服务到 Etcd
func (g *Gateway) registerService() error {
	// 获取本机 IP（这里简化处理，使用配置中的地址）
	// TODO: 生产环境应该自动获取本机 IP

	service := &registry.ServiceInstance{
		ID:      g.serviceID,
		Name:    g.config.Service.Name,
		Version: "1.0.0",
		Endpoints: []string{
			"grpc://" + getLocalIP() + ":9091", // Push 服务 gRPC 地址
		},
		Metadata: map[string]string{
			"http_addr": g.config.Service.HTTPAddr,
			"ws_addr":   g.config.Service.WSAddr,
		},
	}

	if err := g.registry.Register(g.ctx, service, g.config.Registry.DefaultTTL); err != nil {
		return fmt.Errorf("register service failed: %w", err)
	}

	g.logger.Info("service registered",
		clog.String("service_id", g.serviceID),
		clog.String("service_name", g.config.Service.Name),
		clog.Any("endpoints", service.Endpoints))

	return nil
}

// getLocalIP 获取本机 IP 地址
func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "127.0.0.1"
}

// startGRPCServer 启动 gRPC 服务
func (g *Gateway) startGRPCServer() {
	// 创建 gRPC 服务器，添加链路追踪拦截器
	g.grpcServer = grpc.NewServer(
		grpc.ChainUnaryInterceptor(push.TraceUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(push.TraceStreamServerInterceptor()),
	)
	g.pushService.RegisterGRPC(g.grpcServer)

	lis, err := net.Listen("tcp", ":9091")
	if err != nil {
		g.logger.Error("failed to listen on grpc port", clog.Error(err))
		return
	}

	g.logger.Info("grpc server started", clog.String("addr", ":9091"))
	if err := g.grpcServer.Serve(lis); err != nil {
		g.logger.Error("grpc server error", clog.Error(err))
	}
}

// startHTTPServer 启动 HTTP 服务
func (g *Gateway) startHTTPServer() {
	router := gin.New()

	// 应用中间件
	router.Use(g.middlewares.Recovery)
	router.Use(g.middlewares.Logger)
	router.Use(g.middlewares.SlowQuery)
	router.Use(g.middlewares.GlobalIP)

	// 注册 API 路由
	g.apiHandler.RegisterRoutes(router, g.middlewares.RouteOptions()...)

	// 健康检查
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":       "ok",
			"gateway_name": g.config.Service.Name,
			"online_count": g.connMgr.OnlineCount(),
		})
	})

	g.httpServer = &http.Server{
		Addr:    g.config.Service.HTTPAddr,
		Handler: router,
	}

	g.logger.Info("http server started", clog.String("addr", g.config.Service.HTTPAddr))
	if err := g.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		g.logger.Error("http server error", clog.Error(err))
	}
}

// startWSServer 启动 WebSocket 服务
func (g *Gateway) startWSServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", g.wsHandler.HandleWebSocket)

	g.wsServer = &http.Server{
		Addr:    g.config.Service.WSAddr,
		Handler: mux,
	}

	g.logger.Info("websocket server started", clog.String("addr", g.config.Service.WSAddr))
	if err := g.wsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		g.logger.Error("websocket server error", clog.Error(err))
	}
}

// Close 关闭 Gateway 服务
func (g *Gateway) Close() error {
	g.logger.Info("shutting down gateway service")
	g.cancel()

	// 注销服务
	if g.registry != nil {
		if err := g.registry.Deregister(context.Background(), g.serviceID); err != nil {
			g.logger.Error("failed to deregister service", clog.Error(err))
		} else {
			g.logger.Info("service deregistered", clog.String("service_id", g.serviceID))
		}
		g.registry.Close()
	}

	if g.grpcServer != nil {
		g.grpcServer.GracefulStop()
	}

	if g.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		g.httpServer.Shutdown(ctx)
	}

	if g.wsServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		g.wsServer.Shutdown(ctx)
	}

	if g.connMgr != nil {
		g.connMgr.Close()
	}

	if g.logicClient != nil {
		g.logicClient.Close()
	}

	if g.etcdConn != nil {
		g.etcdConn.Close()
	}

	g.logger.Info("gateway service stopped")
	return nil
}
