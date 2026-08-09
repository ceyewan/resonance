package pilot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/db"
	"github.com/ceyewan/genesis/mq"
	"github.com/ceyewan/genesis/registry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/pilot/config"
	"github.com/ceyewan/resonance/pilot/coordinator"
	"github.com/ceyewan/resonance/pilot/identity"
	"github.com/ceyewan/resonance/pilot/ingress"
	"github.com/ceyewan/resonance/pilot/logicclient"
	"github.com/ceyewan/resonance/pilot/mutation"
	pilotobservability "github.com/ceyewan/resonance/pilot/observability"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
	"github.com/ceyewan/resonance/pilot/runtime/pi"
	"github.com/ceyewan/resonance/pilot/runtime/remote"
	"github.com/ceyewan/resonance/pilot/session"
	"github.com/ceyewan/resonance/pilot/stream"
	"github.com/ceyewan/resonance/pilot/toolbroker"
	"github.com/ceyewan/resonance/pkg/grpctrace"
	"github.com/ceyewan/resonance/pkg/health"
	"github.com/ceyewan/resonance/pkg/serviceauth"
	"github.com/ceyewan/resonance/repo"
)

type ingressComponent interface {
	Start() error
	Stop() error
}

type workerComponent interface {
	Start(context.Context) error
	StopClaiming()
	Drain(context.Context) error
	AbortActive()
	Errors() <-chan error
}

type brokerComponent interface {
	Start() error
	Close(context.Context) error
	Errors() <-chan error
}

type healthComponent interface {
	Start() error
	Stop(context.Context) error
	SetReady(bool)
}

type maintenanceComponent interface {
	Start(context.Context) error
	Stop()
	Errors() <-chan error
}

type mutationComponent interface {
	Start(context.Context) error
	Stop() error
	Errors() <-chan error
}

type Pilot struct {
	config *config.Config
	logger clog.Logger

	ingress     ingressComponent
	workers     workerComponent
	runtime     pilotruntime.AgentRuntime
	broker      brokerComponent
	health      healthComponent
	maintenance maintenanceComponent
	mutations   mutationComponent

	closeResources func() error
	ctx            context.Context
	cancel         context.CancelFunc
	errors         chan error

	mu        sync.Mutex
	started   bool
	closing   bool
	closeOnce sync.Once
	closeErr  error
	watchWG   sync.WaitGroup
}

// New builds the production Pilot composition root. Run controls network
// intake; New only creates dependencies and fails with reverse-order cleanup.
func New() (*Pilot, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	logger, err := clog.New(&cfg.Log, clog.WithTraceContext())
	if err != nil {
		return nil, fmt.Errorf("pilot logger: %w", err)
	}
	return newProduction(cfg, logger)
}

