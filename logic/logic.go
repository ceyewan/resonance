package logic

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ceyewan/genesis/auth"
	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/db"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/mq"
	"github.com/ceyewan/genesis/registry"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/logic/config"
	"github.com/ceyewan/resonance/logic/job"
	"github.com/ceyewan/resonance/logic/observability"
	"github.com/ceyewan/resonance/logic/server"
	"github.com/ceyewan/resonance/logic/service"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/pkg/health"
	"github.com/ceyewan/resonance/pkg/serviceauth"
	"github.com/ceyewan/resonance/repo"
)

// Logic Logic 服务生命周期管理器
type Logic struct {
	config    *config.Config
	logger    clog.Logger
	registry  registry.Registry
	serviceID string

	// 服务器
	grpcServer   *server.GRPCServer
	healthServer *health.Server

	// 后台任务
	outboxRelay *job.OutboxRelay

	// 资源
	resources *resources
	ctx       context.Context
	cancel    context.CancelFunc
	errors    chan error
	closeOnce sync.Once
	closeErr  error
}

// resources 内部资源聚合
type resources struct {
	etcdConn       connector.EtcdConnector
	redisConn      connector.RedisConnector
	postgresConn   connector.PostgreSQLConnector
	natsConn       connector.NATSConnector
	mqClient       mq.MQ
	authenticator  auth.Authenticator
	msgIDGen       idgen.Generator // 用于 MsgID (Snowflake)
	sessionIDGen   idgen.Generator // 用于 SessionID (Snowflake)
	sequencer      idgen.Sequencer // 用于会话 SeqID (基于 Redis)
	dbInstance     db.DB
	instanceIDStop func() error // 实例 ID 保活停止函数

	// Repos
	userRepo             repo.UserRepo
	identityRepo         repo.IdentityRepo
	sessionRepo          repo.SessionRepo
	messageRepo          repo.MessageRepo
	routerRepo           repo.RouterRepo
	agentApprovalRepo    repo.AgentApprovalRepo
	agentIAMMutationRepo repo.AgentIAMMutationRepo
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
		errors: make(chan error, 1),
	}

	if err := l.initComponents(); err != nil {
		_ = l.Close()
		return nil, err
	}

	return l, nil
}

