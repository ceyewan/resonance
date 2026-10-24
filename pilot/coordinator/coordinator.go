package coordinator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ceyewan/genesis/clog"

	"github.com/ceyewan/resonance/model"
	pilotobservability "github.com/ceyewan/resonance/pilot/observability"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
	"github.com/ceyewan/resonance/pilot/session"
	"github.com/ceyewan/resonance/repo"
)

type Coordinator struct {
	config Config
	deps   Dependencies
	now    func() time.Time
	token  func() (string, error)
}

func New(config Config, dependencies Dependencies, options ...Option) (*Coordinator, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if dependencies.Runs == nil || dependencies.Runtime == nil || dependencies.Sessions == nil ||
		dependencies.History == nil || dependencies.FinalMessages == nil {
		return nil, fmt.Errorf("coordinator dependencies are incomplete")
	}
	coordinator := &Coordinator{
		config: config,
		deps:   dependencies,
		now:    time.Now,
		token:  randomLeaseToken,
	}
	for _, option := range options {
		option(coordinator)
	}
	return coordinator, nil
}

type Option func(*Coordinator)

func WithClock(now func() time.Time) Option {
	return func(coordinator *Coordinator) {
		if now != nil {
			coordinator.now = now
		}
	}
}

func WithLeaseTokenSource(source func() (string, error)) Option {
	return func(coordinator *Coordinator) {
		if source != nil {
			coordinator.token = source
		}
	}
}

// ProcessOne 优先恢复已准备结果，再领取需要模型推理的新 Run。
func (c *Coordinator) ProcessOne(ctx context.Context) (*model.AgentRun, error) {
	claim, err := c.newClaim()
	if err != nil {
		return nil, err
	}
	prepared, err := c.deps.Runs.ClaimPreparedAgentRun(ctx, claim)
	if err == nil {
		return c.commitPrepared(ctx, prepared)
	}
	if !errors.Is(err, repo.ErrNoAgentRunAvailable) {
		return nil, fmt.Errorf("claim prepared agent run: %w", err)
	}

	claim, err = c.newClaim()
	if err != nil {
		return nil, err
	}
	run, err := c.deps.Runs.ClaimNextAgentRun(ctx, claim)
	if errors.Is(err, repo.ErrNoAgentRunAvailable) {
		return nil, ErrNoWork
	}
	if err != nil {
		return nil, fmt.Errorf("claim next agent run: %w", err)
	}
	return c.execute(ctx, run)
}

func (c *Coordinator) Recover(ctx context.Context) (repo.AgentRunRecoveryResult, error) {
	now := c.now().UTC()
	return c.deps.Runs.RecoverExpiredAgentRuns(ctx, "", now)
}