func newProduction(cfg *config.Config, logger clog.Logger) (_ *Pilot, returnedErr error) {
	ctx, cancel := context.WithCancel(context.Background())
	cleanup := newCleanupStack()
	defer func() {
		if returnedErr != nil {
			cancel()
			returnedErr = errors.Join(returnedErr, cleanup.close())
		}
	}()

	telemetry, err := pilotobservability.New(cfg.Observability)
	if err != nil {
		return nil, err
	}
	cleanup.add(func() error {
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelShutdown()
		return telemetry.Close(shutdownContext)
	})

	postgres, err := connector.NewPostgreSQL(&cfg.PostgreSQL, connector.WithLogger(logger), connector.WithMeter(telemetry.Meter()))
	if err != nil {
		return nil, fmt.Errorf("pilot postgresql init: %w", err)
	}
	cleanup.add(postgres.Close)
	if err := postgres.Connect(ctx); err != nil {
		return nil, fmt.Errorf("pilot postgresql connect: %w", err)
	}
	database, err := db.New(&db.Config{Driver: "postgresql"}, db.WithPostgreSQLConnector(postgres), db.WithLogger(logger))
	if err != nil {
		return nil, fmt.Errorf("pilot database: %w", err)
	}
	cleanup.add(database.Close)

	nats, err := connector.NewNATS(&cfg.NATS, connector.WithLogger(logger), connector.WithMeter(telemetry.Meter()))
	if err != nil {
		return nil, fmt.Errorf("pilot nats init: %w", err)
	}
	cleanup.add(nats.Close)
	if err := nats.Connect(ctx); err != nil {
		return nil, fmt.Errorf("pilot nats connect: %w", err)
	}
	mqClient, err := mq.New(&mq.Config{
		Driver: mq.DriverNATSJetStream, JetStream: &cfg.JetStream,
	}, mq.WithNATSConnector(nats), mq.WithLogger(logger), mq.WithMeter(telemetry.Meter()))
	if err != nil {
		return nil, fmt.Errorf("pilot MQ: %w", err)
	}
	cleanup.add(func() error {
		drainContext, cancelDrain := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelDrain()
		return errors.Join(mqClient.Drain(drainContext), mqClient.Close())
	})

	etcd, err := connector.NewEtcd(&cfg.Etcd, connector.WithLogger(logger), connector.WithMeter(telemetry.Meter()))
	if err != nil {
		return nil, fmt.Errorf("pilot etcd init: %w", err)
	}
	cleanup.add(etcd.Close)
	if err := etcd.Connect(ctx); err != nil {
		return nil, fmt.Errorf("pilot etcd connect: %w", err)
	}
	serviceRegistry, err := registry.New(etcd, cfg.Registry.ToRegistryConfig(), registry.WithLogger(logger), registry.WithMeter(telemetry.Meter()))
	if err != nil {
		return nil, fmt.Errorf("pilot registry: %w", err)
	}
	cleanup.add(func() error {
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelShutdown()
		return serviceRegistry.Shutdown(shutdownContext)
	})

	dialContext, cancelDial := context.WithTimeout(ctx, 5*time.Second)
	logicConnection, err := serviceRegistry.GetConnection(dialContext, cfg.LogicServiceName,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpctrace.UnaryClientInterceptor()),
		grpc.WithChainStreamInterceptor(grpctrace.StreamClientInterceptor()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1<<20), grpc.MaxCallSendMsgSize(1<<20)),
		grpc.WithDefaultServiceConfig(logicClientServiceConfig),
	)
	cancelDial()
	if err != nil {
		return nil, fmt.Errorf("pilot logic connection: %w", err)
	}
	cleanup.add(logicConnection.Close)

	userRepo, err := repo.NewUserRepo(database, repo.WithUserRepoLogger(logger))
	if err != nil {
		return nil, fmt.Errorf("pilot user repo: %w", err)
	}
	sessionRepo, err := repo.NewSessionRepo(database, repo.WithSessionRepoLogger(logger))
	if err != nil {
		return nil, fmt.Errorf("pilot session repo: %w", err)
	}
	runRepo, err := repo.NewAgentRunRepo(database, repo.WithAgentRunRepoLogger(logger))
	if err != nil {
		return nil, fmt.Errorf("pilot agent run repo: %w", err)
	}
	budgetPolicy, err := runRepo.GetAgentBudgetPolicy(ctx, cfg.TenantID)
	if err != nil {
		return nil, fmt.Errorf("pilot requires a provisioned agent budget policy: %w", err)
	}
	if !budgetPolicy.Enabled {
		return nil, fmt.Errorf("pilot requires an enabled agent budget policy for tenant %q", cfg.TenantID)
	}
	identityRepo, err := repo.NewIdentityRepo(database)
	if err != nil {
		return nil, fmt.Errorf("pilot identity repo: %w", err)
	}
	principals, err := identity.NewAuthoritativePrincipalResolver(cfg.TenantID, userRepo, identityRepo)
	if err != nil {
		return nil, err
	}
	iamReader, err := identity.NewAuthoritativeIAMReader(cfg.TenantID, userRepo, identityRepo)
	if err != nil {
		return nil, err
	}
	preparationRepo, err := repo.NewAgentMutationPreparationRepo(database)
	if err != nil {
		return nil, fmt.Errorf("pilot mutation preparation repo: %w", err)
	}
	executionRepo, err := repo.NewAgentToolExecutionRepo(database)
	if err != nil {
		return nil, fmt.Errorf("pilot tool execution repo: %w", err)
	}
	auditRepo, err := repo.NewAgentAuditRepo(database)
	if err != nil {
		return nil, fmt.Errorf("pilot agent audit repo: %w", err)
	}
	signer, err := serviceauth.NewSigner(cfg.ServiceAuth.ServiceID, []byte(cfg.ServiceAuth.Secret))
	if err != nil {
		return nil, fmt.Errorf("pilot service signer: %w", err)
	}
	mutationLogic, err := logicclient.NewAgentIAMMutationClient(
		logicv1.NewAgentApprovalServiceClient(logicConnection),
		logicv1.NewAgentIAMMutationServiceClient(logicConnection),
		signer,
		cfg.AgentBot,
	)
	if err != nil {
		return nil, err
	}
	mutationService, err := mutation.NewService(mutation.Config{
		TenantID: cfg.TenantID, AgentBotUsername: cfg.AgentBot, ApprovalTTL: cfg.Mutation.ApprovalTTL,
		ReconcileEvery: cfg.Mutation.ReconcileEvery, BatchSize: cfg.Mutation.BatchSize,
	}, preparationRepo, executionRepo, auditRepo, principals, mutationLogic)
	if err != nil {
		return nil, err
	}
	mutationWorker, err := mutation.NewComponent(mutation.ComponentConfig{
		Topic: cfg.Mutation.ApprovalTopic, QueueGroup: cfg.Mutation.QueueGroup, MaxInflight: cfg.Mutation.MaxInflight,
	}, mqClient, mutationService)
	if err != nil {
		return nil, err
	}

	sessionManager, err := session.NewLocalManager(session.LocalConfig{
		Root: cfg.Session.Root, MaxSnapshotBytes: cfg.Session.MaxSnapshotBytes,
		RolloverBytes: cfg.Session.RolloverBytes, RolloverEntryCount: cfg.Session.RolloverEntryCount,
	})
	if err != nil {
		return nil, fmt.Errorf("pilot session manager: %w", err)
	}
	cleanup.add(sessionManager.Close)
	sessionGC, err := session.NewGarbageCollector(session.GCConfig{
		LockID: sessionManager.GCLockID(), Interval: cfg.Session.GCInterval, Grace: cfg.Session.GCGrace,
	}, runRepo, sessionManager)
	if err != nil {
		return nil, fmt.Errorf("pilot session garbage collector: %w", err)
	}

	capabilities, err := toolbroker.NewCapabilityManager([]byte(cfg.Broker.CapabilitySecret), cfg.Broker.CapabilityTTL)
	if err != nil {
		return nil, fmt.Errorf("pilot capability manager: %w", err)
	}
	broker, err := toolbroker.New(toolbroker.Config{
		Address: cfg.Broker.Address, ProfileID: cfg.Profile.ID, ProfileVersion: cfg.Profile.Version,
		MaxRequestBytes: cfg.Broker.MaxRequestBytes, MaxResponseBytes: cfg.Broker.MaxResponseBytes,
		RequestTimeout: cfg.Broker.RequestTimeout,
	}, capabilities, runRepo, userRepo,
		toolbroker.WithPrincipalReader(principals),
		toolbroker.WithIAMReader(iamReader),
		toolbroker.WithMutationPreparer(mutationService),
	)
	if err != nil {
		return nil, fmt.Errorf("pilot tool broker: %w", err)
	}

	var runtimeAdapter pilotruntime.AgentRuntime
	switch cfg.Runtime.Mode {
	case config.RuntimeModeLocal:
		if err := ensurePrivateDirectory(cfg.Runtime.WorkDir); err != nil {
			return nil, err
		}
		if err := validateRuntimeFiles(cfg.Runtime.Binary, cfg.Runtime.ExtensionPath); err != nil {
			return nil, err
		}
		if err := pi.PreparePinnedAgentDirectory(cfg.Runtime.AgentDir); err != nil {
			return nil, err
		}
		runtimeEnvironment, environmentErr := cfg.RuntimeEnvironment()
		if environmentErr != nil {
			return nil, environmentErr
		}
		adapter, adapterErr := pi.New(pi.Config{
			Binary: cfg.Runtime.Binary, ExpectedVersion: cfg.Runtime.ExpectedVersion,
			ExtensionPath: cfg.Runtime.ExtensionPath, WorkDir: cfg.Runtime.WorkDir, AgentDir: cfg.Runtime.AgentDir,
			ToolBrokerURL: cfg.ToolBrokerURL(), Environment: runtimeEnvironment,
			MaxFrameBytes: cfg.Runtime.MaxFrameBytes, MaxOutputBytes: cfg.Runtime.MaxOutputBytes,
			MaxStderrBytes: cfg.Runtime.MaxStderrBytes, EventQueueSize: cfg.Runtime.EventQueueSize,
			StartupEventLimit: cfg.Runtime.StartupEventLimit, EventOfferTimeout: cfg.Runtime.EventOfferTimeout,
			CommandTimeout: cfg.Runtime.CommandTimeout, ProbeTimeout: cfg.Runtime.ProbeTimeout,
			AbortGrace: cfg.Runtime.AbortGrace, TermGrace: cfg.Runtime.TermGrace, KillGrace: cfg.Runtime.KillGrace,
		})
		if adapterErr != nil {
			return nil, fmt.Errorf("pilot Pi runtime: %w", adapterErr)
		}
		runtimeAdapter = adapter
	case config.RuntimeModeRemote:
		client, clientErr := remote.NewClient(remote.ClientConfig{
			SocketPath: cfg.Runtime.SocketPath, MaxRequestBytes: cfg.Runtime.RemoteMaxRequestBytes,
			MaxFrameBytes: cfg.Runtime.MaxFrameBytes, EventQueueSize: cfg.Runtime.EventQueueSize,
			DialTimeout: cfg.Runtime.RemoteDialTimeout,
		})
		if clientErr != nil {
			return nil, fmt.Errorf("pilot remote runtime: %w", clientErr)
		}
		cleanup.add(func() error { client.Close(); return nil })
		runtimeAdapter = client
	default:
		return nil, fmt.Errorf("unsupported pilot runtime mode %q", cfg.Runtime.Mode)
	}

	profileSnapshot := pilotruntime.ProfileSnapshot{
		ID: cfg.Profile.ID, Version: cfg.Profile.Version, Provider: cfg.Profile.Provider,
		Model: cfg.Profile.Model, SystemPrompt: cfg.Profile.SystemPrompt,
	}
	profiles, err := identity.NewStaticProfileResolver(cfg.TenantID, profileSnapshot)
	if err != nil {
		return nil, err
	}
	finalWriter, err := logicclient.NewFinalMessageWriter(
		logicv1.NewChatServiceClient(logicConnection), signer, cfg.AgentBot, cfg.Profile.MaxFinalBytes,
	)
	if err != nil {
		return nil, err
	}
	historyBuilder, err := logicclient.NewHistoryPromptBuilder(
		logicv1.NewSessionServiceClient(logicConnection), signer, cfg.AgentBot,
		cfg.Profile.HistoryLimit, cfg.Profile.MaxHistoryPromptBytes,
	)
	if err != nil {
		return nil, err
	}
	streamSink, err := stream.NewSink(stream.Config{
		Topic: cfg.Stream.Topic, BotUsername: cfg.AgentBot,
		FlushInterval: cfg.Stream.FlushInterval, PublishTimeout: cfg.Stream.PublishTimeout,
		MaxStreams: cfg.Stream.MaxStreams, MaxPendingBytes: cfg.Stream.MaxPendingBytes,
		MaxChunkBytes: cfg.Stream.MaxChunkBytes,
	}, mqClient)
	if err != nil {
		return nil, fmt.Errorf("pilot stream sink: %w", err)
	}
	observedStreamSink, err := telemetry.ObserveRuntimeEvents(streamSink)
	if err != nil {
		return nil, fmt.Errorf("pilot observed stream sink: %w", err)
	}
	cleanup.add(func() error {
		streamContext, cancelStream := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelStream()
		return streamSink.Close(streamContext)
	})

	workerID := buildWorkerID(cfg.Service.Name)
	processor, err := coordinator.New(coordinator.Config{
		TenantID: cfg.TenantID, ProfileID: cfg.Profile.ID, ProfileVersion: cfg.Profile.Version,
		WorkerID: workerID, LeaseDuration: cfg.Worker.LeaseTTL,
		HeartbeatInterval: cfg.Worker.HeartbeatInterval, RunTimeout: cfg.Worker.RunTimeout,
		RetryBackoff: cfg.Worker.RetryBackoff, MaxProviderCalls: cfg.Profile.MaxProviderCalls,
	}, coordinator.Dependencies{
		Runs: runRepo, Runtime: runtimeAdapter, Sessions: sessionManager, Profiles: profiles,
		Principals: principals, Capabilities: capabilities, FinalMessages: finalWriter,
		History: historyBuilder, Events: observedStreamSink,
	})
	if err != nil {
		return nil, fmt.Errorf("pilot coordinator: %w", err)
	}
	workers, err := coordinator.NewWorkerPool(coordinator.WorkerConfig{
		WorkerCount: cfg.Worker.Count, PollInterval: cfg.Worker.PollInterval,
		RecoveryInterval: cfg.Worker.RecoveryInterval,
	}, processor)
	if err != nil {
		return nil, err
	}

	admission, err := ingress.NewSingleTenantAdmission(cfg.TenantID, cfg.AgentBot, sessionRepo, userRepo, principals, ingress.Admission{
		ProfileID: cfg.Profile.ID, ProfileVersion: cfg.Profile.Version, RuntimeKind: "pi",
		RuntimeVersion: cfg.Runtime.ExpectedVersion, BridgeVersion: cfg.Profile.BridgeVersion,
		ModelProvider: cfg.Profile.Provider, ModelID: cfg.Profile.Model, MaxAttempts: cfg.Worker.MaxAttempts,
	})
	if err != nil {
		return nil, err
	}
	handler, err := ingress.NewHandler(ingress.HandlerConfig{
		TenantID: cfg.TenantID, BotUsername: cfg.AgentBot, MaxPromptBytes: cfg.Ingress.MaxPromptBytes,
	}, runRepo, admission)
	if err != nil {
		return nil, err
	}
	consumer, err := ingress.NewConsumer(ingress.ConsumerConfig{
		Topic: cfg.Ingress.Topic, QueueGroup: cfg.Ingress.QueueGroup, DLQTopic: cfg.Ingress.DLQTopic,
		MaxInflight: cfg.Ingress.MaxInflight,
	}, mqClient, handler)
	if err != nil {
		return nil, err
	}

	pilotContext, pilotCancel := context.WithCancel(context.Background())
	cancel()
	p := &Pilot{
		config: cfg, logger: logger, ingress: consumer, workers: workers, runtime: runtimeAdapter,
		broker: broker, health: health.NewServer(cfg.HTTPAddr(), logger), maintenance: sessionGC, mutations: mutationWorker,
		closeResources: cleanup.take(),
		ctx:            pilotContext, cancel: pilotCancel, errors: make(chan error, 1),
	}
	return p, nil
}

