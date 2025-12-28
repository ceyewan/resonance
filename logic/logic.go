package logic

import (
	"context"
	"fmt"

	"github.com/ceyewan/genesis/auth"
	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/db"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/mq"
	"github.com/ceyewan/genesis/registry"
	"github.com/ceyewan/resonance/internal/repo"
	"github.com/ceyewan/resonance/logic/config"
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
	snowflakeGen   *idgen.Snowflake // 用于 MsgID
	uuidGen        *idgen.UUID      // 用于 SessionID
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

	// 2. 核心资源
	res, err := l.initResources()
	if err != nil {
		return err
	}
	l.resources = res

	// 3. 基于 Redis 抢占分配唯一实例 ID
	instanceID, stop, failCh, err := idgen.AssignInstanceID(
		l.ctx,
		res.redisConn,
		"resonance:"+l.config.Service.Name,
		1024,
	)
	if err != nil {
		return fmt.Errorf("assign instance id: %w", err)
	}
	l.serviceID = fmt.Sprintf("%s-%d", l.config.Service.Name, instanceID)
	res.instanceIDStop = stop

	// 监听保活失败
	go func() {
		if err := <-failCh; err != nil {
			l.logger.Error("instance id keepalive failed", clog.Error(err))
			l.cancel() // 保活失败，关闭服务
		}
	}()

	// 4. 服务层
	authSvc := service.NewAuthService(res.userRepo, res.sessionRepo, res.authenticator, logger)
	sessionSvc := service.NewSessionService(res.sessionRepo, res.messageRepo, res.userRepo, res.uuidGen, logger)
	chatSvc := service.NewChatService(res.sessionRepo, res.messageRepo, res.snowflakeGen, res.mqClient, logger)
	gatewayOpsSvc := service.NewGatewayOpsService(res.routerRepo, logger)

	// 5. gRPC Server
	l.grpcServer = server.NewGRPCServer(
		l.config.Service.ServerAddr,
		logger,
		authSvc,
		sessionSvc,
		chatSvc,
		gatewayOpsSvc,
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
	dbInstance, err := db.New(mysqlConn, &db.Config{}, db.WithLogger(l.logger))
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
	natsDriver := mq.NewNatsCoreDriver(natsConn, l.logger)
	mqClient, err := mq.New(natsDriver, mq.WithLogger(l.logger))
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
	snowflakeGen, err := idgen.NewSnowflake(l.config.WorkerID)
	if err != nil {
		return nil, fmt.Errorf("snowflake init: %w", err)
	}
	uuidGen := idgen.NewUUID(idgen.WithUUIDVersion("v7"))

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
		snowflakeGen:  snowflakeGen,
		uuidGen:       uuidGen,
		userRepo:      userRepo,
		sessionRepo:   sessionRepo,
		messageRepo:   messageRepo,
		routerRepo:    routerRepo,
	}, nil
}

// Run 启动服务
func (l *Logic) Run() error {
	l.logger.Info("starting logic service...")

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
	service := &registry.ServiceInstance{
		ID:      l.serviceID,
		Name:    l.config.Service.Name,
		Version: "1.0.0",
		Endpoints: []string{
			"grpc://127.0.0.1" + l.config.Service.ServerAddr, // ServerAddr 通常是 ":9090"
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

	// 3. 释放资源
	if l.resources != nil {
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
	}

	return nil
}