func (c *Coordinator) execute(parent context.Context, claimed *model.AgentRun) (*model.AgentRun, error) {
	runContext, cancelRun := context.WithTimeout(parent, c.config.RunTimeout)
	defer cancelRun()
	runContext = pilotobservability.ExtractPersistedTraceContext(runContext, claimed.TraceContext)
	runContext, finishSpan := pilotobservability.StartSpan(runContext, "agent.run")
	defer finishSpan()
	if c.deps.Logger != nil {
		c.deps.Logger.InfoContext(runContext, "agent run started",
			clog.String("tenant_id", claimed.TenantID),
			clog.String("run_id", claimed.RunID),
			clog.String("conversation_id", claimed.ConversationID),
			clog.String("profile_id", claimed.ProfileID),
			clog.Int("attempt", claimed.Attempt),
		)
	}
	guard := newLeaseGuard(c.deps.Runs, claimed, c.config.LeaseDuration, c.config.HeartbeatInterval, c.now)
	guard.start(cancelRun)
	defer guard.stop()

	fail := func(code, summary string, retryable bool, cause error) (*model.AgentRun, error) {
		guard.stop()
		current := guard.snapshot()
		now := c.now().UTC()
		failed, failErr := c.deps.Runs.FailAgentRun(context.Background(), repo.AgentRunFailure{
			Lease: repo.AgentRunLease{
				TenantID: current.TenantID, RunID: current.RunID,
				WorkerID: current.LeaseOwner, LeaseToken: current.LeaseToken,
				ExpectedVersion: current.Version, Now: now,
			},
			ErrorCode: code, ErrorSummary: summary, Retryable: retryable,
			RetryAt: now.Add(c.config.RetryBackoff),
		})
		if failErr != nil {
			return nil, errors.Join(cause, fmt.Errorf("persist agent run failure: %w", failErr))
		}
		return failed, cause
	}

	current := guard.snapshot()
	binding, err := c.deps.Runs.GetAgentSessionBinding(runContext, current.TenantID, current.ConversationID)
	if errors.Is(err, repo.ErrAgentSessionBindingNotFound) {
		binding = nil
	} else if err != nil {
		return fail("session_binding_unavailable", "session binding could not be loaded", true, err)
	}
	// Validate the source through Logic on every turn, even when resuming Pi.
	// This closes the MQ-lag window where a source was edited/recalled before enqueue.
	authoritativePrompt, err := c.deps.History.RebuildPrompt(runContext, current)
	if err != nil {
		return fail("history_unavailable", "source message could not be verified", true, err)
	}
	prompt := current.Prompt
	baseBindingVersion := int64(0)
	if binding != nil {
		baseBindingVersion = binding.Version
	}
	needsHistory := binding == nil
	var staging session.Staging
	if !needsHistory {
		staging, err = c.deps.Sessions.Start(runContext, current, binding)
		if errors.Is(err, session.ErrBindingNeedsRebuild) {
			needsHistory = true
		}
	}
	if needsHistory {
		prompt = authoritativePrompt
		staging, err = c.deps.Sessions.Start(runContext, current, nil)
		if err == nil && binding != nil {
			// Replace the dirty/incompatible binding by CAS rather than pretending
			// this is a new conversation.
			staging.BaseGeneration = binding.Generation
		}
	}
	if err != nil {
		retryable := !errors.Is(err, session.ErrBindingRevoked) && !errors.Is(err, session.ErrBindingNeedsRebuild)
		return fail("session_start_failed", "staging session could not be prepared", retryable, err)
	}
	defer func() { _ = c.deps.Sessions.Discard(context.Background(), staging) }()

	starting, err := guard.apply(func(run *model.AgentRun, now time.Time) (*model.AgentRun, error) {
		return c.deps.Runs.AdvanceAgentRun(runContext, repo.AgentRunTransition{
			Lease: guard.lease(run, now), From: model.AgentRunStatusClaimed, To: model.AgentRunStatusStartingRuntime,
		})
	})
	if err != nil {
		return fail("start_transition_failed", "run could not enter runtime startup", true, err)
	}
	stream, err := c.deps.Runtime.Run(runContext, pilotruntime.RunRequest{
		RunID: starting.RunID, ConversationID: starting.ConversationID, Prompt: prompt,
		Session: staging.Snapshot, Profile: c.config.Assistant,
		MaxOutputTokens: c.config.MaxOutputTokens,
	})
	if err != nil {
		return fail("runtime_start_failed", "agent runtime could not start", true, err)
	}
	_, err = guard.apply(func(run *model.AgentRun, now time.Time) (*model.AgentRun, error) {
		return c.deps.Runs.AdvanceAgentRun(runContext, repo.AgentRunTransition{
			Lease: guard.lease(run, now), From: model.AgentRunStatusStartingRuntime, To: model.AgentRunStatusRunning,
		})
	})
	if err != nil {
		cancelRun()
		_ = c.deps.Runtime.Abort(context.Background(), starting.RunID)
		return fail("running_transition_failed", "run could not enter runtime execution", true, err)
	}

	drainContext, stopDrain := context.WithCancel(runContext)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			select {
			case <-drainContext.Done():
				return
			case event, ok := <-stream.Events():
				if !ok {
					return
				}
				if c.deps.Events != nil {
					_ = c.deps.Events.PublishRuntimeEvent(drainContext, guard.snapshot(), event)
				}
			}
		}
	}()
	result, runtimeErr := stream.Wait()
	stopDrain()
	<-drained
	if runtimeErr != nil {
		if lostErr := guard.lostError(); lostErr != nil {
			runtimeErr = errors.Join(runtimeErr, lostErr)
		}
		return fail("runtime_failed", "agent runtime did not produce a settled result", true, runtimeErr)
	}
	candidate, err := c.deps.Sessions.PrepareCandidate(runContext, staging, result)
	if err != nil {
		return fail("session_prepare_failed", "runtime session candidate could not be prepared", true, err)
	}
	usage := normalizedUsage(result.Usage)
	prepared, err := guard.apply(func(run *model.AgentRun, now time.Time) (*model.AgentRun, error) {
		return c.deps.Runs.PrepareAgentRun(runContext, repo.AgentRunPreparedResult{
			Lease: guard.lease(run, now), BaseSessionGeneration: staging.BaseGeneration,
			BaseBindingVersion: baseBindingVersion,
			CandidateSessionID: candidate.SessionID, CandidateSessionRef: candidate.SessionRef,
			CandidateChecksum: candidate.Checksum, CandidateLeafEntryID: candidate.LeafEntryID,
			CandidateSessionBytes: candidate.ByteSize, CandidateEntryCount: candidate.EntryCount,
			FrozenFinalText: result.FinalText, FinalClientMsgID: "agent:" + run.RunID + ":final",
			InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
			CacheReadTokens: usage.CacheReadTokens, CacheWriteTokens: usage.CacheWriteTokens,
			TotalTokens: usage.TotalTokens, UsageState: string(usage.State),
			CostMicros: usage.CostMicros, Cost: usage.Cost,
		})
	})
	if err != nil {
		return fail("result_prepare_failed", "settled result could not be frozen", true, err)
	}

	committing, err := guard.apply(func(run *model.AgentRun, now time.Time) (*model.AgentRun, error) {
		return c.deps.Runs.BeginAgentRunCommit(runContext, guard.lease(run, now))
	})
	if err != nil {
		return prepared, fmt.Errorf("begin prepared result commit: %w", err)
	}
	return c.commitWithGuard(runContext, guard, committing)
}