func (p *Pilot) Run() error {
	p.mu.Lock()
	if p.started || p.closing {
		p.mu.Unlock()
		return fmt.Errorf("pilot is already started or closing")
	}
	p.started = true
	p.mu.Unlock()

	healthStarted, brokerStarted, workersStarted, ingressStarted, maintenanceStarted, mutationsStarted := false, false, false, false, false, false
	rollback := func(cause error) error {
		result := cause
		p.health.SetReady(false)
		if ingressStarted {
			result = errors.Join(result, p.ingress.Stop())
		}
		if workersStarted {
			p.workers.StopClaiming()
			p.workers.AbortActive()
			rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			result = errors.Join(result, p.workers.Drain(rollbackContext))
			cancel()
		}
		if mutationsStarted {
			result = errors.Join(result, p.mutations.Stop())
		}
		if maintenanceStarted {
			p.maintenance.Stop()
		}
		runtimeContext, cancelRuntime := context.WithTimeout(context.Background(), 5*time.Second)
		result = errors.Join(result, p.runtime.Shutdown(runtimeContext))
		cancelRuntime()
		if brokerStarted {
			brokerContext, cancelBroker := context.WithTimeout(context.Background(), 5*time.Second)
			result = errors.Join(result, p.broker.Close(brokerContext))
			cancelBroker()
		}
		if healthStarted {
			healthContext, cancelHealth := context.WithTimeout(context.Background(), 5*time.Second)
			result = errors.Join(result, p.health.Stop(healthContext))
			cancelHealth()
		}
		return result
	}

	if err := p.health.Start(); err != nil {
		return rollback(fmt.Errorf("pilot health start: %w", err))
	}
	healthStarted = true
	if err := p.runtime.Probe(p.ctx); err != nil {
		return rollback(fmt.Errorf("pilot runtime probe: %w", err))
	}
	if p.maintenance != nil {
		if err := p.maintenance.Start(p.ctx); err != nil {
			return rollback(fmt.Errorf("pilot maintenance start: %w", err))
		}
		maintenanceStarted = true
	}
	if p.mutations != nil {
		if err := p.mutations.Start(p.ctx); err != nil {
			return rollback(fmt.Errorf("pilot IAM mutation component start: %w", err))
		}
		mutationsStarted = true
	}
	if err := p.broker.Start(); err != nil {
		return rollback(fmt.Errorf("pilot tool broker start: %w", err))
	}
	brokerStarted = true
	if err := p.workers.Start(p.ctx); err != nil {
		return rollback(fmt.Errorf("pilot workers start: %w", err))
	}
	workersStarted = true
	if err := p.ingress.Start(); err != nil {
		return rollback(fmt.Errorf("pilot ingress start: %w", err))
	}
	ingressStarted = true
	p.startWatchers()
	p.health.SetReady(true)
	p.logger.Info("pilot service ready")
	return nil
}

