package pi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

// Adapter 以 per-active-run 子进程模型实现 AgentRuntime。
type Adapter struct {
	config    Config
	starter   ProcessStarter
	probed    atomic.Bool
	closing   atomic.Bool
	probeGate chan struct{}

	mu     sync.Mutex
	active map[string]*activeRun
}

// New 创建生产 Pi Adapter。
func New(cfg Config) (*Adapter, error) {
	return newAdapter(cfg, execProcessStarter{})
}

func newAdapter(cfg Config, starter ProcessStarter) (*Adapter, error) {
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if starter == nil {
		return nil, fmt.Errorf("pi process starter is required")
	}
	return &Adapter{
		config:    cfg,
		starter:   starter,
		probeGate: make(chan struct{}, 1),
		active:    make(map[string]*activeRun),
	}, nil
}

type activeRun struct {
	id              string
	process         Process
	client          *RPCClient
	stream          *eventStream
	ctx             context.Context
	cancel          context.CancelFunc
	request         pilotruntime.RunRequest
	state           State
	baseline        SessionStats
	started         atomic.Bool
	promptAttempted atomic.Bool
	settledObserved atomic.Bool

	processDone chan struct{}
	cleanupDone chan struct{}
	processMu   sync.Mutex
	processErr  error

	stderr     *boundedCapture
	stderrDone chan struct{}

	abortRequested atomic.Bool
	abortCh        chan abortRequest
}

type abortRequest struct {
	ack chan error
}

type startupEventCollector struct {
	stopCh   chan struct{}
	done     chan struct{}
	settled  chan struct{}
	stopOnce sync.Once
	events   []WireEvent
	err      error
}

type eventStream struct {
	events       chan pilotruntime.RuntimeEvent
	done         chan struct{}
	once         sync.Once
	lastSequence atomic.Uint64
	mu           sync.Mutex
	result       pilotruntime.RunResult
	err          error
}

func newEventStream(size int) *eventStream {
	return &eventStream{
		events: make(chan pilotruntime.RuntimeEvent, size),
		done:   make(chan struct{}),
	}
}

func (s *eventStream) Events() <-chan pilotruntime.RuntimeEvent { return s.events }

func (s *eventStream) Wait() (pilotruntime.RunResult, error) {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result, s.err
}

func (s *eventStream) finish(result pilotruntime.RunResult, err error) {
	s.once.Do(func() {
		s.mu.Lock()
		s.result = result
		s.err = err
		if err != nil {
			failed := pilotruntime.RuntimeEvent{
				Kind:     pilotruntime.EventFailed,
				Usage:    result.Usage,
				Error:    classifyRuntimeError(err),
				Sequence: s.lastSequence.Load() + 1,
			}
			select {
			case s.events <- failed:
			default:
				// 失败终态优先于可丢弃的进度事件，同时保持内存有界。
				select {
				case <-s.events:
				default:
				}
				select {
				case s.events <- failed:
				default:
				}
			}
		}
		close(s.events)
		close(s.done)
		s.mu.Unlock()
	})
}

func (s *eventStream) emit(ctx context.Context, event pilotruntime.RuntimeEvent, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case s.events <- event:
		s.lastSequence.Store(event.Sequence)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ErrEventBackpressure
	}
}

func classifyRuntimeError(err error) *pilotruntime.RuntimeError {
	classified := &pilotruntime.RuntimeError{Code: "runtime_failed", Message: "agent runtime failed"}
	switch {
	case errors.Is(err, context.Canceled):
		classified.Code = "canceled"
		classified.Message = "agent run canceled"
	case errors.Is(err, context.DeadlineExceeded):
		classified.Code = "deadline_exceeded"
		classified.Message = "agent runtime deadline exceeded"
		classified.Retryable = true
	case errors.Is(err, ErrEventBackpressure):
		classified.Code = "event_backpressure"
		classified.Message = "agent runtime event consumer is too slow"
		classified.Retryable = true
	case errors.Is(err, ErrMalformedJSON), errors.Is(err, ErrFrameTooLarge), errors.Is(err, ErrOutputTooLarge):
		classified.Code = "protocol_error"
		classified.Message = "agent runtime protocol failed"
	}
	return classified
}

