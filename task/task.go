package task

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
	"github.com/ceyewan/resonance/task/streaming"
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
	pusherMgr      pusher.PusherManager
	dispatcher     *dispatcher.Dispatcher
	consumer       consumerComponent
	streamConsumer consumerComponent
	healthServer   healthComponent
	closeOnce      sync.Once
	closeErr       error
}

// resources 内部资源聚合
type resources struct {
	redisConn    closeable
	postgresConn closeable
	natsConn     closeable
	etcdConn     closeable
	mqClient     mq.MQ
	registry     registry.Registry
	routerRepo   repo.RouterRepo
	sessionRepo  repo.SessionRepo
	messageRepo  repo.MessageRepo
	dbInstance   closeable
}

type closeable interface {
	Close() error
}

type consumerComponent interface {
	Start() error
	Stop() error
	SetName(name string)
}

type healthComponent interface {
	Start() error
	Stop(ctx context.Context) error
	SetReady(ready bool)
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
		_ = t.Close()
		return nil, err
	}

	return t, nil
}

// initComponents 初始化所有组件
func (t *Task) initComponents() error {
	// 1. 初始化可观测性（Trace + Metrics）
	obsConfig := &observability.Config{
		Version: t.config.Observability.Version, InstanceID: t.config.Observability.InstanceID,
		Environment: t.config.Observability.Environment, Trace: t.config.Observability.Trace,
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
		res.messageRepo,
		res.routerRepo,
		t.pusherMgr,
		logger,
	)

	// 6. 初始化单消费者（先存储后推送）
	t.consumer = consumer.NewConsumer(
		res.mqClient,
		t.dispatcher.Handle,
		t.config.Consumer,
		logger.WithNamespace("consumer"),
	)
	t.consumer.SetName("chat_event")

	streamDispatcher, err := streaming.NewDispatcher(res.routerRepo, t.pusherMgr, logger.WithNamespace("agent_stream_dispatcher"))
	if err != nil {
		return fmt.Errorf("agent stream dispatcher init: %w", err)
	}
	t.streamConsumer, err = streaming.NewConsumer(
		res.mqClient,
		streamDispatcher.Handle,
		t.config.StreamConsumer,
		t.config.StreamMaxDeltaBytes,
		logger.WithNamespace("agent_stream_consumer"),
	)
	if err != nil {
		return fmt.Errorf("agent stream consumer init: %w", err)
	}

	// 7. 健康检查 Server
	t.healthServer = health.NewServer(t.config.GetHTTPAddr(), logger)

	return nil
}

// initResources 初始化外部连接和 Repo
func (t *Task) initResources() (_ *resources, returnedErr error) {
	cleanup := make([]func() error, 0, 12)
	defer func() {
		if returnedErr == nil {
			return
		}
		for index := len(cleanup) - 1; index >= 0; index-- {
			returnedErr = errors.Join(returnedErr, cleanup[index]())
		}
	}()
	// PostgreSQL
	postgresConn, err := connector.NewPostgreSQL(&t.config.PostgreSQL)
	if err != nil {
		return nil, fmt.Errorf("postgresql init: %w", err)
	}
	cleanup = append(cleanup, postgresConn.Close)
	if err := postgresConn.Connect(t.ctx); err != nil {
		return nil, fmt.Errorf("postgresql connect: %w", err)
	}

	// Redis
	redisConn, err := connector.NewRedis(&t.config.Redis)
	if err != nil {
		return nil, fmt.Errorf("redis init: %w", err)
	}
	cleanup = append(cleanup, redisConn.Close)
	if err := redisConn.Connect(t.ctx); err != nil {
		return nil, fmt.Errorf("redis connect: %w", err)
	}

	// NATS
	natsConn, err := connector.NewNATS(&t.config.NATS, connector.WithLogger(t.logger))
	if err != nil {
		return nil, fmt.Errorf("nats init: %w", err)
	}
	cleanup = append(cleanup, natsConn.Close)
	if err := natsConn.Connect(t.ctx); err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	mqClient, err := mq.New(&mq.Config{
		Driver:    mq.DriverNATSJetStream,
		JetStream: &t.config.JetStream,
	}, mq.WithNATSConnector(natsConn), mq.WithLogger(t.logger))
	if err != nil {
		return nil, fmt.Errorf("mq client init: %w", err)
	}
	cleanup = append(cleanup, mqClient.Close)

	// Etcd (用于服务发现)
	etcdConn, err := connector.NewEtcd(&t.config.Etcd, connector.WithLogger(t.logger))
	if err != nil {
		return nil, fmt.Errorf("etcd init: %w", err)
	}
	cleanup = append(cleanup, etcdConn.Close)
	if err := etcdConn.Connect(t.ctx); err != nil {
		return nil, fmt.Errorf("etcd connect: %w", err)
	}

	// Registry
	reg, err := registry.New(etcdConn, t.config.Registry.ToRegistryConfig(), registry.WithLogger(t.logger))
	if err != nil {
		return nil, fmt.Errorf("registry init: %w", err)
	}
	cleanup = append(cleanup, reg.Close)

	// Repos
	// NewRouterRepo 需要 RedisConnector 接口
	routerRepo, err := repo.NewRouterRepo(redisConn, repo.WithLogger(t.logger))
	if err != nil {
		return nil, fmt.Errorf("router repo init: %w", err)
	}
	cleanup = append(cleanup, routerRepo.Close)

	// NewSessionRepo 需要 db.DB 接口
	// 使用 genesis/db 封装 PostgreSQLConnector
	dbInstance, err := db.New(&db.Config{
		Driver: "postgresql",
	}, db.WithPostgreSQLConnector(postgresConn), db.WithLogger(t.logger))
	if err != nil {
		return nil, fmt.Errorf("db init: %w", err)
	}
	cleanup = append(cleanup, dbInstance.Close)

	sessionRepo, err := repo.NewSessionRepo(dbInstance, repo.WithSessionRepoLogger(t.logger))
	if err != nil {
		return nil, fmt.Errorf("session repo init: %w", err)
	}
	cleanup = append(cleanup, sessionRepo.Close)

	messageRepo, err := repo.NewMessageRepo(dbInstance, repo.WithMessageRepoLogger(t.logger))
	if err != nil {
		return nil, fmt.Errorf("message repo init: %w", err)
	}
	cleanup = append(cleanup, messageRepo.Close)

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
		dbInstance:   dbInstance,
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = t.healthServer.Stop(shutdownCtx)
		cancel()
		return fmt.Errorf("pusher manager start: %w", err)
	}

	// 启动 Consumer (开始消费消息)
	if err := t.consumer.Start(); err != nil {
		t.pusherMgr.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = t.healthServer.Stop(shutdownCtx)
		cancel()
		return fmt.Errorf("consumer start: %w", err)
	}
	if t.streamConsumer != nil {
		if err := t.streamConsumer.Start(); err != nil {
			_ = t.consumer.Stop()
			return fmt.Errorf("agent stream consumer start: %w", err)
		}
	}

	// 服务就绪，标记健康检查
	t.healthServer.SetReady(true)

	return nil
}