// initComponents 初始化所有组件
func (l *Logic) initComponents() error {
	// 1. 日志
	logger, err := clog.New(&l.config.Log, clog.WithTraceContext())
	if err != nil {
		return fmt.Errorf("logger init: %w", err)
	}
	l.logger = logger

	// 2. 可观测性
	obsCfg := &observability.Config{
		Version: l.config.Observability.Version, InstanceID: l.config.Observability.InstanceID,
		Environment: l.config.Observability.Environment,
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
		return fmt.Errorf("init observability: %w", err)
	}

	// 3. 核心资源
	res, err := l.initResources()
	if err != nil {
		return err
	}
	l.resources = res

	// 3. 基于 Redis 抢占分配唯一实例 ID (WorkerID)
	allocator, err := idgen.NewAllocator(&idgen.AllocatorConfig{
		Driver:    idgen.DriverRedis,
		KeyPrefix: l.config.WorkerID.GetKey(),
		MaxID:     l.config.WorkerID.GetMaxID(),
	}, idgen.WithRedisConnector(res.redisConn), idgen.WithMeter(observability.Meter()))
	if err != nil {
		return fmt.Errorf("create allocator: %w", err)
	}
	instanceID, err := allocator.Allocate(l.ctx)
	if err != nil {
		return errors.Join(fmt.Errorf("allocate instance id: %w", err), allocator.Stop())
	}
	l.serviceID = fmt.Sprintf("%s-%d", l.config.Service.Name, instanceID)
	res.instanceIDStop = allocator.Stop

	// 3.1 使用分配到的 instanceID 初始化 ID 生成器
	msgIDGen, err := idgen.NewGenerator(&idgen.GeneratorConfig{
		Mode:     idgen.GeneratorModeSingleDC,
		WorkerID: instanceID,
	}, idgen.WithMeter(observability.Meter()))
	if err != nil {
		return fmt.Errorf("msgID generator init: %w", err)
	}
	res.msgIDGen = msgIDGen
	// Message/Event ID 与生成型 Session ID 共享同一状态机。即使未来两个
	// namespace 被汇总或比较，也不会因两个相同 worker tuple 的独立序列而碰撞。
	res.sessionIDGen = msgIDGen

	// 监听保活失败
	go func() {
		if err, ok := <-allocator.KeepAlive(l.ctx); ok && err != nil {
			l.logger.Error("instance id keepalive failed", clog.Error(err))
			l.reportFatal(fmt.Errorf("instance id keepalive: %w", err))
		}
	}()
	go l.monitorRegistryLeaseFailures()

	// 4. 服务层
	sessionSvc := service.NewSessionService(
		res.sessionRepo,
		res.messageRepo,
		res.userRepo,
		res.sessionIDGen,
		res.msgIDGen,
		res.sequencer,
		res.mqClient,
		logger,
		service.WithTenantMembershipReader(res.identityRepo),
		service.WithAgentSessionPolicy(service.AgentSessionPolicy{
			BotUsername:          l.config.AgentBot.Username,
			BotNickname:          l.config.AgentBot.Nickname,
			UserAssistantVersion: l.config.AgentSessions.UserAssistantProfileVersion,
			IAMAdminVersion:      l.config.AgentSessions.IAMAdminProfileVersion,
		}),
	)
	authSvc := service.NewAuthService(
		res.userRepo, res.identityRepo, res.sessionRepo, res.authenticator, logger,
		service.WithDefaultAgentSessionProvisioner(sessionSvc),
	)
	chatSvc := service.NewChatService(res.sessionRepo, res.messageRepo, res.msgIDGen, res.sequencer, res.mqClient, logger)
	presenceSvc := service.NewPresenceService(res.routerRepo, logger)
	agentApprovalSvc := service.NewAgentApprovalService(
		res.agentApprovalRepo,
		service.NewIdentitySystemScopeAuthorizer(res.identityRepo),
		res.msgIDGen,
		res.mqClient,
		logger,
		service.AgentApprovalPolicy{
			PilotServiceID:   l.config.ServiceAuth.IAMPilotServiceID,
			ReadScope:        l.config.AgentApproval.ReadScope,
			DecideScope:      l.config.AgentApproval.DecideScope,
			AllowSelfApprove: l.config.AgentApproval.AllowSelfApprove,
		},
	)
	agentIAMMutationSvc := service.NewAgentIAMMutationService(
		res.agentApprovalRepo,
		res.agentIAMMutationRepo,
		service.NewIdentitySystemScopeAuthorizer(res.identityRepo),
		logger,
		service.AgentIAMMutationPolicy{
			PilotServiceID: l.config.ServiceAuth.IAMPilotServiceID,
			WriteScope:     model.ScopeIAMUsersWrite,
			DecideScope:    model.ScopeAgentApprovalDecide,
		},
	)

	// 5. 后台任务
	l.outboxRelay = job.NewOutboxRelay(res.messageRepo, res.mqClient, logger, &l.config.Outbox)

	// 6. gRPC Server
	serverOptions := []server.Option{
		server.WithProtectedActor(l.config.AgentBot.Username),
		server.WithAgentApprovalService(agentApprovalSvc),
		server.WithAgentIAMMutationService(agentIAMMutationSvc),
		server.WithGatewayServiceID(l.config.ServiceAuth.GatewayServiceID),
	}
	if l.config.ServiceAuth.PilotSecret != "" {
		serverOptions = append(serverOptions, server.WithServiceProfile(
			l.config.ServiceAuth.PilotServiceID,
			model.AgentProfileUserAssistant,
			l.config.AgentSessions.UserAssistantProfileVersion,
		))
	}
	if l.config.ServiceAuth.IAMPilotSecret != "" {
		serverOptions = append(serverOptions, server.WithServiceProfile(
			l.config.ServiceAuth.IAMPilotServiceID,
			model.AgentProfileIAMAdmin,
			l.config.AgentSessions.IAMAdminProfileVersion,
		))
	}
	servicePolicies := map[string]serviceauth.ServicePolicy{
		l.config.ServiceAuth.GatewayServiceID: {
			Secret:                  []byte(l.config.ServiceAuth.GatewaySecret),
			AllowAnyActor:           true,
			AllowedMethods:          gatewayServiceMethods(),
			AllowAnyTenant:          true,
			RequirePrincipalVersion: true,
		},
	}
	if l.config.ServiceAuth.PilotSecret != "" {
		servicePolicies[l.config.ServiceAuth.PilotServiceID] = serviceauth.ServicePolicy{
			Secret:         []byte(l.config.ServiceAuth.PilotSecret),
			AllowedActors:  map[string]struct{}{l.config.AgentBot.Username: {}},
			AllowedMethods: userPilotServiceMethods(),
			AllowedTenants: map[string]struct{}{l.config.ServiceAuth.PilotTenantID: {}},
		}
	}
	if l.config.ServiceAuth.IAMPilotSecret != "" {
		servicePolicies[l.config.ServiceAuth.IAMPilotServiceID] = serviceauth.ServicePolicy{
			Secret:         []byte(l.config.ServiceAuth.IAMPilotSecret),
			AllowedActors:  map[string]struct{}{l.config.AgentBot.Username: {}},
			AllowedMethods: iamPilotServiceMethods(),
			AllowedTenants: map[string]struct{}{l.config.ServiceAuth.IAMPilotTenantID: {}},
		}
	}
	nonceStore, err := serviceauth.NewRedisNonceStore(
		res.redisConn.GetClient(), l.config.ServiceAuth.NonceKeyPrefix,
	)
	if err != nil {
		return fmt.Errorf("service auth nonce store: %w", err)
	}
	verifier, err := serviceauth.NewVerifier(serviceauth.VerifierConfig{
		MaxSkew:  l.config.ServiceAuth.MaxSkew,
		Services: servicePolicies,
	}, serviceauth.WithNonceStore(nonceStore))
	if err != nil {
		return fmt.Errorf("service auth verifier: %w", err)
	}
	serverOptions = append(serverOptions, server.WithServiceAuth(verifier))
	l.grpcServer = server.NewGRPCServer(
		l.config.GetServerAddr(),
		logger,
		authSvc,
		sessionSvc,
		chatSvc,
		presenceSvc,
		serverOptions...,
	)

	// 7. 健康检查 Server
	l.healthServer = health.NewServer(l.config.GetHTTPAddr(), logger)

	return nil
}