// Run 启动 Pi、完成 get_state 核验并等待 prompt ACK 后返回 EventStream。
func (a *Adapter) Run(ctx context.Context, req pilotruntime.RunRequest) (pilotruntime.EventStream, error) {
	if a.closing.Load() {
		return nil, notStartedError(ErrRuntimeClosed)
	}
	if !a.probed.Load() {
		return nil, notStartedError(ErrRuntimeNotReady)
	}
	spec, err := a.config.buildProcessSpec(req)
	if err != nil {
		return nil, notStartedError(err)
	}

	runCtx, runCancel := context.WithCancel(ctx)
	run := &activeRun{
		id:          req.RunID,
		stream:      newEventStream(a.config.EventQueueSize),
		ctx:         runCtx,
		cancel:      runCancel,
		request:     req,
		processDone: make(chan struct{}),
		cleanupDone: make(chan struct{}),
		stderr:      newBoundedCapture(a.config.MaxStderrBytes),
		stderrDone:  make(chan struct{}),
		abortCh:     make(chan abortRequest, 1),
	}
	if err := a.reserve(run); err != nil {
		return nil, notStartedError(err)
	}

	process, err := a.starter.Start(runCtx, spec)
	if err != nil {
		result := a.failStartup(run, nil, err)
		return nil, pilotruntime.NewRunError(err, result.Usage)
	}
	run.process = process
	go run.waitProcess()
	go func() {
		drainStderr(process.Stderr(), run.stderr)
		close(run.stderrDone)
	}()

	run.client = NewRPCClient(process.Stdin(), process.Stdout(), ClientConfig{
		MaxFrameBytes:     a.config.MaxFrameBytes,
		MaxOutputBytes:    a.config.MaxOutputBytes,
		EventQueueSize:    a.config.EventQueueSize,
		EventOfferTimeout: a.config.EventOfferTimeout,
	})
	collector := collectStartupEvents(run.client, a.config.StartupEventLimit)

	commandCtx, cancel := context.WithTimeout(runCtx, a.config.CommandTimeout)
	state, err := run.client.GetState(commandCtx)
	cancel()
	if err == nil {
		err = verifyState(state, req)
		run.state = state
	}
	if err == nil {
		commandCtx, cancel = context.WithTimeout(runCtx, a.config.CommandTimeout)
		var commands commandsData
		commands, err = run.client.GetCommands(commandCtx)
		cancel()
		if err == nil {
			err = verifyBridgeCommands(commands, req.Profile)
		}
	}
	if err == nil {
		commandCtx, cancel = context.WithTimeout(runCtx, a.config.CommandTimeout)
		run.baseline, err = run.client.GetSessionStats(commandCtx)
		cancel()
		if err == nil {
			err = verifyUsageSnapshot(run, run.baseline)
		}
	}
	if err == nil {
		run.promptAttempted.Store(true)
		commandCtx, cancel = context.WithTimeout(runCtx, a.config.CommandTimeout)
		err = run.client.Prompt(commandCtx, req.Prompt)
		cancel()
	}
	if err != nil {
		if run.promptAttempted.Load() {
			err = errors.Join(err, a.abortUncertainStartup(run, collector))
		}
		result := a.failStartup(run, collector, err)
		return nil, pilotruntime.NewRunError(err, result.Usage)
	}

	initialEvents, collectorErr := collector.stop()
	if collectorErr != nil {
		result := a.failStartup(run, nil, collectorErr)
		return nil, pilotruntime.NewRunError(collectorErr, result.Usage)
	}
	run.started.Store(true)
	go a.monitor(run, initialEvents)
	return run.stream, nil
}