func (p *Pilot) Errors() <-chan error  { return p.errors }
func (p *Pilot) Done() <-chan struct{} { return p.ctx.Done() }

func (p *Pilot) startWatchers() {
	p.watchWG.Add(2)
	go func() {
		defer p.watchWG.Done()
		select {
		case err := <-p.broker.Errors():
			if err != nil {
				p.reportFatal(err)
			}
		case <-p.ctx.Done():
		}
	}()
	if p.maintenance != nil {
		p.watchWG.Go(func() {
			for {
				select {
				case err := <-p.maintenance.Errors():
					if err != nil {
						p.logger.Warn("pilot maintenance iteration failed", clog.Error(err))
					}
				case <-p.ctx.Done():
					return
				}
			}
		})
	}
	if p.mutations != nil {
		p.watchWG.Go(func() {
			for {
				select {
				case err := <-p.mutations.Errors():
					if err != nil {
						p.logger.Warn("pilot IAM mutation reconciliation failed", clog.Error(err))
					}
				case <-p.ctx.Done():
					return
				}
			}
		})
	}
	go func() {
		defer p.watchWG.Done()
		for {
			select {
			case err := <-p.workers.Errors():
				if err != nil {
					p.logger.Warn("pilot worker iteration failed", clog.Error(err))
				}
			case <-p.ctx.Done():
				return
			}
		}
	}()
}

