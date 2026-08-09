package runtimehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ceyewan/genesis/clog"

	"github.com/ceyewan/resonance/pilot/runtime/pi"
	"github.com/ceyewan/resonance/pilot/runtime/relay"
	"github.com/ceyewan/resonance/pilot/runtime/remote"
	"github.com/ceyewan/resonance/pkg/health"
)

type Host struct {
	config  *Config
	logger  clog.Logger
	runtime *pi.Adapter
	relay   *relay.Relay
	server  *remote.Server
	health  *health.Server
	errors  chan error
	done    chan struct{}
	cancel  context.CancelFunc

	mu        sync.Mutex
	started   bool
	closeOnce sync.Once
	doneOnce  sync.Once
	closeErr  error
}

func New() (*Host, error) {
	config, err := Load()
	if err != nil {
		return nil, err
	}
	logger, err := clog.New(&config.Log, clog.WithTraceContext())
	if err != nil {
		return nil, fmt.Errorf("runtime host logger: %w", err)
	}
	return newHost(config, logger)
}

func newHost(config *Config, logger clog.Logger) (*Host, error) {
	if config == nil {
		return nil, fmt.Errorf("runtime host config is required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(filepath.Dir(config.Remote.SocketPath)); err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(config.Runtime.WorkDir); err != nil {
		return nil, err
	}
	if err := validateRuntimeFiles(config.Runtime.Binary, config.Runtime.ExtensionPath); err != nil {
		return nil, err
	}
	if err := pi.PreparePinnedAgentDirectory(config.Runtime.AgentDir); err != nil {
		return nil, err
	}
	environment, err := config.RuntimeEnvironment()
	if err != nil {
		return nil, err
	}
	adapter, err := pi.New(pi.Config{
		Binary: config.Runtime.Binary, ExpectedVersion: config.Runtime.ExpectedVersion,
		ExtensionPath: config.Runtime.ExtensionPath, WorkDir: config.Runtime.WorkDir, AgentDir: config.Runtime.AgentDir,
		ToolBrokerURL: "http://" + config.Relay.ListenAddress, Environment: environment,
		MaxFrameBytes: config.Runtime.MaxFrameBytes, MaxOutputBytes: config.Runtime.MaxOutputBytes,
		MaxStderrBytes: config.Runtime.MaxStderrBytes, EventQueueSize: config.Runtime.EventQueueSize,
		StartupEventLimit: config.Runtime.StartupEventLimit, EventOfferTimeout: config.Runtime.EventOfferTimeout,
		CommandTimeout: config.Runtime.CommandTimeout, ProbeTimeout: config.Runtime.ProbeTimeout,
		AbortGrace: config.Runtime.AbortGrace, TermGrace: config.Runtime.TermGrace, KillGrace: config.Runtime.KillGrace,
	})
	if err != nil {
		return nil, fmt.Errorf("runtime host Pi adapter: %w", err)
	}
	toolRelay, err := relay.New(relay.Config{
		ListenAddress: config.Relay.ListenAddress, BrokerSocket: config.Relay.BrokerSocket,
		MaxRequestBytes: config.Relay.MaxRequestBytes, MaxResponseBytes: config.Relay.MaxResponseBytes,
		RequestTimeout: config.Relay.RequestTimeout, MaxConcurrent: config.Relay.MaxConcurrent,
	})
	if err != nil {
		return nil, fmt.Errorf("runtime host tool relay: %w", err)
	}
	server, err := remote.NewServer(remote.ServerConfig{
		SocketPath: config.Remote.SocketPath, SessionRoot: config.Remote.SessionRoot,
		MaxRequestBytes: config.Remote.MaxRequestBytes, MaxFrameBytes: config.Remote.MaxFrameBytes,
		HeaderTimeout: config.Remote.HeaderTimeout,
	}, adapter)
	if err != nil {
		return nil, fmt.Errorf("runtime host remote server: %w", err)
	}
	return &Host{
		config: config, logger: logger, runtime: adapter, relay: toolRelay, server: server,
		health: health.NewServer(config.HTTPAddr(), logger), errors: make(chan error, 1), done: make(chan struct{}),
	}, nil
}

func (h *Host) Run() error {
	h.mu.Lock()
	if h.started {
		h.mu.Unlock()
		return fmt.Errorf("runtime host already started")
	}
	h.started = true
	h.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	rollback := func(cause error) error {
		h.health.SetReady(false)
		closeContext, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = h.server.Close(closeContext)
		_ = h.relay.Close(closeContext)
		_ = h.health.Stop(closeContext)
		cancel()
		return cause
	}
	if err := h.health.Start(); err != nil {
		return rollback(fmt.Errorf("runtime host health: %w", err))
	}
	if err := h.relay.Start(); err != nil {
		return rollback(fmt.Errorf("runtime host relay: %w", err))
	}
	if err := h.runtime.Probe(ctx); err != nil {
		return rollback(fmt.Errorf("runtime host Pi probe: %w", err))
	}
	if err := h.server.Start(); err != nil {
		return rollback(fmt.Errorf("runtime host UDS server: %w", err))
	}
	h.health.SetReady(true)
	go h.watch(ctx, h.relay.Errors())
	go h.watch(ctx, h.server.Errors())
	go h.watchShutdown(ctx)
	h.logger.Info("isolated Pi runtime host ready")
	return nil
}

func (h *Host) watch(ctx context.Context, source <-chan error) {
	select {
	case err := <-source:
		if err != nil {
			h.health.SetReady(false)
			select {
			case h.errors <- err:
			default:
			}
		}
	case <-ctx.Done():
	}
}

func (h *Host) Errors() <-chan error  { return h.errors }
func (h *Host) Done() <-chan struct{} { return h.done }

func (h *Host) watchShutdown(ctx context.Context) {
	select {
	case <-h.server.Shutdowns():
		h.health.SetReady(false)
		h.doneOnce.Do(func() { close(h.done) })
	case <-ctx.Done():
	}
}

func (h *Host) Close() error {
	h.closeOnce.Do(func() {
		h.health.SetReady(false)
		h.doneOnce.Do(func() { close(h.done) })
		if h.cancel != nil {
			h.cancel()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		h.closeErr = errors.Join(h.server.Close(ctx), h.relay.Close(ctx), h.health.Stop(ctx))
	})
	return h.closeErr
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create runtime host private directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("runtime host private path must be a non-symlink directory")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return fmt.Errorf("runtime host private path must not traverse symbolic links")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("protect runtime host private directory: %w", err)
	}
	return nil
}

func validateRuntimeFiles(binary, extension string) error {
	binaryInfo, err := os.Stat(binary)
	if err != nil || !binaryInfo.Mode().IsRegular() {
		return fmt.Errorf("pinned Pi binary must resolve to a regular file")
	}
	extensionInfo, err := os.Lstat(extension)
	if err != nil || !extensionInfo.Mode().IsRegular() || extensionInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("trusted Pi Bridge must be a regular non-symlink file")
	}
	return nil
}
