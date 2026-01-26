package logic

import (
	"context"
	"fmt"
	"time"

	"github.com/ceyewan/genesis/auth"
	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/db"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/mq"
	"github.com/ceyewan/genesis/registry"
	"github.com/ceyewan/resonance/internal/repo"
	"github.com/ceyewan/resonance/logic/config"
	"github.com/ceyewan/resonance/logic/job"
	"github.com/ceyewan/resonance/logic/observability"
	"github.com/ceyewan/resonance/logic/server"
	"github.com/ceyewan/resonance/logic/service"
)

// Logic Logic 服务生命周期管理器
type Logic struct {
	config    *config.Config
	logger    clog.Logger
	registry  registry.Registry
	serviceID string

	// 服务器
	grpcServer *server.GRPCServer

	// 后台任务
	outboxRelay *job.OutboxRelay

	// 资源
	resources *resources
	ctx       context.Context
	cancel    context.CancelFunc
}

// resources 内部资源聚合
type resources struct {
	etcdConn       connector.EtcdConnector
	redisConn      connector.RedisConnector
	mysqlConn      connector.MySQLConnector
	natsConn       connector.NATSConnector
	mqClient       mq.Client
	authenticator  auth.Authenticator
	msgIDGen       idgen.Generator // 用于 MsgID (Snowflake)
	sessionIDGen   idgen.Generator // 用于 SessionID (Snowflake)
	sequencer      idgen.Sequencer // 用于会话 SeqID (基于 Redis)
	dbInstance     db.DB
	instanceIDStop func() // 实例 ID 保活停止函数

	// Repos
	userRepo    repo.UserRepo
	sessionRepo repo.SessionRepo
	messageRepo repo.MessageRepo
	routerRepo  repo.RouterRepo
}

// New 创建 Logic 实例
func New() (*Logic, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	l := &Logic{
		config: cfg,
		ctx:    ctx,
		cancel: cancel,
	}

	if err := l.initComponents(); err != nil {
		l.Close()
		return nil, err
	}

	return l, nil
}

// initComponents 初始化所有组件
func (l *Logic) initComponents() error {
	// 1. 日志
	logger, _ := clog.New(&l.config.Log, clog.WithStandardContext())
	l.logger = logger

	// 2. 可观测性
	obsCfg := &observability.Config{
		Trace: observability.TraceConfig{
			Disable:  l.config.Observability.Trace.Disable,
			Endpoint: l.config.Observability.Trace.Endpoint,
			Insecure: l.config.Observability.Trace.Insecure,
			Sampler:  l.config.Observability.Trace.Sampler,
		},
		Metrics: observability.MetricsConfig{
			Port:          l.config.Observability.Metrics.Port,
			Path:          l.config.Observability.Metrics.Path,
			EnableRuntime: l.config.Observability.Metrics.EnableRuntime,
		},
	}
	if err := observability.Init(obsCfg); err != nil {
		l.logger.Warn("failed to init observability", clog.Error(err))
		// 可观测性初始化失败不影响服务启动
	}

	// 3. 核心资源
	res, err := l.initResources()
	if err != nil {
		return err
	}
	l.resources = res

	// 初始化管理员账号（可选）
	if err := service.EnsureAdminUser(l.ctx, res.userRepo, res.sessionRepo, l.config.Admin.Username, l.config.Admin.Password, l.config.Admin.Nickname, l.logger); err != nil {
		l.logger.Error("failed to ensure admin user", clog.Error(err))
	}

	// 3. 基于 Redis 抢占分配唯一实例 ID (WorkerID)
	allocator, err := idgen.NewAllocator(&idgen.AllocatorConfig{
		Driver: "redis",
		MaxID:  l.config.WorkerID.GetMaxID(),
	}, idgen.WithRedisConnector(res.redisConn))
	if err != nil {
		return fmt.Errorf("create allocator: %w", err)
	}
	instanceID, err := allocator.Allocate(l.ctx)
	if err != nil {
		return fmt.Errorf("allocate instance id: %w", err)
	}
	l.serviceID = fmt.Sprintf("%s-%d", l.config.Service.Name, instanceID)
	res.instanceIDStop = allocator.Stop

	// 3.1 使用分配到的 instanceID 初始化 ID 生成器
	msgIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: instanceID})
	if err != nil {
		return fmt.Errorf("msgID generator init: %w", err)
	}
	res.msgIDGen = msgIDGen

	sessionIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{WorkerID: instanceID})
	if err != nil {
		return fmt.Errorf("sessionID generator init: %w", err)
	}
	res.sessionIDGen = sessionIDGen

	// 监听保活失败
	go func() {
		if err := <-allocator.KeepAlive(l.ctx); err != nil {
			l.logger.Error("instance id keepalive failed", clog.Error(err))
			l.cancel() // 保活失败，关闭服务
		}
	}()

	// 4. 服务层
	authSvc := service.NewAuthService(res.userRepo, res.sessionRepo, res.authenticator, logger)
	sessionSvc := service.NewSessionService(res.sessionRepo, res.messageRepo, res.userRepo, res.sessionIDGen, res.msgIDGen, res.sequencer, res.mqClient, logger)
	chatSvc := service.NewChatService(res.sessionRepo, res.messageRepo, res.msgIDGen, res.sequencer, res.mqClient, logger)
	presenceSvc := service.NewPresenceService(res.routerRepo, logger)

	// 5. 后台任务
	l.outboxRelay = job.NewOutboxRelay(res.messageRepo, res.mqClient, logger, &l.config.Outbox)

	// 6. gRPC Server
	l.grpcServer = server.NewGRPCServer(
		l.config.Service.ServerAddr,
		logger,
		authSvc,
		sessionSvc,
		chatSvc,
		presenceSvc,
	)

	return nil
}