// Close 优雅关闭
func (t *Task) Close() error {
	t.closeOnce.Do(func() { t.closeErr = t.close() })
	return t.closeErr
}

func (t *Task) close() error {
	var result error
	if t.logger != nil {
		t.logger.Info("shutting down task service...")
	}

	// 标记服务未就绪
	if t.healthServer != nil {
		t.healthServer.SetReady(false)
	}

	t.cancel()

	// 1. 停止健康检查服务器
	if t.healthServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		result = errors.Join(result, t.healthServer.Stop(shutdownCtx))
		cancel()
	}

	// 2. 停止临时流与持久消息摄入
	if t.streamConsumer != nil {
		if err := t.streamConsumer.Stop(); err != nil {
			if t.logger != nil {
				t.logger.Warn("stop agent stream consumer failed", clog.Error(err))
			}
			result = errors.Join(result, err)
		}
	}
	if t.consumer != nil {
		if err := t.consumer.Stop(); err != nil {
			if t.logger != nil {
				t.logger.Warn("stop consumer failed", clog.Error(err))
			}
			result = errors.Join(result, err)
		}
	}

	// 3. 关闭 Pusher (断开 Gateway 连接)
	if t.pusherMgr != nil {
		t.pusherMgr.Close()
	}

	// 4. 按依赖逆序释放资源
	if t.resources != nil {
		if t.resources.routerRepo != nil {
			result = errors.Join(result, t.resources.routerRepo.Close())
		}
		if t.resources.sessionRepo != nil {
			result = errors.Join(result, t.resources.sessionRepo.Close())
		}
		if t.resources.messageRepo != nil {
			result = errors.Join(result, t.resources.messageRepo.Close())
		}
		mqCtx, cancelMQ := context.WithTimeout(context.Background(), 10*time.Second)
		if t.resources.mqClient != nil {
			result = errors.Join(result, t.resources.mqClient.Drain(mqCtx), t.resources.mqClient.Close())
		}
		cancelMQ()
		if t.resources.registry != nil {
			registryCtx, cancelRegistry := context.WithTimeout(context.Background(), 5*time.Second)
			result = errors.Join(result, t.resources.registry.Shutdown(registryCtx))
			cancelRegistry()
		}
		if t.resources.dbInstance != nil {
			result = errors.Join(result, t.resources.dbInstance.Close())
		}
		if t.resources.etcdConn != nil {
			result = errors.Join(result, t.resources.etcdConn.Close())
		}
		if t.resources.natsConn != nil {
			result = errors.Join(result, t.resources.natsConn.Close())
		}
		if t.resources.redisConn != nil {
			result = errors.Join(result, t.resources.redisConn.Close())
		}
		if t.resources.postgresConn != nil {
			result = errors.Join(result, t.resources.postgresConn.Close())
		}
	}

	// 5. 关闭可观测性组件
	observabilityCtx, cancelObservability := context.WithTimeout(context.Background(), 5*time.Second)
	result = errors.Join(result, observability.Shutdown(observabilityCtx))
	cancelObservability()
	return result
}
