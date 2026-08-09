package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ceyewan/genesis/auth"
	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/ratelimit"
	"github.com/ceyewan/genesis/registry"

	"github.com/ceyewan/resonance/gateway/config"
	"github.com/ceyewan/resonance/gateway/logicclient"
	"github.com/ceyewan/resonance/gateway/observability"
	"github.com/ceyewan/resonance/gateway/pushserver"
	"github.com/ceyewan/resonance/gateway/server"
	"github.com/ceyewan/resonance/gateway/transport/httpapi"
	"github.com/ceyewan/resonance/gateway/transport/ws"
	"github.com/ceyewan/resonance/pkg/health"
	"github.com/ceyewan/resonance/pkg/serviceauth"
)

// Gateway 网关服务生命周期管理器
type Gateway struct {
	config    *config.Config
	logger    clog.Logger
	registry  registry.Registry
	gatewayID string // 唯一服务实例 ID，例如 gateway-service-001
	workerID  int64  // 唯一 worker 实例 ID，例如 001, 002 等

	// 服务实例
	httpServer  *server.HTTPServer
	grpcServer  *server.GRPCServer
	healthProbe *health.Probe

	// 核心资源
	resources *resources
	ctx       context.Context
	cancel    context.CancelFunc

	// workerID 保活停止函数
	stopWorkerIDKeepAlive func() error
	closeOnce             sync.Once
	closeErr              error
}

// resources 内部资源聚合，方便统一管理
type resources struct {
	redisConn     connector.RedisConnector
	etcdConn      connector.EtcdConnector
	logicClient   *logicclient.Client
	connMgr       *ws.Manager
	authenticator auth.Authenticator
	userSigner    *serviceauth.Signer
	limiter       ratelimit.Limiter
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
		_ = g.Close()
		return nil, err
	}

	return g, nil
}