func normalizedUsage(usage *pilotruntime.Usage) *pilotruntime.Usage {
	if usage == nil {
		return &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}
	}
	switch usage.State {
	case pilotruntime.UsageStateExact, pilotruntime.UsageStateNotStarted, pilotruntime.UsageStateUnknown:
		return usage
	default:
		return &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}
	}
}

func (c *Coordinator) commitPrepared(parent context.Context, claimed *model.AgentRun) (*model.AgentRun, error) {
	commitContext, cancel := context.WithTimeout(parent, c.config.RunTimeout)
	defer cancel()
	commitContext = pilotobservability.ExtractPersistedTraceContext(commitContext, claimed.TraceContext)
	commitContext, finishSpan := pilotobservability.StartSpan(commitContext, "agent.commit_prepared")
	defer finishSpan()
	guard := newLeaseGuard(c.deps.Runs, claimed, c.config.LeaseDuration, c.config.HeartbeatInterval, c.now)
	guard.start(cancel)
	defer guard.stop()
	return c.commitWithGuard(commitContext, guard, claimed)
}

func (c *Coordinator) commitWithGuard(ctx context.Context, guard *leaseGuard, run *model.AgentRun) (*model.AgentRun, error) {
	current := run
	if current.FinalEventID == 0 {
		// 历史可能在本轮执行期间被撤回/编辑：必须在写入 Logic 之前复查，
		// 消息一旦落库就收不回来了。
		if err := c.ensureStillCommittable(ctx, guard); err != nil {
			return current, err
		}
		ack, err := c.deps.FinalMessages.CommitFinalMessage(ctx, FinalMessageRequest{
			TenantID: current.TenantID, RunID: current.RunID, ConversationID: current.ConversationID,
			ClientMsgID: current.FinalClientMsgID, Content: current.FrozenFinalText,
		})
		if err != nil {
			return current, fmt.Errorf("commit frozen final message: %w", err)
		}
		_, err = guard.apply(func(run *model.AgentRun, now time.Time) (*model.AgentRun, error) {
			return c.deps.Runs.RecordAgentRunFinalMessage(ctx, repo.AgentRunFinalMessage{
				Lease: guard.lease(run, now), EventID: ack.EventID, SeqID: ack.SeqID, TimestampMs: ack.TimestampMs,
			})
		})
		if err != nil {
			return run, fmt.Errorf("record final message acknowledgement: %w", err)
		}
	}

	guard.stop()
	current = guard.snapshot()
	completed, err := c.deps.Runs.CompleteAgentRun(ctx, repo.AgentRunCompletion{
		Lease: repo.AgentRunLease{
			TenantID: current.TenantID, RunID: current.RunID,
			WorkerID: current.LeaseOwner, LeaseToken: current.LeaseToken,
			ExpectedVersion: current.Version, Now: c.now().UTC(),
		},
		CommittedGeneration: current.BaseSessionGeneration + 1,
	})
	if err != nil {
		return current, fmt.Errorf("commit session binding and complete run: %w", err)
	}
	return completed, nil
}

