// Package pilot runs one shared Bot with an independent Pi Session per private conversation.
package pilot

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/pilot/config"
	"github.com/ceyewan/resonance/pilot/coordinator"
	"github.com/ceyewan/resonance/pilot/ingress"
	"github.com/ceyewan/resonance/pilot/logicclient"
	pilotobservability "github.com/ceyewan/resonance/pilot/observability"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
	"github.com/ceyewan/resonance/pilot/runtime/pi"
	"github.com/ceyewan/resonance/pilot/session"
	"github.com/ceyewan/resonance/pilot/stream"
	"github.com/ceyewan/resonance/pkg/grpctrace"
	"github.com/ceyewan/resonance/pkg/health"
	"github.com/ceyewan/resonance/pkg/serviceauth"
	"github.com/ceyewan/resonance/repo"
)

type Pilot struct {
	config      *config.Config
	logger      clog.Logger
	ingress     *ingress.Consumer
	workers     *coordinator.WorkerPool
	runtime     pilotruntime.AgentRuntime
	health      *health.Server
	maintenance *session.GarbageCollector
	ctx         context.Context
	cancel      context.CancelFunc
	errors      chan error
	cleanup     []func() error
	closeOnce   sync.Once
	closeErr    error
}

func New() (_ *Pilot, err error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	logger, err := clog.New(&cfg.Log, clog.WithTraceContext())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Pilot{config: cfg, logger: logger, ctx: ctx, cancel: cancel, errors: make(chan error, 1)}
	defer func() {
		if err != nil {
			err = errors.Join(err, p.Close())
		}
	}()
	telemetry, err := pilotobservability.New(cfg.Observability)
	if err != nil {
		return nil, err
	}
	p.cleanup = append(p.cleanup, func() error {
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		return telemetry.Close(c)
	})
	postgres, err := connector.NewPostgreSQL(&cfg.PostgreSQL, connector.WithLogger(logger), connector.WithMeter(telemetry.Meter()))
	if err != nil {
		return nil, err
	}
	p.cleanup = append(p.cleanup, postgres.Close)
	if err = postgres.Connect(ctx); err != nil {
		return nil, err
	}
	database, err := db.New(&db.Config{Driver: "postgresql"}, db.WithPostgreSQLConnector(postgres), db.WithLogger(logger))
	if err != nil {
		return nil, err
	}
	p.cleanup = append(p.cleanup, database.Close)
	nats, err := connector.NewNATS(&cfg.NATS, connector.WithLogger(logger), connector.WithMeter(telemetry.Meter()))
	if err != nil {
		return nil, err
	}
	p.cleanup = append(p.cleanup, nats.Close)
	if err = nats.Connect(ctx); err != nil {
		return nil, err
	}
	queue, err := mq.New(&mq.Config{Driver: mq.DriverNATSJetStream, JetStream: &cfg.JetStream}, mq.WithNATSConnector(nats), mq.WithLogger(logger), mq.WithMeter(telemetry.Meter()))
	if err != nil {
		return nil, err
	}
	p.cleanup = append(p.cleanup, queue.Close)
	etcd, err := connector.NewEtcd(&cfg.Etcd, connector.WithLogger(logger), connector.WithMeter(telemetry.Meter()))
	if err != nil {
		return nil, err
	}
	p.cleanup = append(p.cleanup, etcd.Close)
	if err = etcd.Connect(ctx); err != nil {
		return nil, err
	}
	reg, err := registry.New(cfg.Registry.ToRegistryConfig(), registry.WithEtcdConnector(etcd), registry.WithLogger(logger), registry.WithMeter(telemetry.Meter()))
	if err != nil {
		return nil, err
	}
	p.cleanup = append(p.cleanup, reg.Close)
	dialCtx, stopDial := context.WithTimeout(ctx, 5*time.Second)
	connection, err := reg.GetConnection(dialCtx, cfg.LogicServiceName, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpctrace.UnaryClientInterceptor()), grpc.WithDefaultServiceConfig(logicClientServiceConfig))
	stopDial()
	if err != nil {
		return nil, err
	}
	p.cleanup = append(p.cleanup, connection.Close)
	users, err := repo.NewUserRepo(database)
	if err != nil {
		return nil, err
	}
	conversations, err := repo.NewSessionRepo(database, repo.WithSessionRepoLogger(logger))
	if err != nil {
		return nil, err
	}
	runs, err := repo.NewAgentRunRepo(database, repo.WithAgentRunRepoLogger(logger))
	if err != nil {
		return nil, err
	}
	sessions, err := session.NewLocalManager(session.LocalConfig{Root: cfg.Session.Root, MaxSnapshotBytes: cfg.Session.MaxSnapshotBytes, RolloverBytes: cfg.Session.RolloverBytes, RolloverEntryCount: cfg.Session.RolloverEntryCount})
	if err != nil {
		return nil, err
	}
	p.cleanup = append(p.cleanup, sessions.Close)
	p.maintenance, err = session.NewGarbageCollector(session.GCConfig{LockID: sessions.GCLockID(), Interval: cfg.Session.GCInterval, Grace: cfg.Session.GCGrace}, runs, sessions)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(cfg.Runtime.WorkDir, 0700); err != nil {
		return nil, err
	}
	if err = os.Chmod(cfg.Runtime.WorkDir, 0700); err != nil {
		return nil, err
	}
	if err = pi.PreparePinnedAgentDirectory(cfg.Runtime.AgentDir); err != nil {
		return nil, err
	}
	environment, err := cfg.RuntimeEnvironment()
	if err != nil {
		return nil, err
	}
	p.runtime, err = pi.New(pi.Config{Binary: cfg.Runtime.Binary, ExpectedVersion: cfg.Runtime.ExpectedVersion, ExtensionPath: cfg.Runtime.ExtensionPath, WorkDir: cfg.Runtime.WorkDir, AgentDir: cfg.Runtime.AgentDir, Environment: environment})
	if err != nil {
		return nil, err
	}
	assistant := pilotruntime.ProfileSnapshot{ID: model.AgentProfileUserAssistant, Version: 1, Provider: cfg.Assistant.Provider, Model: cfg.Assistant.Model, SystemPrompt: cfg.Assistant.SystemPrompt}
	signer, err := serviceauth.NewSigner(cfg.ServiceAuth.ServiceID, []byte(cfg.ServiceAuth.Secret))
	if err != nil {
		return nil, err
	}
	writer, err := logicclient.NewFinalMessageWriter(logicv1.NewChatServiceClient(connection), signer, cfg.AgentBot, cfg.Assistant.MaxFinalBytes)
	if err != nil {
		return nil, err
	}
	history, err := logicclient.NewHistoryPromptBuilder(logicv1.NewSessionServiceClient(connection), signer, cfg.AgentBot, cfg.Assistant.HistoryLimit, cfg.Assistant.MaxHistoryPromptBytes)
	if err != nil {
		return nil, err
	}
	sink, err := stream.NewSink(stream.Config{Topic: cfg.Stream.Topic, BotUsername: cfg.AgentBot, FlushInterval: cfg.Stream.FlushInterval, PublishTimeout: cfg.Stream.PublishTimeout, MaxStreams: cfg.Stream.MaxStreams, MaxPendingBytes: cfg.Stream.MaxPendingBytes, MaxChunkBytes: cfg.Stream.MaxChunkBytes}, queue)
	if err != nil {
		return nil, err
	}
	p.cleanup = append(p.cleanup, func() error {
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		return sink.Close(c)
	})
	events, err := telemetry.ObserveRuntimeEvents(sink)
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	processor, err := coordinator.New(coordinator.Config{Assistant: assistant, ProfileID: assistant.ID, ProfileVersion: assistant.Version, WorkerID: fmt.Sprintf("pilot:%s:%d", host, os.Getpid()), LeaseDuration: cfg.Worker.LeaseTTL, HeartbeatInterval: cfg.Worker.HeartbeatInterval, RunTimeout: cfg.Worker.RunTimeout, RetryBackoff: cfg.Worker.RetryBackoff, MaxOutputTokens: cfg.Assistant.MaxOutputTokens}, coordinator.Dependencies{Logger: logger, Runs: runs, Runtime: p.runtime, Sessions: sessions, History: history, FinalMessages: writer, Events: events})
	if err != nil {
		return nil, err
	}
	p.workers, err = coordinator.NewWorkerPool(coordinator.WorkerConfig{WorkerCount: cfg.Worker.Count, PollInterval: cfg.Worker.PollInterval, RecoveryInterval: cfg.Worker.RecoveryInterval}, processor)
	if err != nil {
		return nil, err
	}
	admission, err := ingress.NewConversationAdmission(cfg.AgentBot, conversations, users, ingress.Admission{ProfileID: assistant.ID, ProfileVersion: assistant.Version, RuntimeKind: "pi", RuntimeVersion: cfg.Runtime.ExpectedVersion, BridgeVersion: "1", ModelProvider: assistant.Provider, ModelID: assistant.Model, MaxAttempts: cfg.Worker.MaxAttempts})
	if err != nil {
		return nil, err
	}
	handler, err := ingress.NewHandler(ingress.HandlerConfig{BotUsername: cfg.AgentBot, MaxPromptBytes: cfg.Ingress.MaxPromptBytes}, runs, admission)
	if err != nil {
		return nil, err
	}
	p.ingress, err = ingress.NewConsumer(ingress.ConsumerConfig{Topic: cfg.Ingress.Topic, QueueGroup: cfg.Ingress.QueueGroup, DLQTopic: cfg.Ingress.DLQTopic, MaxInflight: cfg.Ingress.MaxInflight}, queue, handler)
	if err != nil {
		return nil, err
	}
	p.health = health.NewServer(cfg.HTTPAddr(), logger)
	return p, nil
}