func gatewayServiceMethods() map[string]struct{} {
	return map[string]struct{}{
		logicv1.ChatService_SendEvent_FullMethodName:               {},
		logicv1.SessionService_GetSessionList_FullMethodName:       {},
		logicv1.SessionService_CreateSession_FullMethodName:        {},
		logicv1.SessionService_CreateAgentSession_FullMethodName:   {},
		logicv1.SessionService_GetHistoryEvents_FullMethodName:     {},
		logicv1.SessionService_GetContactList_FullMethodName:       {},
		logicv1.SessionService_SearchUser_FullMethodName:           {},
		logicv1.SessionService_UpdateReadPosition_FullMethodName:   {},
		logicv1.SessionService_PullInboxDelta_FullMethodName:       {},
		logicv1.AgentApprovalService_DecideApproval_FullMethodName: {},
		logicv1.AgentApprovalService_GetApproval_FullMethodName:    {},
		logicv1.AgentApprovalService_ListApprovals_FullMethodName:  {},
	}
}

func userPilotServiceMethods() map[string]struct{} {
	return map[string]struct{}{
		logicv1.ChatService_SendEvent_FullMethodName:           {},
		logicv1.SessionService_GetHistoryEvents_FullMethodName: {},
	}
}

func iamPilotServiceMethods() map[string]struct{} {
	methods := userPilotServiceMethods()
	methods[logicv1.AgentApprovalService_CreateApproval_FullMethodName] = struct{}{}
	methods[logicv1.AgentIAMMutationService_PreviewTenantMembershipStatus_FullMethodName] = struct{}{}
	methods[logicv1.AgentIAMMutationService_GetExecutionApproval_FullMethodName] = struct{}{}
	methods[logicv1.AgentIAMMutationService_ExecuteTenantMembershipStatus_FullMethodName] = struct{}{}
	return methods
}

