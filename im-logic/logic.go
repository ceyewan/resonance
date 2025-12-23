package logic

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ceyewan/genesis/cache"
	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/mq"

	// "github.com/ceyewan/resonance/im-api/gen/go/logic/v1/logicv1connect"
	"github.com/ceyewan/resonance/im-logic/service"
	"github.com/ceyewan/resonance/im-sdk/repo"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// Logic Logic 服务
type Logic struct {
	config *Config
	logger clog.Logger

	// 基础组件
	mysqlConn connector.MySQLConnector
	redisConn connector.RedisConnector
	natsConn  connector.NATSConnector
	cache     cache.Cache
	idGen     idgen.IDGenerator
	mq        mq.Publisher

	// 仓储层 (需要外部注入实现)
	userRepo    repo.UserRepository
	tokenRepo   repo.TokenRepository
	sessionRepo repo.SessionRepository
	contactRepo repo.ContactRepository
	messageRepo repo.MessageRepository

	// 服务层
	authService       *service.AuthService
	sessionService    *service.SessionService
	chatService       *service.ChatService
	gatewayOpsService *service.GatewayOpsService

	// HTTP 服务器
	httpServer *http.Server
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
		return fmt.Errorf("failed to create mysql connector: %w", err)
	}

	// 初始化 Redis 连接
	l.redisConn, err = connector.NewRedis(&l.config.Redis, connector.WithLogger(l.logger))
	if err != nil {
		return fmt.Errorf("failed to create redis connector: %w", err)
	}

	// 初始化 NATS 连接
	l.natsConn, err = connector.NewNATS(&l.config.NATS, connector.WithLogger(l.logger))
	if err != nil {
		return fmt.Errorf("failed to create nats connector: %w", err)
	}

	// 初始化 Cache
	l.cache, err = cache.NewRedisCache(l.redisConn, cache.WithLogger(l.logger))
	if err != nil {
		return fmt.Errorf("failed to create cache: %w", err)
	}

	// 初始化 ID 生成器
	l.idGen, err = idgen.NewSnowflake(&l.config.IDGen)
	if err != nil {
		return fmt.Errorf("failed to create id generator: %w", err)
	}

	// 初始化 MQ Publisher
	l.mq, err = mq.NewNATSPublisher(l.natsConn, mq.WithLogger(l.logger))
	if err != nil {
		return fmt.Errorf("failed to create mq publisher: %w", err)
	}

	return nil
}

// initServices 初始化服务层
func (l *Logic) initServices() {
	// 注意：这里的 repo 需要外部注入实现
	// 目前使用 nil，实际使用时需要通过 SetRepositories 注入

	l.authService = service.NewAuthService(
		l.userRepo,
		l.tokenRepo,
		l.logger,
	)

	l.sessionService = service.NewSessionService(
		l.sessionRepo,
		l.contactRepo,
		l.messageRepo,
		l.userRepo,
		l.logger,
	)

	l.chatService = service.NewChatService(
		l.sessionRepo,
		l.messageRepo,
		l.idGen,
		l.mq,
		l.logger,
	)

	l.gatewayOpsService = service.NewGatewayOpsService(
		l.cache,
		l.logger,
	)
}

// SetRepositories 设置仓储层实现（外部注入）
func (l *Logic) SetRepositories(
	userRepo repo.UserRepository,
	tokenRepo repo.TokenRepository,
	sessionRepo repo.SessionRepository,
	contactRepo repo.ContactRepository,
	messageRepo repo.MessageRepository,
) {
	l.userRepo = userRepo
	l.tokenRepo = tokenRepo
	l.sessionRepo = sessionRepo
	l.contactRepo = contactRepo
	l.messageRepo = messageRepo

	// 重新初始化服务层
	l.initServices()
}

// Run 启动 Logic 服务
func (l *Logic) Run() error {
	l.logger.Info("starting logic service", clog.String("addr", l.config.ServerAddr))

	// 创建 Gin 路由
	router := gin.Default()

	// 注册 gRPC 服务（使用 ConnectRPC）
	l.registerServices(router)

	// 健康检查
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	})

	// 创建 HTTP/2 服务器（支持 gRPC）
	l.httpServer = &http.Server{
		Addr:    l.config.ServerAddr,
		Handler: h2c.NewHandler(router, &http2.Server{}),
	}

	l.logger.Info("logic service started", clog.String("addr", l.config.ServerAddr))

	if err := l.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		l.logger.Error("logic service error", clog.Error(err))
		return err
	}

	return nil
}

// registerServices 注册所有 gRPC 服务
func (l *Logic) registerServices(router *gin.Engine) {
	// AuthService
	authPath, authHandler := logicv1connect.NewAuthServiceHandler(l.authService)
	router.Any(authPath+"*any", gin.WrapH(authHandler))

	// SessionService
	sessionPath, sessionHandler := logicv1connect.NewSessionServiceHandler(l.sessionService)
	router.Any(sessionPath+"*any", gin.WrapH(sessionHandler))

	// ChatService
	chatPath, chatHandler := logicv1connect.NewChatServiceHandler(l.chatService)
	router.Any(chatPath+"*any", gin.WrapH(chatHandler))

	// GatewayOpsService
	opsPath, opsHandler := logicv1connect.NewGatewayOpsServiceHandler(l.gatewayOpsService)
	router.Any(opsPath+"*any", gin.WrapH(opsHandler))

	l.logger.Info("registered all gRPC services")
}

// Close 关闭 Logic 服务
func (l *Logic) Close() error {
	l.logger.Info("shutting down logic service")

	l.cancel()

	// 关闭 HTTP 服务器
	if l.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := l.httpServer.Shutdown(ctx); err != nil {
			l.logger.Error("failed to shutdown http server", clog.Error(err))
		}
	}

	// 关闭 MQ
	if l.mq != nil {
		l.mq.Close()
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
