package task

import (
	"context"
	"fmt"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/db"
	"github.com/ceyewan/genesis/mq"
	"github.com/ceyewan/genesis/registry"
	"github.com/ceyewan/resonance/pkg/health"
	"github.com/ceyewan/resonance/repo"
	"github.com/ceyewan/resonance/task/config"
	"github.com/ceyewan/resonance/task/consumer"
	"github.com/ceyewan/resonance/task/dispatcher"
	"github.com/ceyewan/resonance/task/observability"
	"github.com/ceyewan/resonance/task/pusher"
)

// Task 任务服务生命周期管理器
type Task struct {
	config *config.Config
	logger clog.Logger
	ctx    context.Context
	cancel context.CancelFunc

	// 核心资源
	resources *resources

	// 组件
	pusherMgr       *pusher.Manager
	dispatcher      *dispatcher.Dispatcher
	storageConsumer *consumer.Consumer
	pushConsumer    *consumer.Consumer
	healthServer    *health.Server
}

// resources 内部资源聚合
type resources struct {
	redisConn    connector.RedisConnector
	postgresConn connector.PostgreSQLConnector
	natsConn     connector.NATSConnector
	etcdConn     connector.EtcdConnector
	mqClient     mq.MQ
	registry     registry.Registry
	routerRepo   repo.RouterRepo
	sessionRepo  repo.SessionRepo
	messageRepo  repo.MessageRepo
}

// New 创建 Task 实例
func New() (*Task, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	t := &Task{
		config: cfg,
		ctx:    ctx,
		cancel: cancel,
	}

	if err := t.initComponents(); err != nil {
		t.Close()
		return nil, err
	}

	return t, nil
}

// initComponents 初始化所有组件
func (t *Task) initComponents() error {
	// 1. 初始化可观测性（Trace + Metrics）
	obsConfig := &observability.Config{
		Trace:   t.config.Observability.Trace,
		Metrics: t.config.Observability.Metrics,
	}
	if err := observability.Init(obsConfig); err != nil {
		return fmt.Errorf("observability init: %w", err)
	}

	// 2. 初始化 Logger（带 Trace Context 支持）
	logger, err := observability.NewLogger(&t.config.Log)
	if err != nil {
		return fmt.Errorf("logger init: %w", err)
	}
	t.logger = logger

	// 3. 初始化核心资源
	res, err := t.initResources()
	if err != nil {
		return err
	}
	t.resources = res

	// 4. 初始化 Pusher Manager
	queueSize := t.config.GatewayQueueSize
	if queueSize <= 0 {
		queueSize = 1000 // 默认每个 Gateway 队列大小 1000
	}
	pusherCount := t.config.GatewayPusherCount
	if pusherCount <= 0 {
		pusherCount = 3 // 默认每个 Gateway 3 个并发推送协程
	}
	pollInterval := t.config.Registry.PollInterval
	if pollInterval <= 0 {
		pollInterval = 10 * time.Second // 默认 10s 轮询一次
	}
	t.pusherMgr = pusher.NewManager(logger, res.registry, t.config.GatewayServiceName, queueSize, pusherCount, pollInterval)

	// 5. 初始化 Dispatcher
	t.dispatcher = dispatcher.NewDispatcher(
		res.sessionRepo,
		res.messageRepo,
		res.routerRepo,
		t.pusherMgr,
		logger,
	)

	// 6. 初始化 Consumers
	// 6.1 Storage Consumer (落库)
	t.storageConsumer = consumer.NewConsumer(
		res.mqClient,
		t.dispatcher.DispatchStorage,
		t.config.StorageConsumer,
		logger.WithNamespace("consumer_storage"),
	)
	t.storageConsumer.SetName("storage")

	// 6.2 Push Consumer (推送)
	t.pushConsumer = consumer.NewConsumer(
		res.mqClient,
		t.dispatcher.DispatchPush,
		t.config.PushConsumer,
		logger.WithNamespace("consumer_push"),
	)
	t.pushConsumer.SetName("push")

	// 7. 健康检查 Server
	t.healthServer = health.NewServer(t.config.GetHTTPAddr(), logger)

	return nil
}