// initResources 初始化资源
func (l *Logic) initResources() (_ *resources, returnedErr error) {
	cleanup := make([]func() error, 0, 16)
	defer func() {
		if returnedErr == nil {
			return
		}
		for index := len(cleanup) - 1; index >= 0; index-- {
			returnedErr = errors.Join(returnedErr, cleanup[index]())
		}
	}()
	// DB (PostgreSQL)
	postgresConn, err := connector.NewPostgreSQL(&l.config.PostgreSQL, connector.WithLogger(l.logger), connector.WithMeter(observability.Meter()))
	if err != nil {
		return nil, fmt.Errorf("postgresql init: %w", err)
	}
	cleanup = append(cleanup, postgresConn.Close)
	if err := postgresConn.Connect(l.ctx); err != nil {
		return nil, fmt.Errorf("postgresql connect: %w", err)
	}
	dbInstance, err := db.New(&db.Config{Driver: "postgresql"}, db.WithPostgreSQLConnector(postgresConn), db.WithLogger(l.logger))
	if err != nil {
		return nil, fmt.Errorf("db init: %w", err)
	}
	cleanup = append(cleanup, dbInstance.Close)

	// Redis
	redisConn, err := connector.NewRedis(&l.config.Redis, connector.WithLogger(l.logger), connector.WithMeter(observability.Meter()))
	if err != nil {
		return nil, fmt.Errorf("redis init: %w", err)
	}
	cleanup = append(cleanup, redisConn.Close)
	if err := redisConn.Connect(l.ctx); err != nil {
		return nil, fmt.Errorf("redis connect: %w", err)
	}

	// NATS
	natsConn, err := connector.NewNATS(&l.config.NATS, connector.WithLogger(l.logger), connector.WithMeter(observability.Meter()))
	if err != nil {
		return nil, fmt.Errorf("nats init: %w", err)
	}
	cleanup = append(cleanup, natsConn.Close)
	if err := natsConn.Connect(l.ctx); err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	mqClient, err := mq.New(&mq.Config{
		Driver:    mq.DriverNATSJetStream,
		JetStream: &l.config.JetStream,
	}, mq.WithNATSConnector(natsConn), mq.WithLogger(l.logger), mq.WithMeter(observability.Meter()))
	if err != nil {
		return nil, fmt.Errorf("mq client init: %w", err)
	}
	cleanup = append(cleanup, mqClient.Close)

	// Etcd & Registry
	etcdConn, err := connector.NewEtcd(&l.config.Etcd, connector.WithLogger(l.logger), connector.WithMeter(observability.Meter()))
	if err != nil {
		return nil, fmt.Errorf("etcd init: %w", err)
	}
	cleanup = append(cleanup, etcdConn.Close)
	if err := etcdConn.Connect(l.ctx); err != nil {
		return nil, fmt.Errorf("etcd connect: %w", err)
	}
	reg, err := registry.New(etcdConn, l.config.Registry.ToRegistryConfig(), registry.WithLogger(l.logger), registry.WithMeter(observability.Meter()))
	if err != nil {
		return nil, fmt.Errorf("registry init: %w", err)
	}
	cleanup = append(cleanup, reg.Close)

	// Authenticator
	authenticator, err := auth.New(&l.config.Auth, auth.WithLogger(l.logger), auth.WithMeter(observability.Meter()))
	if err != nil {
		return nil, fmt.Errorf("auth init: %w", err)
	}

	// ID Generators
	// 注意：msgIDGen 和 sessionIDGen 稍后在 initComponents 中根据分配到的 instanceID 初始化
	var msgIDGen idgen.Generator
	var sessionIDGen idgen.Generator
	sequencer, err := idgen.NewSequencer(&idgen.SequencerConfig{
		Driver:    idgen.DriverRedis,
		KeyPrefix: "resonance:logic:seq",
		Step:      1,
	}, idgen.WithRedisConnector(redisConn), idgen.WithLogger(l.logger), idgen.WithMeter(observability.Meter()))
	if err != nil {
		return nil, fmt.Errorf("sequencer init: %w", err)
	}

	// Repos
	// 假设 NewUserRepo 和 NewMessageRepo 签名与 SessionRepo 类似
	userRepo, err := repo.NewUserRepo(dbInstance) // 需要确认签名
	if err != nil {
		return nil, fmt.Errorf("user repo init: %w", err)
	}
	cleanup = append(cleanup, userRepo.Close)
	identityRepo, err := repo.NewIdentityRepo(dbInstance)
	if err != nil {
		return nil, fmt.Errorf("identity repo init: %w", err)
	}
	cleanup = append(cleanup, identityRepo.Close)
	messageRepo, err := repo.NewMessageRepo(dbInstance) // 需要确认签名
	if err != nil {
		return nil, fmt.Errorf("message repo init: %w", err)
	}
	cleanup = append(cleanup, messageRepo.Close)
	sessionRepo, err := repo.NewSessionRepo(dbInstance, repo.WithSessionRepoLogger(l.logger))
	if err != nil {
		return nil, fmt.Errorf("session repo init: %w", err)
	}
	cleanup = append(cleanup, sessionRepo.Close)
	routerRepo, err := repo.NewRouterRepo(redisConn, repo.WithLogger(l.logger))
	if err != nil {
		return nil, fmt.Errorf("router repo init: %w", err)
	}
	cleanup = append(cleanup, routerRepo.Close)
	agentApprovalRepo, err := repo.NewAgentApprovalRepo(dbInstance)
	if err != nil {
		return nil, fmt.Errorf("agent approval repo init: %w", err)
	}
	cleanup = append(cleanup, agentApprovalRepo.Close)
	agentIAMMutationRepo, err := repo.NewAgentIAMMutationRepo(dbInstance, l.config.AgentBot.Username)
	if err != nil {
		return nil, fmt.Errorf("agent IAM mutation repo init: %w", err)
	}
	cleanup = append(cleanup, agentIAMMutationRepo.Close)
	l.registry = reg

	return &resources{
		postgresConn:         postgresConn,
		redisConn:            redisConn,
		natsConn:             natsConn,
		etcdConn:             etcdConn,
		mqClient:             mqClient,
		dbInstance:           dbInstance,
		authenticator:        authenticator,
		msgIDGen:             msgIDGen,
		sessionIDGen:         sessionIDGen,
		sequencer:            sequencer,
		userRepo:             userRepo,
		identityRepo:         identityRepo,
		sessionRepo:          sessionRepo,
		messageRepo:          messageRepo,
		routerRepo:           routerRepo,
		agentApprovalRepo:    agentApprovalRepo,
		agentIAMMutationRepo: agentIAMMutationRepo,
	}, nil
}

