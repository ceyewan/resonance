package logic

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/mq"
	"github.com/ceyewan/genesis/xerrors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	logicv1 "github.com/ceyewan/resonance/im-api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/im-sdk/repo"
	"github.com/ceyewan/resonance/logic/service"
)

// Logic Logic 服务
type Logic struct {
	config *Config
	logger clog.Logger

	// 基础组件
	mysqlConn connector.MySQLConnector
	redisConn connector.RedisConnector
	natsConn  connector.NATSConnector
	idGen     idgen.Int64Generator
	mqClient  mq.Client

	// 仓储层 (需要外部注入实现)
	userRepo    repo.UserRepo
	sessionRepo repo.SessionRepo
	messageRepo repo.MessageRepo
	routerRepo  repo.RouterRepo

	// 服务层
	authService       *service.AuthService
	sessionService    *service.SessionService
	chatService       *service.ChatService
	gatewayOpsService *service.GatewayOpsService

	// gRPC 服务器
	grpcServer *grpc.Server
	listener   net.Listener
	ctx        context.Context
	cancel     context.CancelFunc
}

// New 创建 Logic 实例
func New(cfg *Config) (*Logic, error) {
	// 初始化日志
	logger, err := clog.New(&cfg.Log)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	l := &Logic{
		config: cfg,
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
	}

	// 初始化基础组件
	if err := l.initComponents(); err != nil {
		return nil, err
	}

	// 初始化服务层
	l.initServices()

	return l, nil
}

// initComponents 初始化基础组件
func (l *Logic) initComponents() error {
	var err error

	// 初始化 MySQL 连接
	l.mysqlConn, err = connector.NewMySQL(&l.config.MySQL, connector.WithLogger(l.logger))
	if err != nil {
		return xerrors.Wrapf(err, "failed to create mysql connector")
	}

	// 初始化 Redis 连接
	l.redisConn, err = connector.NewRedis(&l.config.Redis, connector.WithLogger(l.logger))
	if err != nil {
		return xerrors.Wrapf(err, "failed to create redis connector")
	}

	// 初始化 NATS 连接
	l.natsConn, err = connector.NewNATS(&l.config.NATS, connector.WithLogger(l.logger))
	if err != nil {
		return xerrors.Wrapf(err, "failed to create nats connector")
	}

	// 初始化 ID 生成器（使用 Redis 协调 WorkerID）
	l.idGen, err = idgen.NewSnowflake(&l.config.IDGen, l.redisConn, nil, idgen.WithLogger(l.logger))
	if err != nil {
		return xerrors.Wrapf(err, "failed to create id generator")
	}

	// 初始化 MQ Client
	mqConfig := &mq.Config{
		Driver: mq.DriverNatsCore,
	}
	l.mqClient, err = mq.New(l.natsConn, mqConfig, mq.WithLogger(l.logger))
	if err != nil {
		return xerrors.Wrapf(err, "failed to create mq client")
	}

	return nil
}

// initServices 初始化服务层
func (l *Logic) initServices() {
	l.authService = service.NewAuthService(
		l.userRepo,
		l.logger,
	)

	l.sessionService = service.NewSessionService(
		l.sessionRepo,
		l.messageRepo,
		l.userRepo,
		l.logger,
	)

	l.chatService = service.NewChatService(
		l.sessionRepo,
		l.messageRepo,
		l.routerRepo,
		l.idGen,
		l.mqClient,
		l.logger,
	)

	l.gatewayOpsService = service.NewGatewayOpsService(
		l.routerRepo,
		l.logger,
	)
}

// SetRepositories 设置仓储层实现（外部注入）
func (l *Logic) SetRepositories(
	userRepo repo.UserRepo,
	sessionRepo repo.SessionRepo,
	messageRepo repo.MessageRepo,
	routerRepo repo.RouterRepo,
) {
	l.userRepo = userRepo
	l.sessionRepo = sessionRepo
	l.messageRepo = messageRepo
	l.routerRepo = routerRepo

	// 重新初始化服务层
	l.initServices()
}

// Run 启动 Logic 服务
func (l *Logic) Run() error {
	l.logger.Info("starting logic service", clog.String("addr", l.config.ServerAddr))

	// 创建 gRPC 服务器
	l.grpcServer = grpc.NewServer()

	// 注册服务
	logicv1.RegisterAuthServiceServer(l.grpcServer, l.authService)
	logicv1.RegisterSessionServiceServer(l.grpcServer, l.sessionService)
	logicv1.RegisterChatServiceServer(l.grpcServer, l.chatService)
	logicv1.RegisterGatewayOpsServiceServer(l.grpcServer, l.gatewayOpsService)

	// 注册反射服务（用于 grpcurl 等工具）
	reflection.Register(l.grpcServer)

	// 创建监听器
	listener, err := net.Listen("tcp", l.config.ServerAddr)
	if err != nil {
		return xerrors.Wrapf(err, "failed to listen on %s", l.config.ServerAddr)
	}
	l.listener = listener

	l.logger.Info("logic service started", clog.String("addr", l.config.ServerAddr))

	// 启动 gRPC 服务器
	if err := l.grpcServer.Serve(listener); err != nil {
		l.logger.Error("logic service error", clog.Error(err))
		return err
	}

	return nil
}

// Close 关闭 Logic 服务
func (l *Logic) Close() error {
	l.logger.Info("shutting down logic service")

	l.cancel()

	// 关闭 gRPC 服务器
	if l.grpcServer != nil {
		l.grpcServer.GracefulStop()
	}

	// 等待服务器完全关闭
	time.Sleep(100 * time.Millisecond)

	// 关闭 MQ
	if l.mqClient != nil {
		l.mqClient.Close()
	}

	// 关闭连接
	if l.natsConn != nil {
		l.natsConn.Close()
	}

	if l.redisConn != nil {
		l.redisConn.Close()
	}

	if l.mysqlConn != nil {
		l.mysqlConn.Close()
	}

	l.logger.Info("logic service stopped")
	return nil
}