// initResources 初始化外部连接和 Repo
func (t *Task) initResources() (*resources, error) {
	// PostgreSQL
	postgresConn, err := connector.NewPostgreSQL(&t.config.PostgreSQL)
	if err != nil {
		return nil, fmt.Errorf("postgresql init: %w", err)
	}

	// Redis
	redisConn, err := connector.NewRedis(&t.config.Redis)
	if err != nil {
		return nil, fmt.Errorf("redis init: %w", err)
	}

	// NATS
	natsConn, err := connector.NewNATS(&t.config.NATS, connector.WithLogger(t.logger))
	if err != nil {
		return nil, fmt.Errorf("nats init: %w", err)
	}
	if err := natsConn.Connect(t.ctx); err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	// MQ Client (NATS Core)
	mqClient, err := mq.New(&mq.Config{
		Driver: mq.DriverNATSCore,
	}, mq.WithNATSConnector(natsConn), mq.WithLogger(t.logger))
	if err != nil {
		return nil, fmt.Errorf("mq client init: %w", err)
	}

	// Etcd (用于服务发现)
	etcdConn, err := connector.NewEtcd(&t.config.Etcd, connector.WithLogger(t.logger))
	if err != nil {
		return nil, fmt.Errorf("etcd init: %w", err)
	}
	if err := etcdConn.Connect(t.ctx); err != nil {
		return nil, fmt.Errorf("etcd connect: %w", err)
	}

	// Registry
	reg, err := registry.New(etcdConn, t.config.Registry.ToRegistryConfig(), registry.WithLogger(t.logger))
	if err != nil {
		return nil, fmt.Errorf("registry init: %w", err)
	}

	// Repos
	// NewRouterRepo 需要 RedisConnector 接口
	routerRepo, err := repo.NewRouterRepo(redisConn, repo.WithLogger(t.logger))
	if err != nil {
		return nil, fmt.Errorf("router repo init: %w", err)
	}

	// NewSessionRepo 需要 db.DB 接口
	// 使用 genesis/db 封装 PostgreSQLConnector
	dbInstance, err := db.New(&db.Config{
		Driver: "postgresql",
	}, db.WithPostgreSQLConnector(postgresConn), db.WithLogger(t.logger))
	if err != nil {
		return nil, fmt.Errorf("db init: %w", err)
	}

	sessionRepo, err := repo.NewSessionRepo(dbInstance, repo.WithSessionRepoLogger(t.logger))
	if err != nil {
		return nil, fmt.Errorf("session repo init: %w", err)
	}

	messageRepo, err := repo.NewMessageRepo(dbInstance, repo.WithMessageRepoLogger(t.logger))
	if err != nil {
		return nil, fmt.Errorf("message repo init: %w", err)
	}

	return &resources{
		postgresConn: postgresConn,
		redisConn:    redisConn,
		natsConn:     natsConn,
		etcdConn:     etcdConn,
		mqClient:     mqClient,
		registry:     reg,
		sessionRepo:  sessionRepo,
		messageRepo:  messageRepo,
		routerRepo:   routerRepo,
	}, nil
}

// Run 启动服务
func (t *Task) Run() error {
	t.logger.Info("starting task service...")

	// 启动健康检查服务器
	if err := t.healthServer.Start(); err != nil {
		return fmt.Errorf("health server start: %w", err)
	}

	// 启动 Pusher Manager (开始服务发现)
	if err := t.pusherMgr.Start(); err != nil {
		return fmt.Errorf("pusher manager start: %w", err)
	}

	// 启动 Consumers (开始消费消息)
	if err := t.storageConsumer.Start(); err != nil {
		return fmt.Errorf("storage consumer start: %w", err)
	}
	if err := t.pushConsumer.Start(); err != nil {
		return fmt.Errorf("push consumer start: %w", err)
	}

	// 服务就绪，标记健康检查
	t.healthServer.SetReady(true)

	return nil
}

// Close 优雅关闭
func (t *Task) Close() error {
	t.logger.Info("shutting down task service...")

	// 标记服务未就绪
	if t.healthServer != nil {
		t.healthServer.SetReady(false)
	}

	t.cancel()

	// 1. 停止健康检查服务器
	if t.healthServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = t.healthServer.Stop(shutdownCtx)
		cancel()
	}

	// 2. 停止消费
	if t.storageConsumer != nil {
		t.storageConsumer.Stop()
	}
	if t.pushConsumer != nil {
		t.pushConsumer.Stop()
	}

	// 3. 关闭 Pusher (断开 Gateway 连接)
	if t.pusherMgr != nil {
		t.pusherMgr.Close()
	}

	// 4. 释放资源（带超时控制）
	if t.resources != nil {
		// 创建带超时的 context，用于控制资源关闭的最大等待时间
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// 使用 goroutine 并发关闭，监听超时
		done := make(chan struct{})
		go func() {
			// 按依赖关系逆序关闭
			t.resources.registry.Close()
			t.resources.etcdConn.Close()
			t.resources.natsConn.Close()
			t.resources.redisConn.Close()
			t.resources.postgresConn.Close()
			close(done)
		}()

		select {
		case <-done:
			// 正常关闭完成
		case <-shutdownCtx.Done():
			// 超时，记录警告但继续
			t.logger.Warn("resource shutdown timed out after 10s, some connections may not be closed cleanly")
		}
	}

	// 5. 关闭可观测性组件
	if err := observability.Shutdown(context.Background()); err != nil {
		t.logger.Error("observability shutdown failed", clog.Error(err))
	}

	return nil
}