// Run 启动服务
func (l *Logic) Run() error {
	l.logger.Info("starting logic service...")

	// 启动健康检查服务器
	if err := l.healthServer.Start(); err != nil {
		return fmt.Errorf("health server start: %w", err)
	}

	// 启动后台任务
	l.outboxRelay.StartAsync(l.ctx)

	// 启动 gRPC Server
	go func() {
		if err := l.grpcServer.Start(); err != nil {
			l.logger.Error("grpc server failed", clog.Error(err))
			l.reportFatal(fmt.Errorf("grpc server: %w", err))
		}
	}()

	// 注册服务
	if err := l.registerService(); err != nil {
		return err
	}

	// 服务注册成功后才标记就绪
	l.healthServer.SetReady(true)
	return nil
}

// Errors reports the first background failure that requires process shutdown.
func (l *Logic) Errors() <-chan error { return l.errors }

// Done is closed when the Logic lifecycle context is canceled.
func (l *Logic) Done() <-chan struct{} { return l.ctx.Done() }

func (l *Logic) reportFatal(err error) {
	if err == nil || l.ctx.Err() != nil {
		return
	}
	select {
	case l.errors <- err:
	default:
	}
	if l.healthServer != nil {
		l.healthServer.SetReady(false)
	}
	l.cancel()
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
	l.closeOnce.Do(func() { l.closeErr = l.close() })
	return l.closeErr
}