func (p *Pilot) Run() error {
	if err := p.health.Start(); err != nil {
		return err
	}
	if err := p.runtime.Probe(p.ctx); err != nil {
		return err
	}
	if err := p.maintenance.Start(p.ctx); err != nil {
		return err
	}
	if err := p.workers.Start(p.ctx); err != nil {
		return err
	}
	if err := p.ingress.Start(); err != nil {
		return err
	}
	go func() {
		for {
			select {
			case err := <-p.workers.Errors():
				p.logger.Warn("Pilot run failed", clog.Error(err))
			case err := <-p.maintenance.Errors():
				p.logger.Warn("Pilot session cleanup failed", clog.Error(err))
			case err := <-p.ingress.Errors():
				p.reportFatal(err)
				return
			case <-p.ctx.Done():
				return
			}
		}
	}()
	p.health.SetReady(true)
	p.logger.Info("Pilot ready: one Bot, one private conversation per user")
	return nil
}
func (p *Pilot) Errors() <-chan error  { return p.errors }
func (p *Pilot) Done() <-chan struct{} { return p.ctx.Done() }

// reportFatal 处理无法自行恢复的后台失败：先摘掉 readiness，再把错误交给 main，
// 最后取消根 context 让进程带着明确原因退出，避免"进程还在但已经收不到消息"。
func (p *Pilot) reportFatal(err error) {
	p.logger.Error("Pilot fatal background failure", clog.Error(err))
	p.health.SetReady(false)
	select {
	case p.errors <- err:
	default:
	}
	p.cancel()
}
func (p *Pilot) Close() error {
	p.closeOnce.Do(func() {
		if p.health != nil {
			p.health.SetReady(false)
		}
		if p.ingress != nil {
			p.closeErr = errors.Join(p.closeErr, p.ingress.Stop())
		}
		if p.workers != nil {
			p.workers.StopClaiming()
			c, stop := context.WithTimeout(context.Background(), p.config.Worker.ShutdownDrainTimeout)
			err := p.workers.Drain(c)
			stop()
			if err != nil {
				p.workers.AbortActive()
			}
		}
		if p.runtime != nil {
			c, stop := context.WithTimeout(context.Background(), 10*time.Second)
			p.closeErr = errors.Join(p.closeErr, p.runtime.Shutdown(c))
			stop()
		}
		if p.workers != nil {
			c, stop := context.WithTimeout(context.Background(), 10*time.Second)
			p.closeErr = errors.Join(p.closeErr, p.workers.Drain(c))
			stop()
		}
		if p.maintenance != nil {
			p.maintenance.Stop()
		}
		p.cancel()
		for i := len(p.cleanup) - 1; i >= 0; i-- {
			p.closeErr = errors.Join(p.closeErr, p.cleanup[i]())
		}
		if p.health != nil {
			c, stop := context.WithTimeout(context.Background(), 5*time.Second)
			p.closeErr = errors.Join(p.closeErr, p.health.Stop(c))
			stop()
		}
	})
	return p.closeErr
}

// Retries obtain a fresh workload signature; final replies use a stable idempotency key.
const logicClientServiceConfig = `{"methodConfig":[]}`