var errHistoryInvalidated = errors.New("authoritative history changed before final commit")

// ensureStillCommittable 是最终回复写入 Logic 之前的最后一道闸门。撤回/编辑事件由
// Pilot 自己在 ingress 侧处理，因此这里的复查完全不依赖 Logic 认识 Agent 表。
func (c *Coordinator) ensureStillCommittable(ctx context.Context, guard *leaseGuard) error {
	current := guard.snapshot()
	stored, err := c.deps.Runs.GetAgentRun(ctx, current.TenantID, current.RunID)
	if err != nil {
		return fmt.Errorf("reload agent run before final commit: %w", err)
	}
	if stored.Status != model.AgentRunStatusCommitting || stored.FinalEventID != 0 {
		return fmt.Errorf("%w: run is no longer committable", errHistoryInvalidated)
	}
	binding, err := c.deps.Runs.GetAgentSessionBinding(ctx, current.TenantID, current.ConversationID)
	if errors.Is(err, repo.ErrAgentSessionBindingNotFound) {
		if current.BaseSessionGeneration != 0 || current.BaseBindingVersion != 0 {
			return fmt.Errorf("%w: session binding disappeared", errHistoryInvalidated)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("reload session binding before final commit: %w", err)
	}
	if binding.Version != current.BaseBindingVersion {
		return fmt.Errorf("%w: session snapshot was invalidated during this run", errHistoryInvalidated)
	}
	return nil
}

func (c *Coordinator) newClaim() (repo.AgentRunClaim, error) {
	token, err := c.token()
	if err != nil {
		return repo.AgentRunClaim{}, fmt.Errorf("generate lease token: %w", err)
	}
	return repo.AgentRunClaim{
		ProfileID: c.config.ProfileID, ProfileVersion: c.config.ProfileVersion,
		WorkerID: c.config.WorkerID, LeaseToken: token,
		Now: c.now().UTC(), LeaseDuration: c.config.LeaseDuration,
	}, nil
}

func validateConfig(config Config) error {
	if config.ProfileID == "" || config.ProfileVersion < 1 || config.WorkerID == "" || config.MaxOutputTokens < 1 {
		return fmt.Errorf("coordinator profile snapshot, worker_id and max_output_tokens are required")
	}
	if config.Assistant.ID == "" || config.Assistant.Version < 1 || config.Assistant.Provider == "" ||
		config.Assistant.Model == "" || config.Assistant.SystemPrompt == "" {
		return fmt.Errorf("coordinator assistant snapshot is incomplete")
	}
	if config.LeaseDuration <= 0 || config.HeartbeatInterval <= 0 || config.RunTimeout <= 0 || config.RetryBackoff < 0 {
		return fmt.Errorf("coordinator durations are invalid")
	}
	if config.LeaseDuration < 3*config.HeartbeatInterval {
		return fmt.Errorf("lease duration must be at least three heartbeat intervals")
	}
	return nil
}