// initResources 初始化资源
func (l *Logic) initResources() (*resources, error) {
	// DB (MySQL)
	mysqlConn, err := connector.NewMySQL(&l.config.MySQL)
	if err != nil {
		return nil, fmt.Errorf("mysql init: %w", err)
	}
	dbInstance, err := db.New(&db.Config{Driver: "mysql"}, db.WithMySQLConnector(mysqlConn), db.WithLogger(l.logger))
	if err != nil {
		return nil, fmt.Errorf("db init: %w", err)
	}

	// Redis
	redisConn, err := connector.NewRedis(&l.config.Redis)
	if err != nil {
		return nil, fmt.Errorf("redis init: %w", err)
	}

	// NATS
	natsConn, err := connector.NewNATS(&l.config.NATS, connector.WithLogger(l.logger))
	if err != nil {
		return nil, fmt.Errorf("nats init: %w", err)
	}
	if err := natsConn.Connect(l.ctx); err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	mqClient, err := mq.New(&mq.Config{Driver: mq.DriverNatsCore}, mq.WithNATSConnector(natsConn), mq.WithLogger(l.logger))
	if err != nil {
		return nil, fmt.Errorf("mq client init: %w", err)
	}

	// Etcd & Registry
	etcdConn, err := connector.NewEtcd(&l.config.Etcd, connector.WithLogger(l.logger))
	if err != nil {
		return nil, fmt.Errorf("etcd init: %w", err)
	}
	if err := etcdConn.Connect(l.ctx); err != nil {
		return nil, fmt.Errorf("etcd connect: %w", err)
	}
	reg, err := registry.New(etcdConn, l.config.Registry.ToRegistryConfig(), registry.WithLogger(l.logger))
	if err != nil {
		return nil, fmt.Errorf("registry init: %w", err)
	}
	l.registry = reg

	// Authenticator
	authenticator, err := auth.New(&l.config.Auth, auth.WithLogger(l.logger))
	if err != nil {
		return nil, fmt.Errorf("auth init: %w", err)
	}

	// ID Generators
	// 注意：msgIDGen 和 sessionIDGen 稍后在 initComponents 中根据分配到的 instanceID 初始化
	var msgIDGen idgen.Generator
	var sessionIDGen idgen.Generator
	sequencer, _ := idgen.NewSequencer(&idgen.SequencerConfig{
		Driver:    "redis",
		KeyPrefix: "resonance:logic:seq",
		Step:      1,
	}, idgen.WithRedisConnector(redisConn), idgen.WithLogger(l.logger))

	// Repos
	// 假设 NewUserRepo 和 NewMessageRepo 签名与 SessionRepo 类似
	userRepo, err := repo.NewUserRepo(dbInstance) // 需要确认签名
	if err != nil {
		return nil, fmt.Errorf("user repo init: %w", err)
	}
	messageRepo, err := repo.NewMessageRepo(dbInstance) // 需要确认签名
	if err != nil {
		return nil, fmt.Errorf("message repo init: %w", err)
	}
	sessionRepo, err := repo.NewSessionRepo(dbInstance, repo.WithSessionRepoLogger(l.logger))
	if err != nil {
		return nil, fmt.Errorf("session repo init: %w", err)
	}
	routerRepo, err := repo.NewRouterRepo(redisConn, repo.WithLogger(l.logger))
	if err != nil {
		return nil, fmt.Errorf("router repo init: %w", err)
	}

	return &resources{
		mysqlConn:     mysqlConn,
		redisConn:     redisConn,
		natsConn:      natsConn,
		etcdConn:      etcdConn,
		mqClient:      mqClient,
		dbInstance:    dbInstance,
		authenticator: authenticator,
		msgIDGen:      msgIDGen,
		sessionIDGen:  sessionIDGen,
		sequencer:     sequencer,
		userRepo:      userRepo,
		sessionRepo:   sessionRepo,
		messageRepo:   messageRepo,
		routerRepo:    routerRepo,
	}, nil
}