// initComponents 初始化所有组件
func (g *Gateway) initComponents() error {
	// 1. 初始化可观测性（Trace + Metrics）
	obsCfg := &observability.Config{
		Version: g.config.Observability.Version, InstanceID: g.config.Observability.InstanceID,
		Environment: g.config.Observability.Environment,
		Trace: observability.TraceConfig{
			Disable:  g.config.Observability.Trace.Disable,
			Endpoint: g.config.Observability.Trace.Endpoint,
			Insecure: g.config.Observability.Trace.Insecure,
			Sampler:  g.config.Observability.Trace.Sampler,
		},
		Metrics: observability.MetricsConfig{
			Port:          g.config.Observability.Metrics.Port,
			Path:          g.config.Observability.Metrics.Path,
			EnableRuntime: g.config.Observability.Metrics.EnableRuntime,
		},
	}
	if err := observability.Init(obsCfg); err != nil {
		g.logger = nil
		return fmt.Errorf("init observability: %w", err)
	}

	// 2. 初始化 Logger（带 Trace Context 支持）
	logger, err := observability.NewLogger(&g.config.Log)
	if err != nil {
		return fmt.Errorf("logger init: %w", err)
	}
	g.logger = logger
	authenticator, err := auth.New(&g.config.Auth, auth.WithLogger(logger))
	if err != nil {
		return fmt.Errorf("auth init: %w", err)
	}
	userSigner, err := serviceauth.NewSigner(
		g.config.ServiceAuth.GatewayServiceID,
		[]byte(g.config.ServiceAuth.GatewaySecret),
	)
	if err != nil {
		return fmt.Errorf("gateway service auth signer: %w", err)
	}

	// 3. 初始化核心资源 (Redis, Etcd, Registry)
	res, err := g.initBaseResources()
	if err != nil {
		return err
	}
	res.authenticator = authenticator
	res.userSigner = userSigner
	g.resources = res

	// 4. 使用 Allocator 从 Redis 获取唯一的 workerID
	allocator, err := idgen.NewAllocator(&idgen.AllocatorConfig{
		Driver:    idgen.DriverRedis,
		KeyPrefix: g.config.WorkerID.GetKey(),
		MaxID:     g.config.WorkerID.GetMaxID(),
	}, idgen.WithRedisConnector(res.redisConn))
	if err != nil {
		return fmt.Errorf("create allocator: %w", err)
	}
	workerID, err := allocator.Allocate(g.ctx)
	if err != nil {
		return errors.Join(fmt.Errorf("allocate workerID: %w", err), allocator.Stop())
	}
	g.workerID = workerID
	g.stopWorkerIDKeepAlive = allocator.Stop

	// 监听 workerID 保活失败
	go func() {
		if err, ok := <-allocator.KeepAlive(g.ctx); ok && err != nil {
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
	idGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{
		Mode:     idgen.GeneratorModeSingleDC,
		WorkerID: workerID,
	})
	if err != nil {
		return fmt.Errorf("create id generator: %w", err)
	}

	// 8. 初始化服务接口 (Servers)
	g.healthProbe = health.NewProbe()
	if err := g.initServers(idGen); err != nil {
		return err
	}

	go g.monitorRegistryLeaseFailures()

	return nil
}

// initBaseResources 初始化外部连接 (Redis、Etcd、Registry)
func (g *Gateway) initBaseResources() (_ *resources, returnedErr error) {
	cleanup := make([]func() error, 0, 3)
	defer func() {
		if returnedErr == nil {
			return
		}
		for index := len(cleanup) - 1; index >= 0; index-- {
			returnedErr = errors.Join(returnedErr, cleanup[index]())
		}
	}()
	// Redis
	redisConn, err := connector.NewRedis(&g.config.Redis, connector.WithLogger(g.logger))
	if err != nil {
		return nil, fmt.Errorf("redis init: %w", err)
	}
	cleanup = append(cleanup, redisConn.Close)
	if err := redisConn.Connect(g.ctx); err != nil {
		return nil, fmt.Errorf("redis connect: %w", err)
	}

	// Etcd
	etcdConn, err := connector.NewEtcd(&g.config.Etcd, connector.WithLogger(g.logger))
	if err != nil {
		return nil, fmt.Errorf("etcd init: %w", err)
	}
	cleanup = append(cleanup, etcdConn.Close)
	if err := etcdConn.Connect(g.ctx); err != nil {
		return nil, fmt.Errorf("etcd connect: %w", err)
	}

	// Registry
	reg, err := registry.New(etcdConn, g.config.Registry.ToRegistryConfig(), registry.WithLogger(g.logger))
	if err != nil {
		return nil, fmt.Errorf("registry init: %w", err)
	}
	cleanup = append(cleanup, reg.Close)
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

	logicClient, err := logicclient.NewClient(
		g.config.GetLogicServiceName(),
		g.gatewayID,
		g.logger,
		g.registry,
		logicclient.WithUserServiceSigner(g.resources.userSigner),
	)
	if err != nil {
		return fmt.Errorf("logic client init: %w", err)
	}

	// 创建并设置 StatusBatcher（状态批量同步器）
	statusBatcher := logicclient.NewStatusBatcher(
		logicClient.PresenceSvc(),
		g.gatewayID,
		g.logger,
		logicclient.WithBatchSize(g.config.StatusBatcher.GetBatchSize()),
		logicclient.WithFlushInterval(g.config.StatusBatcher.GetFlushInterval()),
	)
	logicClient.SetStatusBatcher(statusBatcher)

	presence := ws.NewPresenceCallback(logicClient, g.logger)
	connMgr := ws.NewManager(g.logger, nil, presence.OnUserOnline, presence.OnUserOffline)

	g.resources.logicClient = logicClient
	g.resources.connMgr = connMgr

	return nil
}

// initServers 初始化各个协议的服务端
func (g *Gateway) initServers(idGen idgen.Generator) error {
	// WebSocket Handler
	dispatcher := ws.NewDispatcher(g.logger, g.resources.logicClient)
	wsHandler := ws.NewUpgrader(g.logger, g.resources.connMgr, dispatcher, g.config.WSConfig)
	g.resources.connMgr.SetUpgrader(wsHandler.Upgrader())

	// HTTP Handler & Middlewares
	limiterOptions := []ratelimit.Option{ratelimit.WithLogger(g.logger)}
	if g.config.RateLimit.Driver == ratelimit.DriverDistributed {
		limiterOptions = append(limiterOptions, ratelimit.WithRedisConnector(g.resources.redisConn))
	}
	limiter, err := ratelimit.New(g.config.RateLimit.ToGenesisConfig(), limiterOptions...)
	if err != nil {
		return fmt.Errorf("rate limiter init: %w", err)
	}
	g.resources.limiter = limiter
	middlewares := httpapi.NewMiddlewares(g.logger, limiter, idGen)
	apiHandler := httpapi.NewHTTPHandler(g.resources.logicClient, g.resources.authenticator, g.logger)

	// Push Service
	pushService := pushserver.NewService(g.resources.connMgr, g.logger)

	// Servers
	g.httpServer = server.NewHTTPServer(g.config, g.logger, apiHandler, middlewares, wsHandler, g.healthProbe)
	g.grpcServer = server.NewGRPCServer(fmt.Sprintf(":%d", g.config.GetGRPCPort()), g.logger, pushService)
	return nil
}

func (g *Gateway) monitorRegistryLeaseFailures() {
	for {
		select {
		case failure, ok := <-g.registry.LeaseFailures():
			if !ok {
				return
			}
			g.logger.Error("gateway registry lease lost", clog.String("service_id", failure.ServiceID), clog.Error(failure.Err))
			if g.healthProbe != nil {
				g.healthProbe.SetReady(false)
			}
			g.cancel()
		case <-g.ctx.Done():
			return
		}
	}
}

// Run 启动所有服务并注册
func (g *Gateway) Run() error {
	g.logger.Info("starting gateway servers...")
	g.healthProbe.SetReady(false)
	g.healthProbe.SetShutdown(false)

	// 启动 StatusBatcher
	g.resources.logicClient.StartStatusBatcher()

	go func() {
		if err := g.grpcServer.Start(); err != nil {
			g.logger.Error("grpc server failed", clog.Error(err))
			g.cancel()
		}
	}()
	go func() {
		if err := g.httpServer.Start(); err != nil {
			g.logger.Error("http server failed", clog.Error(err))
			g.cancel()
		}
	}()

	if err := g.registerService(); err != nil {
		return err
	}
	g.healthProbe.SetReady(true)
	return nil
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
	g.closeOnce.Do(func() { g.closeErr = g.close() })
	return g.closeErr
}

func (g *Gateway) close() error {
	var result error
	if g.logger != nil {
		g.logger.Info("shutting down gateway...")
	}
	if g.healthProbe != nil {
		g.healthProbe.SetReady(false)
		g.healthProbe.SetShutdown(true)
	}
	g.cancel()

	// 1. 停止 workerID 保活
	if g.stopWorkerIDKeepAlive != nil {
		result = errors.Join(result, g.stopWorkerIDKeepAlive())
	}

	// 2. 注销服务
	if g.registry != nil {
		registryCtx, cancelRegistry := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelRegistry()
		if g.gatewayID != "" {
			if err := g.registry.Deregister(registryCtx, g.gatewayID); err != nil {
				g.logger.Warn("deregister gateway failed", clog.Error(err))
				result = errors.Join(result, fmt.Errorf("deregister gateway: %w", err))
			}
		}
		result = errors.Join(result, g.registry.Shutdown(registryCtx))
	}

	// 3. 停止服务实例
	if g.grpcServer != nil {
		g.grpcServer.Stop()
	}

	httpShutdownCtx, httpCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer httpCancel()

	if g.httpServer != nil {
		if err := g.httpServer.Stop(httpShutdownCtx); err != nil {
			g.logger.Warn("stop http server failed", clog.Error(err))
		}
	}

	// 4. 按依赖逆序释放核心资源。每个组件自身负责有界关闭。
	if g.resources != nil {
		if g.resources.connMgr != nil {
			result = errors.Join(result, g.resources.connMgr.Close())
		}
		if g.resources.logicClient != nil {
			result = errors.Join(result, g.resources.logicClient.Close())
		}
		if g.resources.limiter != nil {
			result = errors.Join(result, g.resources.limiter.Close())
		}
		if g.resources.etcdConn != nil {
			result = errors.Join(result, g.resources.etcdConn.Close())
		}
		if g.resources.redisConn != nil {
			result = errors.Join(result, g.resources.redisConn.Close())
		}
	}

	// 5. 关闭可观测性组件
	observabilityCtx, cancelObservability := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelObservability()
	result = errors.Join(result, observability.Shutdown(observabilityCtx))
	return result
}
