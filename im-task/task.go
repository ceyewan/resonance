package task

import (
	"context"
	"fmt"
	"time"

	"github.com/ceyewan/genesis/cache"
	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/mq"
	"github.com/ceyewan/resonance/im-sdk/repo"
	"github.com/ceyewan/resonance/im-task/consumer"
	"github.com/ceyewan/resonance/im-task/dispatcher"
	"github.com/ceyewan/resonance/im-task/pusher"
)

// Task Task 服务
type Task struct {
	config *Config
	logger clog.Logger

	// 基础组件
	mysqlConn  connector.MySQLConnector
	redisConn  connector.RedisConnector
	natsConn   connector.NATSConnector
	cache      cache.Cache
	subscriber mq.Subscriber

	// 仓储层 (需要外部注入实现)
	sessionRepo repo.SessionRepository

	// 业务组件
	pusher     *pusher.GatewayPusher
	dispatcher *dispatcher.Dispatcher
	consumer   *consumer.Consumer

	ctx    context.Context
	cancel context.CancelFunc
}

// New 创建 Task 实例
func New(cfg *Config) (*Task, error) {
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
		return fmt.Errorf("failed to create mysql connector: %w", err)
	}

	// 初始化 Redis 连接
	t.redisConn, err = connector.NewRedis(&t.config.Redis, connector.WithLogger(t.logger))
	if err != nil {
		return fmt.Errorf("failed to create redis connector: %w", err)
	}

	// 初始化 NATS 连接
	t.natsConn, err = connector.NewNATS(&t.config.NATS, connector.WithLogger(t.logger))
	if err != nil {
		return fmt.Errorf("failed to create nats connector: %w", err)
	}

	// 初始化 Cache
	t.cache, err = cache.NewRedisCache(t.redisConn, cache.WithLogger(t.logger))
	if err != nil {
		return fmt.Errorf("failed to create cache: %w", err)
	}

	// 初始化 MQ Subscriber
	t.subscriber, err = mq.NewNATSSubscriber(t.natsConn, mq.WithLogger(t.logger))
	if err != nil {
		return fmt.Errorf("failed to create mq subscriber: %w", err)
	}

	return nil
}

// initBusinessComponents 初始化业务组件
func (t *Task) initBusinessComponents() error {
	var err error

	// 初始化 Gateway Pusher
	t.pusher, err = pusher.NewGatewayPusher(t.config.GatewayAddrs, t.logger)
	if err != nil {
		return fmt.Errorf("failed to create gateway pusher: %w", err)
	}

	// 初始化 Dispatcher
	t.dispatcher = dispatcher.NewDispatcher(
		t.sessionRepo,
		t.cache,
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
		t.subscriber,
		t.dispatcher,
		consumerConfig,
		t.logger,
	)

	return nil
}

// SetRepositories 设置仓储层实现（外部注入）
func (t *Task) SetRepositories(sessionRepo repo.SessionRepository) {
	t.sessionRepo = sessionRepo

	// 重新初始化业务组件
	t.dispatcher = dispatcher.NewDispatcher(
		t.sessionRepo,
		t.cache,
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
		t.subscriber,
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

	// 关闭连接
	if t.natsConn != nil {
		t.natsConn.Close()
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