// Run 启动服务
func (l *Logic) Run() error {
	l.logger.Info("starting logic service...")

	// 启动后台任务
	go l.outboxRelay.Start(l.ctx)

	// 启动 gRPC Server
	go func() {
		if err := l.grpcServer.Start(); err != nil {
			l.logger.Error("grpc server failed", clog.Error(err))
			l.cancel()
		}
	}()

	// 注册服务
	return l.registerService()
}

// registerService 注册服务到 Etcd
func (l *Logic) registerService() error {
	endpoint := l.config.GetAdvertiseEndpoint()
	service := &registry.ServiceInstance{
		ID:      l.serviceID,
		Name:    l.config.Service.Name,
		Version: "1.0.0",
		Endpoints: []string{
			"grpc://" + endpoint,
		},
	}

	return l.registry.Register(l.ctx, service, l.config.Registry.DefaultTTL)
}

// Close 优雅关闭
func (l *Logic) Close() error {
	l.logger.Info("shutting down logic service...")
	l.cancel()

	// 1. 注销服务
	if l.registry != nil {
		l.registry.Deregister(context.Background(), l.serviceID)
		l.registry.Close()
	}

	// 2. 停止服务器
	if l.grpcServer != nil {
		l.grpcServer.Stop()
	}

	// 3. 释放资源（带超时控制）
	if l.resources != nil {
		// 创建带超时的 context，用于控制资源关闭的最大等待时间
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// 使用 goroutine 并发关闭，监听超时
		done := make(chan struct{})
		go func() {
			// 停止实例 ID 保活
			if l.resources.instanceIDStop != nil {
				l.resources.instanceIDStop()
			}

			// 关闭 Repo (主要是清理缓存或日志，DB连接通常由 dbInstance 管理)
			l.resources.routerRepo.Close()
			l.resources.sessionRepo.Close()
			l.resources.userRepo.Close()
			l.resources.messageRepo.Close()

			l.resources.etcdConn.Close()
			l.resources.natsConn.Close()
			l.resources.redisConn.Close()
			l.resources.mysqlConn.Close()
			close(done)
		}()

		select {
		case <-done:
			// 正常关闭完成
		case <-shutdownCtx.Done():
			// 超时，记录警告但继续
			l.logger.Warn("resource shutdown timed out after 10s, some connections may not be closed cleanly")
		}
	}

	// 4. 关闭可观测性组件
	if err := observability.Shutdown(context.Background()); err != nil {
		l.logger.Error("observability shutdown failed", clog.Error(err))
	}

	return nil
}
