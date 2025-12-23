package task

import (
	"context"
	"fmt"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/mq"
	"github.com/ceyewan/genesis/registry"
	"github.com/ceyewan/genesis/xerrors"
	"github.com/ceyewan/resonance/im-sdk/repo"
	"github.com/ceyewan/resonance/task/consumer"
	"github.com/ceyewan/resonance/task/dispatcher"
	"github.com/ceyewan/resonance/task/pusher"
)

// Task Task 服务
type Task struct {
	config *Config
	logger clog.Logger

	// 基础组件
	mysqlConn connector.MySQLConnector
	redisConn connector.RedisConnector
	natsConn  connector.NATSConnector
	etcdConn  connector.EtcdConnector
	registry  registry.Registry
	mqClient  mq.Client

	// 仓储层 (需要外部注入实现)
	routerRepo  repo.RouterRepo
	sessionRepo repo.SessionRepo

	// 业务组件
	pusher     *pusher.GatewayPusher
	dispatcher *dispatcher.Dispatcher
	consumer   *consumer.Consumer

	ctx    context.Context
	cancel context.CancelFunc
}

// New 创建 Task 实例（无参数，内部自己加载 config）
func New() (*Task, error) {
	cfg, err := Load()
	if err != nil {
		return nil, err
	}

	return NewWithConfig(cfg)
}

// NewWithConfig 创建 Task 实例（带 config 参数）
func NewWithConfig(cfg *Config) (*Task, error) {
	// 初始化日志
	logger, err := clog.New(&cfg.Log)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	t := &Task{
		config: cfg,
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
	}

	// 初始化基础组件
	if err := t.initComponents(); err != nil {
		return nil, err
	}

	// 初始化业务组件
	if err := t.initBusinessComponents(); err != nil {
		return nil, err
	}

	return t, nil
}

// initComponents 初始化基础组件
func (t *Task) initComponents() error {
	var err error

	// 初始化 MySQL 连接
	t.mysqlConn, err = connector.NewMySQL(&t.config.MySQL, connector.WithLogger(t.logger))
	if err != nil {
		return xerrors.Wrapf(err, "failed to create mysql connector")
	}

	// 初始化 Redis 连接
	t.redisConn, err = connector.NewRedis(&t.config.Redis, connector.WithLogger(t.logger))
	if err != nil {
		return xerrors.Wrapf(err, "failed to create redis connector")
	}

	// 初始化 NATS 连接
	t.natsConn, err = connector.NewNATS(&t.config.NATS, connector.WithLogger(t.logger))
	if err != nil {
		return xerrors.Wrapf(err, "failed to create nats connector")
	}

	// 初始化 Etcd 连接
	t.etcdConn, err = connector.NewEtcd(&t.config.Etcd, connector.WithLogger(t.logger))
	if err != nil {
		return xerrors.Wrapf(err, "failed to create etcd connector")
	}

	// 建立 Etcd 连接
	if err := t.etcdConn.Connect(t.ctx); err != nil {
		return xerrors.Wrapf(err, "failed to connect to etcd")
	}

	// 初始化 Registry
	t.registry, err = registry.New(t.etcdConn, t.config.Registry.ToRegistryConfig(), registry.WithLogger(t.logger))
	if err != nil {
		return xerrors.Wrapf(err, "failed to create registry")
	}

	// 初始化 MQ Client
	mqConfig := &mq.Config{
		Driver: mq.DriverNatsCore,
	}
	t.mqClient, err = mq.New(t.natsConn, mqConfig, mq.WithLogger(t.logger))
	if err != nil {
		return xerrors.Wrapf(err, "failed to create mq client")
	}

	return nil
}

// initBusinessComponents 初始化业务组件
func (t *Task) initBusinessComponents() error {
	var err error

	// 初始化 Gateway Pusher (使用 registry 进行服务发现)
	t.pusher, err = pusher.NewGatewayPusher(t.registry, t.config.GatewayServiceName, t.logger)
	if err != nil {
		return xerrors.Wrapf(err, "failed to create gateway pusher")
	}

	// 初始化 Dispatcher (需要 routerRepo，在 SetRepositories 中设置)
	t.dispatcher = dispatcher.NewDispatcher(
		t.sessionRepo,
		t.routerRepo,
		t.pusher,
		t.logger,
	)

	// 初始化 Consumer
	consumerConfig := consumer.ConsumerConfig{
		Topic:         t.config.ConsumerConfig.Topic,
		QueueGroup:    t.config.ConsumerConfig.QueueGroup,
		WorkerCount:   t.config.ConsumerConfig.WorkerCount,
		MaxRetry:      t.config.ConsumerConfig.MaxRetry,
		RetryInterval: time.Duration(t.config.ConsumerConfig.RetryInterval) * time.Second,
	}

	t.consumer = consumer.NewConsumer(
		t.mqClient,
		t.dispatcher,
		consumerConfig,
		t.logger,
	)

	return nil
}

// SetRepositories 设置仓储层实现（外部注入）
func (t *Task) SetRepositories(routerRepo repo.RouterRepo, sessionRepo repo.SessionRepo) {
	t.routerRepo = routerRepo
	t.sessionRepo = sessionRepo

	// 重新初始化 Dispatcher
	t.dispatcher = dispatcher.NewDispatcher(
		t.sessionRepo,
		t.routerRepo,
		t.pusher,
		t.logger,
	)

	// 重新初始化 Consumer
	consumerConfig := consumer.ConsumerConfig{
		Topic:         t.config.ConsumerConfig.Topic,
		QueueGroup:    t.config.ConsumerConfig.QueueGroup,
		WorkerCount:   t.config.ConsumerConfig.WorkerCount,
		MaxRetry:      t.config.ConsumerConfig.MaxRetry,
		RetryInterval: time.Duration(t.config.ConsumerConfig.RetryInterval) * time.Second,
	}

	t.consumer = consumer.NewConsumer(
		t.mqClient,
		t.dispatcher,
		consumerConfig,
		t.logger,
	)
}

// Run 启动 Task 服务
func (t *Task) Run() error {
	t.logger.Info("starting task service")

	// 启动消费者
	if err := t.consumer.Start(); err != nil {
		t.logger.Error("failed to start consumer", clog.Error(err))
		return err
	}

	t.logger.Info("task service started")

	// 阻塞等待退出信号
	<-t.ctx.Done()

	return nil
}

// Close 关闭 Task 服务
func (t *Task) Close() error {
	t.logger.Info("shutting down task service")

	t.cancel()

	// 停止消费者
	if t.consumer != nil {
		t.consumer.Stop()
	}

	// 关闭 Pusher
	if t.pusher != nil {
		t.pusher.Close()
	}

	// 关闭 Registry
	if t.registry != nil {
		t.registry.Close()
	}

	// 关闭连接
	if t.natsConn != nil {
		t.natsConn.Close()
	}

	if t.etcdConn != nil {
		t.etcdConn.Close()
	}

	if t.redisConn != nil {
		t.redisConn.Close()
	}

	if t.mysqlConn != nil {
		t.mysqlConn.Close()
	}

	t.logger.Info("task service stopped")
	return nil
}
