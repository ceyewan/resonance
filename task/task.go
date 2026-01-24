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
	"github.com/ceyewan/resonance/internal/repo"
	"github.com/ceyewan/resonance/task/config"
	"github.com/ceyewan/resonance/task/consumer"
	"github.com/ceyewan/resonance/task/dispatcher"
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
	pusherMgr  *pusher.Manager
	dispatcher *dispatcher.Dispatcher
	consumer   *consumer.Consumer
}

// resources 内部资源聚合
type resources struct {
	redisConn   connector.RedisConnector
	mysqlConn   connector.MySQLConnector
	natsConn    connector.NATSConnector
	etcdConn    connector.EtcdConnector
	mqClient    mq.Client
	registry    registry.Registry
	routerRepo  repo.RouterRepo
	sessionRepo repo.SessionRepo
	messageRepo repo.MessageRepo
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
	// 1. 初始化 Logger
	logger, _ := clog.New(&t.config.Log, clog.WithStandardContext())
	t.logger = logger

	// 2. 初始化核心资源
	res, err := t.initResources()
	if err != nil {
		return err
	}
	t.resources = res

	// 3. 初始化 Pusher Manager
	t.pusherMgr = pusher.NewManager(logger, res.registry, t.config.GatewayServiceName)

	// 4. 初始化 Dispatcher
	t.dispatcher = dispatcher.NewDispatcher(
		res.sessionRepo,
		res.messageRepo,
		res.routerRepo,
		t.pusherMgr,
		logger,
	)

	// 5. 初始化 Consumer
	t.consumer = consumer.NewConsumer(
		res.mqClient,
		t.dispatcher,
		t.config.ConsumerConfig,
		logger,
	)

	return nil
}

// initResources 初始化外部连接和 Repo
func (t *Task) initResources() (*resources, error) {
	// MySQL
	mysqlConn, err := connector.NewMySQL(&t.config.MySQL)
	if err != nil {
		return nil, fmt.Errorf("mysql init: %w", err)
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
		Driver: mq.DriverNatsCore,
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
	// 使用 genesis/db 封装 MySQLConnector
	dbInstance, err := db.New(&db.Config{
		Driver: "mysql",
	}, db.WithMySQLConnector(mysqlConn), db.WithLogger(t.logger))
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
		mysqlConn:   mysqlConn,
		redisConn:   redisConn,
		natsConn:    natsConn,
		etcdConn:    etcdConn,
		mqClient:    mqClient,
		registry:    reg,
		sessionRepo: sessionRepo,
		messageRepo: messageRepo,
		routerRepo:  routerRepo,
	}, nil
}

// Run 启动服务
func (t *Task) Run() error {
	t.logger.Info("starting task service...")

	// 启动 Pusher Manager (开始服务发现)
	if err := t.pusherMgr.Start(); err != nil {
		return fmt.Errorf("pusher manager start: %w", err)
	}

	// 启动 Consumer (开始消费消息)
	if err := t.consumer.Start(); err != nil {
		return fmt.Errorf("consumer start: %w", err)
	}

	return nil
}

// Close 优雅关闭
func (t *Task) Close() error {
	t.logger.Info("shutting down task service...")
	t.cancel()

	// 1. 停止消费
	if t.consumer != nil {
		t.consumer.Stop()
	}

	// 2. 关闭 Pusher (断开 Gateway 连接)
	if t.pusherMgr != nil {
		t.pusherMgr.Close()
	}

	// 3. 释放资源
	if t.resources != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		t.resources.registry.Close() // Registry 可能不需要显式 Close，视实现而定
		t.resources.etcdConn.Close()
		t.resources.natsConn.Close()
		t.resources.redisConn.Close()
		t.resources.mysqlConn.Close()
		_ = ctx // avoid unused
	}

	return nil
}