func (p *Pilot) reportFatal(err error) {
	p.health.SetReady(false)
	select {
	case p.errors <- err:
	default:
	}
	p.cancel()
}

func (p *Pilot) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closing = true
		p.mu.Unlock()
		p.health.SetReady(false)
		var closeErr error
		if p.ingress != nil {
			closeErr = errors.Join(closeErr, p.ingress.Stop())
		}
		if p.workers != nil {
			p.workers.StopClaiming()
			drainContext, cancelDrain := context.WithTimeout(context.Background(), p.config.Worker.ShutdownDrainTimeout)
			drainErr := p.workers.Drain(drainContext)
			cancelDrain()
			if drainErr != nil {
				p.workers.AbortActive()
				abortContext, cancelAbort := context.WithTimeout(context.Background(), 10*time.Second)
				closeErr = errors.Join(closeErr, p.workers.Drain(abortContext))
				cancelAbort()
			}
		}
		if p.runtime != nil {
			runtimeContext, cancelRuntime := context.WithTimeout(context.Background(), 10*time.Second)
			closeErr = errors.Join(closeErr, p.runtime.Shutdown(runtimeContext))
			cancelRuntime()
		}
		if p.mutations != nil {
			closeErr = errors.Join(closeErr, p.mutations.Stop())
		}
		if p.maintenance != nil {
			p.maintenance.Stop()
		}
		if p.broker != nil {
			brokerContext, cancelBroker := context.WithTimeout(context.Background(), 5*time.Second)
			closeErr = errors.Join(closeErr, p.broker.Close(brokerContext))
			cancelBroker()
		}
		p.cancel()
		p.watchWG.Wait()
		if p.closeResources != nil {
			closeErr = errors.Join(closeErr, p.closeResources())
		}
		if p.health != nil {
			healthContext, cancelHealth := context.WithTimeout(context.Background(), 5*time.Second)
			closeErr = errors.Join(closeErr, p.health.Stop(healthContext))
			cancelHealth()
		}
		p.closeErr = closeErr
	})
	return p.closeErr
}