// Abort 请求指定 Run 优雅终止。重复调用是幂等的。
func (a *Adapter) Abort(ctx context.Context, runID string) error {
	a.mu.Lock()
	run, ok := a.active[runID]
	a.mu.Unlock()
	if !ok {
		return ErrRunNotFound
	}
	if !run.abortRequested.CompareAndSwap(false, true) {
		return nil
	}

	request := abortRequest{ack: make(chan error, 1)}
	// abortCh 对唯一首个请求有容量；调用方 Context 只限制等待，不撤销内部终止动作。
	run.abortCh <- request
	if !run.started.Load() {
		run.cancel()
	}
	select {
	case err := <-request.ack:
		return err
	case <-run.stream.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Shutdown 永久停止接收新 Run，并等待所有活动 Pi 子进程完成回收。
func (a *Adapter) Shutdown(ctx context.Context) error {
	a.closing.Store(true)
	a.mu.Lock()
	runs := make([]*activeRun, 0, len(a.active))
	for _, run := range a.active {
		runs = append(runs, run)
	}
	a.mu.Unlock()
	if len(runs) == 0 {
		return nil
	}

	done := make(chan bool, len(runs))
	for _, run := range runs {
		go func(run *activeRun) {
			_ = a.Abort(ctx, run.id)
			select {
			case <-run.cleanupDone:
				done <- true
			case <-ctx.Done():
				done <- false
			}
		}(run)
	}
	for range runs {
		select {
		case cleaned := <-done:
			if !cleaned {
				return ctx.Err()
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Probe 校验二进制可启动且版本精确匹配。它不会加载 Session 或 Extension。
func (a *Adapter) Probe(ctx context.Context) error {
	if a.closing.Load() {
		return ErrRuntimeClosed
	}
	// Probe 会更新全局 readiness。串行化探测，避免一个失败的并发 Probe
	// 被另一个成功 Probe 覆盖后仍错误地允许新 Run。
	select {
	case a.probeGate <- struct{}{}:
		defer func() { <-a.probeGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if a.closing.Load() {
		return ErrRuntimeClosed
	}
	a.probed.Store(false)
	probeCtx, cancel := context.WithTimeout(ctx, a.config.ProbeTimeout)
	defer cancel()

	process, err := a.starter.Start(probeCtx, ProcessSpec{
		Path: a.config.Binary,
		Args: []string{"--version"},
		Env:  append([]string(nil), a.config.Environment...),
		Dir:  a.config.WorkDir,
	})
	if err != nil {
		return err
	}
	stderr := newBoundedCapture(a.config.MaxStderrBytes)
	stderrDone := make(chan struct{})
	go func() {
		drainStderr(process.Stderr(), stderr)
		close(stderrDone)
	}()
	stdout := newBoundedCapture(a.config.MaxFrameBytes)
	stdoutDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(stdout, process.Stdout())
		close(stdoutDone)
	}()
	waitCh := make(chan error, 1)
	go func() { waitCh <- process.Wait() }()

	select {
	case waitErr := <-waitCh:
		<-stdoutDone
		<-stderrDone
		if waitErr != nil {
			return fmt.Errorf("pi version process failed: %w", waitErr)
		}
		_, truncated := stdout.stats()
		if truncated {
			return fmt.Errorf("read pi version: %w", ErrFrameTooLarge)
		}
		value := strings.TrimSpace(string(stdout.snapshot()))
		if value != a.config.ExpectedVersion {
			return fmt.Errorf("pi version mismatch: got %s, want %q", safeDiagnostic(value), a.config.ExpectedVersion)
		}
		if a.closing.Load() {
			return ErrRuntimeClosed
		}
		a.probed.Store(true)
		return nil
	case <-stdout.exceeded:
		_ = process.Kill()
		<-waitCh
		<-stdoutDone
		<-stderrDone
		return fmt.Errorf("read pi version: %w", ErrFrameTooLarge)
	case <-probeCtx.Done():
		_ = process.Kill()
		<-waitCh
		<-stdoutDone
		<-stderrDone
		return probeCtx.Err()
	}
}

func (a *Adapter) monitor(run *activeRun, initialEvents []WireEvent) {
	var sequence uint64
	var result pilotruntime.RunResult
	var terminalErr error

	defer func() {
		if result.Usage == nil {
			result.Usage = a.bestEffortUsage(run)
		}
		run.cancel()
		a.shutdown(run)
		a.release(run.id, run)
		run.stream.finish(result, terminalErr)
		close(run.cleanupDone)
	}()

	consume := func(event WireEvent) bool {
		settled, emitted, err := a.handleEvent(run, event, sequence+1)
		if err != nil {
			terminalErr = a.abortAndWait(run, abortRequest{}, err)
			return true
		}
		if emitted {
			sequence++
		}
		if settled {
			run.settledObserved.Store(true)
			result, terminalErr = a.collectResult(run, sequence+1)
			return true
		}
		return false
	}
	if slices.ContainsFunc(initialEvents, consume) {
		return
	}

	for {
		// 每处理一个事件前先检查控制信号，持续事件洪泛不能饿死 Abort/Cancel/Exit。
		select {
		case request := <-run.abortCh:
			terminalErr = a.abortAndWait(run, request, context.Canceled)
			return
		case <-run.ctx.Done():
			terminalErr = a.abortAndWait(run, abortRequest{}, run.ctx.Err())
			return
		case <-run.processDone:
			terminalErr = run.getProcessErr()
			if terminalErr == nil {
				terminalErr = fmt.Errorf("pi process exited before agent_settled")
			}
			return
		default:
		}

		select {
		case event, ok := <-run.client.Events():
			if !ok {
				terminalErr = run.client.Wait()
				if terminalErr == nil {
					terminalErr = fmt.Errorf("pi rpc stdout closed before agent_settled")
				}
				return
			}
			if consume(event) {
				return
			}
		case request := <-run.abortCh:
			terminalErr = a.abortAndWait(run, request, context.Canceled)
			return
		case <-run.ctx.Done():
			terminalErr = a.abortAndWait(run, abortRequest{}, run.ctx.Err())
			return
		case <-run.processDone:
			terminalErr = run.getProcessErr()
			if terminalErr == nil {
				terminalErr = fmt.Errorf("pi process exited before agent_settled")
			}
			return
		}
	}
}

func (a *Adapter) handleEvent(run *activeRun, event WireEvent, sequence uint64) (bool, bool, error) {
	mapped, disposition, err := mapWireEvent(event, sequence)
	if err != nil {
		return false, false, err
	}
	switch disposition {
	case eventIgnored:
		return false, false, nil
	case eventSettled:
		return true, false, nil
	case eventMapped:
		if err := run.stream.emit(context.Background(), mapped, a.config.EventOfferTimeout); err != nil {
			return false, false, err
		}
		return false, true, nil
	default:
		return false, false, nil
	}
}

func (a *Adapter) collectResult(run *activeRun, settledSequence uint64) (pilotruntime.RunResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.config.CommandTimeout)
	defer cancel()

	text, err := run.client.GetLastAssistantText(ctx)
	if err != nil {
		return pilotruntime.RunResult{}, err
	}
	if text == nil {
		return pilotruntime.RunResult{}, fmt.Errorf("pi settled without an assistant message")
	}
	stats, err := run.client.GetSessionStats(ctx)
	if err != nil {
		return pilotruntime.RunResult{Usage: unknownUsage()}, err
	}
	usage, err := usageDelta(run, stats)
	if err != nil {
		return pilotruntime.RunResult{Usage: unknownUsage()}, err
	}
	leafID, err := run.client.GetLeafEntryID(ctx)
	if err != nil {
		return pilotruntime.RunResult{}, err
	}

	result := pilotruntime.RunResult{
		FinalText:   *text,
		SessionID:   stats.SessionID,
		SessionFile: stats.SessionFile,
		LeafEntryID: leafID,
		Usage:       usage,
	}
	if err := run.stream.emit(context.Background(), pilotruntime.RuntimeEvent{
		Kind:     pilotruntime.EventSettled,
		Usage:    result.Usage,
		Sequence: settledSequence,
	}, a.config.EventOfferTimeout); err != nil {
		return pilotruntime.RunResult{}, err
	}
	return result, nil
}

func (a *Adapter) abortAndWait(run *activeRun, request abortRequest, cause error) error {
	commandCtx, cancel := context.WithTimeout(context.Background(), a.config.CommandTimeout)
	defer cancel()
	abortErr := run.client.Abort(commandCtx)
	if request.ack != nil {
		request.ack <- abortErr
	}
	if abortErr != nil && !errors.Is(abortErr, io.EOF) {
		return errors.Join(cause, fmt.Errorf("pi abort command: %w", abortErr))
	}

	timer := time.NewTimer(a.config.AbortGrace)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-run.client.Events():
			if !ok {
				return cause
			}
			_, disposition, err := mapWireEvent(event, 0)
			if err != nil {
				return errors.Join(cause, err)
			}
			if disposition == eventSettled {
				run.settledObserved.Store(true)
				return cause
			}
		case <-run.processDone:
			return cause
		case <-timer.C:
			return cause
		}
	}
}

func (a *Adapter) shutdown(run *activeRun) {
	if run.client != nil {
		_ = run.client.Close()
	}
	if run.process == nil {
		return
	}
	if waitDone(run.processDone, a.config.TermGrace) {
		waitDone(run.stderrDone, a.config.KillGrace)
		return
	}
	_ = run.process.Signal(syscall.SIGTERM)
	if waitDone(run.processDone, a.config.KillGrace) {
		waitDone(run.stderrDone, a.config.KillGrace)
		return
	}
	_ = run.process.Kill()
	// SIGKILL 后仍必须等唯一 Wait goroutine 完成，不能让 EventStream.Wait
	// 在子进程尚未回收时返回。
	<-run.processDone
	<-run.stderrDone
}

func (a *Adapter) reserve(run *activeRun) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing.Load() {
		return ErrRuntimeClosed
	}
	if _, exists := a.active[run.id]; exists {
		return ErrRunExists
	}
	a.active[run.id] = run
	return nil
}

func (a *Adapter) failStartup(run *activeRun, collector *startupEventCollector, err error) pilotruntime.RunResult {
	if collector != nil {
		_, _ = collector.stop()
	}
	result := pilotruntime.RunResult{Usage: a.bestEffortUsage(run)}
	run.cancel()
	run.stream.finish(result, err)
	a.shutdown(run)
	a.release(run.id, run)
	close(run.cleanupDone)
	return result
}

func collectStartupEvents(client *RPCClient, limit int) *startupEventCollector {
	collector := &startupEventCollector{
		stopCh:  make(chan struct{}),
		done:    make(chan struct{}),
		settled: make(chan struct{}),
		events:  make([]WireEvent, 0, min(limit, 32)),
	}
	go func() {
		defer close(collector.done)
		settled := false
		for {
			select {
			case <-collector.stopCh:
				return
			default:
			}
			select {
			case <-collector.stopCh:
				return
			case event, ok := <-client.Events():
				if !ok {
					collector.err = client.Wait()
					if collector.err == nil {
						collector.err = io.EOF
					}
					return
				}
				if event.Type == "agent_settled" && !settled {
					settled = true
					close(collector.settled)
				}
				if len(collector.events) < limit {
					collector.events = append(collector.events, event)
				} else if collector.err == nil {
					collector.err = ErrEventBackpressure
				}
			}
		}
	}()
	return collector
}

func (c *startupEventCollector) stop() ([]WireEvent, error) {
	c.stopOnce.Do(func() { close(c.stopCh) })
	<-c.done
	return c.events, c.err
}

func (a *Adapter) abortUncertainStartup(run *activeRun, collector *startupEventCollector) error {
	ctx, cancel := context.WithTimeout(context.Background(), a.config.CommandTimeout)
	abortErr := run.client.Abort(ctx)
	cancel()

	timer := time.NewTimer(a.config.AbortGrace)
	defer timer.Stop()
	select {
	case <-collector.settled:
		run.settledObserved.Store(true)
	case <-run.processDone:
	case <-timer.C:
	}
	if abortErr != nil && !errors.Is(abortErr, io.EOF) {
		return fmt.Errorf("abort prompt with unknown outcome: %w", abortErr)
	}
	return nil
}

func (a *Adapter) release(runID string, expected *activeRun) {
	a.mu.Lock()
	if a.active[runID] == expected {
		delete(a.active, runID)
	}
	a.mu.Unlock()
}

func (r *activeRun) waitProcess() {
	err := r.process.Wait()
	r.processMu.Lock()
	r.processErr = err
	r.processMu.Unlock()
	close(r.processDone)
}

func (r *activeRun) getProcessErr() error {
	r.processMu.Lock()
	defer r.processMu.Unlock()
	return r.processErr
}

func verifyState(state State, req pilotruntime.RunRequest) error {
	if state.Model == nil {
		return fmt.Errorf("pi get_state returned no model")
	}
	if state.Model.Provider != req.Profile.Provider || state.Model.ID != req.Profile.Model {
		return fmt.Errorf("pi model mismatch: got %s/%s, want %s/%s",
			state.Model.Provider, state.Model.ID, req.Profile.Provider, req.Profile.Model)
	}
	if state.IsStreaming || state.IsCompacting {
		return fmt.Errorf("pi process is not idle during handshake")
	}
	if state.SessionID == "" || state.SessionFile == "" {
		return fmt.Errorf("pi get_state returned no persistent session")
	}
	stateFile, err := secureSessionFile(req.Session.Directory, state.SessionFile)
	if err != nil {
		return fmt.Errorf("pi session file escaped staging directory")
	}
	if req.Session.FilePath != "" {
		expectedFile, expectedErr := secureSessionFile(req.Session.Directory, req.Session.FilePath)
		if expectedErr != nil || stateFile != expectedFile {
			return fmt.Errorf("pi loaded a different staging session file")
		}
	}
	return nil
}

func verifySettledSession(run *activeRun, stats SessionStats) error {
	if stats.SessionID == "" || stats.SessionFile == "" {
		return fmt.Errorf("pi session stats omitted persistent session")
	}
	if stats.SessionID != run.state.SessionID {
		return fmt.Errorf("pi session changed during run")
	}
	statsFile, err := secureSessionFile(run.request.Session.Directory, stats.SessionFile)
	if err != nil {
		return fmt.Errorf("pi settled session escaped staging directory")
	}
	stateFile, err := secureSessionFile(run.request.Session.Directory, run.state.SessionFile)
	if err != nil || statsFile != stateFile {
		return fmt.Errorf("pi session changed during run")
	}
	return nil
}

func verifyUsageSnapshot(run *activeRun, stats SessionStats) error {
	if err := verifySettledSession(run, stats); err != nil {
		return err
	}
	if stats.Tokens.Input > int64Max-stats.Tokens.Output ||
		stats.Tokens.Input+stats.Tokens.Output > int64Max-stats.Tokens.CacheRead ||
		stats.Tokens.Input+stats.Tokens.Output+stats.Tokens.CacheRead > int64Max-stats.Tokens.CacheWrite {
		return fmt.Errorf("pi session stats token total overflow")
	}
	wantTotal := stats.Tokens.Input + stats.Tokens.Output + stats.Tokens.CacheRead + stats.Tokens.CacheWrite
	if stats.Tokens.Total != wantTotal {
		return fmt.Errorf("pi session stats token total is inconsistent")
	}
	return nil
}

func usageDelta(run *activeRun, final SessionStats) (*pilotruntime.Usage, error) {
	if err := verifyUsageSnapshot(run, final); err != nil {
		return nil, err
	}
	baseline := run.baseline
	if final.SessionID != baseline.SessionID || final.SessionFile != baseline.SessionFile {
		return nil, fmt.Errorf("pi session identity changed between usage snapshots")
	}
	if baseline.costExact == nil || final.costExact == nil {
		return nil, fmt.Errorf("pi session usage cost is missing")
	}
	if final.Tokens.Input < baseline.Tokens.Input || final.Tokens.Output < baseline.Tokens.Output ||
		final.Tokens.CacheRead < baseline.Tokens.CacheRead || final.Tokens.CacheWrite < baseline.Tokens.CacheWrite ||
		final.Tokens.Total < baseline.Tokens.Total || final.costExact.Cmp(baseline.costExact) < 0 {
		return nil, fmt.Errorf("pi session usage counters decreased during run")
	}
	costDelta := new(big.Rat).Sub(final.costExact, baseline.costExact)
	scaledMicros := new(big.Rat).Mul(costDelta, new(big.Rat).SetInt64(1_000_000))
	costMicros, remainder := new(big.Int), new(big.Int)
	costMicros.QuoRem(scaledMicros.Num(), scaledMicros.Denom(), remainder)
	if remainder.Sign() > 0 {
		costMicros.Add(costMicros, big.NewInt(1))
	}
	if !costMicros.IsInt64() {
		return nil, fmt.Errorf("pi session usage cost delta exceeds supported range")
	}
	cost, _ := costDelta.Float64()
	usage := &pilotruntime.Usage{
		State:            pilotruntime.UsageStateExact,
		InputTokens:      final.Tokens.Input - baseline.Tokens.Input,
		OutputTokens:     final.Tokens.Output - baseline.Tokens.Output,
		CacheReadTokens:  final.Tokens.CacheRead - baseline.Tokens.CacheRead,
		CacheWriteTokens: final.Tokens.CacheWrite - baseline.Tokens.CacheWrite,
		TotalTokens:      final.Tokens.Total - baseline.Tokens.Total,
		CostMicros:       costMicros.Int64(),
		Cost:             cost,
	}
	if usage.TotalTokens != usage.InputTokens+usage.OutputTokens+usage.CacheReadTokens+usage.CacheWriteTokens {
		return nil, fmt.Errorf("pi session usage delta total is inconsistent")
	}
	return usage, nil
}

func (a *Adapter) bestEffortUsage(run *activeRun) *pilotruntime.Usage {
	if !run.promptAttempted.Load() {
		return &pilotruntime.Usage{State: pilotruntime.UsageStateNotStarted}
	}
	if !run.settledObserved.Load() || run.client == nil || !processAlive(run) {
		return unknownUsage()
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.config.CommandTimeout)
	defer cancel()
	stats, err := run.client.GetSessionStats(ctx)
	if err != nil {
		return unknownUsage()
	}
	usage, err := usageDelta(run, stats)
	if err != nil {
		return unknownUsage()
	}
	return usage
}

func processAlive(run *activeRun) bool {
	if run.process == nil {
		return false
	}
	select {
	case <-run.processDone:
		return false
	default:
		return true
	}
}

func unknownUsage() *pilotruntime.Usage {
	return &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}
}

func notStartedError(err error) error {
	return pilotruntime.NewRunError(err, &pilotruntime.Usage{State: pilotruntime.UsageStateNotStarted})
}

const int64Max = int64(^uint64(0) >> 1)

func waitDone(done <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

type boundedCapture struct {
	mu         sync.Mutex
	buf        bytes.Buffer
	limit      int
	total      int64
	truncated  bool
	exceeded   chan struct{}
	exceedOnce sync.Once
}

func newBoundedCapture(limit int) *boundedCapture {
	return &boundedCapture{limit: limit, exceeded: make(chan struct{})}
}

func (c *boundedCapture) Write(data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total += int64(len(data))
	remaining := c.limit - c.buf.Len()
	if remaining > 0 {
		keep := min(len(data), remaining)
		_, _ = c.buf.Write(data[:keep])
	}
	if c.total > int64(c.limit) {
		c.truncated = true
		c.exceedOnce.Do(func() { close(c.exceeded) })
	}
	return len(data), nil
}

func (c *boundedCapture) snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf.Bytes()...)
}

func (c *boundedCapture) stats() (total int64, truncated bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total, c.truncated
}

func drainStderr(stderr io.Reader, capture *boundedCapture) {
	_, _ = io.Copy(capture, stderr)
}

var _ pilotruntime.AgentRuntime = (*Adapter)(nil)