func (l *Logic) monitorRegistryLeaseFailures() {
	for {
		select {
		case failure, ok := <-l.registry.LeaseFailures():
			if !ok {
				return
			}
			l.logger.Error("logic registry lease lost", clog.String("service_id", failure.ServiceID), clog.Error(failure.Err))
			if l.healthServer != nil {
				l.healthServer.SetReady(false)
			}
			l.reportFatal(fmt.Errorf("registry lease for %s: %w", failure.ServiceID, failure.Err))
		case <-l.ctx.Done():
			return
		}
	}
}

func (l *Logic) close() error {
	var result error
	if l.logger != nil {
		l.logger.Info("shutting down logic service...")
	}

	// 标记服务未就绪
	if l.healthServer != nil {
		l.healthServer.SetReady(false)
	}

	l.cancel()

	// 1. 停止健康检查服务器
	if l.healthServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		result = errors.Join(result, l.healthServer.Stop(shutdownCtx))
		cancel()
	}

	// 2. 注销服务
	if l.registry != nil {
		registryCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if l.serviceID != "" {
			if err := l.registry.Deregister(registryCtx, l.serviceID); err != nil {
				if l.logger != nil {
					l.logger.Warn("deregister logic service failed", clog.Error(err))
				}
				result = errors.Join(result, fmt.Errorf("deregister logic: %w", err))
			}
		}
		result = errors.Join(result, l.registry.Shutdown(registryCtx))
		cancel()
	}

	// 3. 停止 gRPC 服务器
	if l.grpcServer != nil {
		l.grpcServer.Stop()
	}

	if l.outboxRelay != nil {
		waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		result = errors.Join(result, l.outboxRelay.Wait(waitCtx))
		cancel()
	}

	// 4. 按依赖逆序释放资源
	if l.resources != nil {
		if l.resources.instanceIDStop != nil {
			result = errors.Join(result, l.resources.instanceIDStop())
		}
		if l.resources.routerRepo != nil {
			result = errors.Join(result, l.resources.routerRepo.Close())
		}
		if l.resources.sessionRepo != nil {
			result = errors.Join(result, l.resources.sessionRepo.Close())
		}
		if l.resources.userRepo != nil {
			result = errors.Join(result, l.resources.userRepo.Close())
		}
		if l.resources.identityRepo != nil {
			result = errors.Join(result, l.resources.identityRepo.Close())
		}
		if l.resources.messageRepo != nil {
			result = errors.Join(result, l.resources.messageRepo.Close())
		}
		if l.resources.agentApprovalRepo != nil {
			result = errors.Join(result, l.resources.agentApprovalRepo.Close())
		}
		if l.resources.agentIAMMutationRepo != nil {
			result = errors.Join(result, l.resources.agentIAMMutationRepo.Close())
		}
		mqCtx, cancelMQ := context.WithTimeout(context.Background(), 10*time.Second)
		if l.resources.mqClient != nil {
			result = errors.Join(result, l.resources.mqClient.Drain(mqCtx), l.resources.mqClient.Close())
		}
		cancelMQ()
		if l.resources.dbInstance != nil {
			result = errors.Join(result, l.resources.dbInstance.Close())
		}
		if l.resources.etcdConn != nil {
			result = errors.Join(result, l.resources.etcdConn.Close())
		}
		if l.resources.natsConn != nil {
			result = errors.Join(result, l.resources.natsConn.Close())
		}
		if l.resources.redisConn != nil {
			result = errors.Join(result, l.resources.redisConn.Close())
		}
		if l.resources.postgresConn != nil {
			result = errors.Join(result, l.resources.postgresConn.Close())
		}
	}

	// 5. 关闭可观测性组件
	observabilityCtx, cancelObservability := context.WithTimeout(context.Background(), 5*time.Second)
	observability.RecordShutdownFlushProbe(l.logger)
	result = errors.Join(result, observability.Shutdown(observabilityCtx))
	cancelObservability()
	return result
}