// Payload-bound service authentication issues one nonce per application call.
// gRPC transparent retries would reuse that metadata and be rejected as a
// replay. Coordinator retries re-enter the writer, obtain a fresh signature,
// and rely on the final message idempotency key to recover the original ACK.
const logicClientServiceConfig = `{"methodConfig":[]}`

func buildWorkerID(serviceName string) string {
	hostname, _ := os.Hostname()
	value := serviceName + ":" + hostname + ":" + strconv.Itoa(os.Getpid())
	if len(value) <= 128 {
		return value
	}
	return serviceName + ":" + strconv.Itoa(os.Getpid())
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create pilot runtime directory: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secure pilot runtime directory: %w", err)
	}
	return nil
}

func validateRuntimeFiles(binary, extension string) error {
	if _, err := os.Stat(binary); err != nil {
		return fmt.Errorf("stat pinned Pi binary: %w", err)
	}
	info, err := os.Lstat(extension)
	if err != nil {
		return fmt.Errorf("stat trusted Pi Bridge: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("trusted Pi Bridge must be a regular non-symlink file")
	}
	absolute, err := filepath.Abs(extension)
	if err != nil || absolute != extension {
		return fmt.Errorf("trusted Pi Bridge path is invalid")
	}
	return nil
}

type cleanupStack struct {
	mu    sync.Mutex
	funcs []func() error
}

func newCleanupStack() *cleanupStack              { return &cleanupStack{} }
func (s *cleanupStack) add(function func() error) { s.funcs = append(s.funcs, function) }
func (s *cleanupStack) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result error
	for index := len(s.funcs) - 1; index >= 0; index-- {
		result = errors.Join(result, s.funcs[index]())
	}
	s.funcs = nil
	return result
}
func (s *cleanupStack) take() func() error { return s.close }
