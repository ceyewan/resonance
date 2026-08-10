package coordinator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ceyewan/genesis/clog"

	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/pilot/identity"
	pilotobservability "github.com/ceyewan/resonance/pilot/observability"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
	"github.com/ceyewan/resonance/pilot/session"
	sharediam "github.com/ceyewan/resonance/pkg/iam"
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
		dependencies.Profiles == nil || dependencies.Principals == nil || dependencies.Capabilities == nil ||
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
	unknown, err := c.deps.Runs.RecoverExpiredAgentBudgetAttempts(ctx, c.config.TenantID, now)
	if err != nil {
		return repo.AgentRunRecoveryResult{}, fmt.Errorf("recover expired agent budget attempts: %w", err)
	}
	recovered, err := c.deps.Runs.RecoverExpiredAgentRuns(ctx, c.config.TenantID, now)
	if err != nil {
		return repo.AgentRunRecoveryResult{}, err
	}
	recovered.UnknownBudgetAttempts = unknown
	return recovered, nil
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
	profile, err := c.deps.Profiles.ResolveProfile(runContext, current.TenantID, current.ProfileID, current.ProfileVersion)
	if err != nil {
		return fail("profile_unavailable", "agent profile could not be resolved", true, err)
	}
	if profile.ID != current.ProfileID || profile.Version != current.ProfileVersion ||
		profile.Provider != current.ModelProvider || profile.Model != current.ModelID || profile.SystemPrompt == "" {
		return fail("profile_mismatch", "resolved profile did not match the queued snapshot", false, ErrProfileMismatch)
	}
	binding, err := c.deps.Runs.GetAgentSessionBinding(runContext, current.TenantID, current.ConversationID)
	if errors.Is(err, repo.ErrAgentSessionBindingNotFound) {
		binding = nil
	} else if err != nil {
		return fail("session_binding_unavailable", "session binding could not be loaded", true, err)
	}
	principal, err := c.authorizeRun(runContext, current)
	if err != nil {
		if isProfileAccessDenied(err) {
			return c.cancelUnauthorized(guard, err)
		}
		return fail("principal_unavailable", "actor principal could not be resolved", true, err)
	}
	capability, err := c.deps.Capabilities.IssueCapability(runContext, current, principal)
	if err != nil {
		return fail("capability_denied", "run capability could not be issued", false, err)
	}
	if capability.IsZero() {
		return fail("capability_denied", "run capability issuer returned an empty token", false, fmt.Errorf("empty capability"))
	}

	prompt := current.Prompt
	needsHistory := binding == nil && current.SourceSeqID > 1
	var staging session.Staging
	if !needsHistory {
		staging, err = c.deps.Sessions.Start(runContext, current, binding)
		if errors.Is(err, session.ErrBindingNeedsRebuild) {
			needsHistory = true
		}
	}
	if needsHistory {
		prompt, err = c.deps.History.RebuildPrompt(runContext, current)
		if err != nil {
			return fail("history_rebuild_failed", "authoritative conversation history could not be rebuilt", true, err)
		}
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
	var budgetAttempt *model.AgentBudgetAttempt
	_, err = guard.apply(func(run *model.AgentRun, now time.Time) (*model.AgentRun, error) {
		reserved, reserveErr := c.deps.Runs.ReserveAgentBudget(runContext, repo.AgentBudgetReservation{
			Lease: guard.lease(run, now), Attempt: run.Attempt,
			ProfileID: run.ProfileID, ProfileVersion: run.ProfileVersion,
		})
		budgetAttempt = reserved
		return run, reserveErr
	})
	if err != nil {
		retryable := !errors.Is(err, repo.ErrAgentBudgetExceeded) &&
			!errors.Is(err, repo.ErrAgentBudgetPolicyNotFound) && !errors.Is(err, repo.ErrAgentBudgetPolicyDisabled)
		return fail("budget_reservation_failed", "tenant agent budget could not be reserved", retryable, err)
	}
	if budgetAttempt == nil || budgetAttempt.ReservedTokens < 1 || budgetAttempt.ReservedCostMicros < 1 {
		return fail("budget_reservation_invalid", "tenant agent budget reservation was incomplete", false, repo.ErrAgentBudgetAttemptConflict)
	}

	settleBudget := func(usage *pilotruntime.Usage) error {
		usage = normalizedBudgetUsage(usage)
		_, settleErr := guard.apply(func(run *model.AgentRun, now time.Time) (*model.AgentRun, error) {
			_, budgetErr := c.deps.Runs.SettleAgentBudget(context.Background(), repo.AgentBudgetSettlement{
				Lease: guard.lease(run, now), Attempt: run.Attempt,
				Usage: repo.AgentBudgetUsage{
					State: string(usage.State), InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
					CacheReadTokens: usage.CacheReadTokens, CacheWriteTokens: usage.CacheWriteTokens,
					TotalTokens: usage.TotalTokens, CostMicros: usage.CostMicros,
				},
			})
			return run, budgetErr
		})
		return settleErr
	}

	stream, err := c.deps.Runtime.Run(runContext, pilotruntime.RunRequest{
		RunID: starting.RunID, ConversationID: starting.ConversationID, Prompt: prompt,
		Session: staging.Snapshot, Profile: profile, Actor: principal, Capability: capability,
		Limits: pilotruntime.ExecutionLimits{
			MaxTotalTokens: budgetAttempt.ReservedTokens, MaxCostMicros: budgetAttempt.ReservedCostMicros,
			MaxProviderCalls: c.config.MaxProviderCalls,
		},
	})
	if err != nil {
		if settleErr := settleBudget(runtimeStartErrorUsage(err)); settleErr != nil {
			err = errors.Join(err, fmt.Errorf("settle failed runtime start budget: %w", settleErr))
		}
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
		if settleErr := settleBudget(nil); settleErr != nil {
			err = errors.Join(err, fmt.Errorf("hold runtime budget after transition failure: %w", settleErr))
		}
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
	if settleErr := settleBudget(result.Usage); settleErr != nil {
		runtimeErr = errors.Join(runtimeErr, fmt.Errorf("settle agent budget attempt: %w", settleErr))
	}
	if runtimeErr != nil {
		if lostErr := guard.lostError(); lostErr != nil {
			runtimeErr = errors.Join(runtimeErr, lostErr)
		}
		return fail("runtime_failed", "agent runtime did not produce a settled result", true, runtimeErr)
	}
	_, err = c.authorizeRun(runContext, guard.snapshot())
	if err != nil {
		if isProfileAccessDenied(err) {
			return c.cancelUnauthorized(guard, err)
		}
		return fail("principal_recheck_unavailable", "actor principal could not be rechecked", true, err)
	}
	candidate, err := c.deps.Sessions.PrepareCandidate(runContext, staging, result)
	if err != nil {
		return fail("session_prepare_failed", "runtime session candidate could not be prepared", true, err)
	}
	usage := normalizedBudgetUsage(result.Usage)
	prepared, err := guard.apply(func(run *model.AgentRun, now time.Time) (*model.AgentRun, error) {
		return c.deps.Runs.PrepareAgentRun(runContext, repo.AgentRunPreparedResult{
			Lease: guard.lease(run, now), BaseSessionGeneration: staging.BaseGeneration,
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

func (c *Coordinator) authorizeRun(
	ctx context.Context,
	run *model.AgentRun,
) (pilotruntime.ActorPrincipal, error) {
	principal, err := c.deps.Principals.ResolvePrincipal(ctx, run.TenantID, run.ActorID)
	if err != nil {
		if errors.Is(err, identity.ErrPrincipalUnauthorized) {
			return pilotruntime.ActorPrincipal{}, fmt.Errorf("%w: %v", ErrProfileAccessDenied, err)
		}
		return pilotruntime.ActorPrincipal{}, err
	}
	if principal.TenantID != run.TenantID || principal.ActorID != run.ActorID || principal.Username != run.ActorUsername {
		return pilotruntime.ActorPrincipal{}, fmt.Errorf("%w: %v", ErrProfileAccessDenied, ErrPrincipalMismatch)
	}
	if err := sharediam.AuthorizeAgentProfile(run.ProfileID, principal.Roles, principal.Scopes); err != nil {
		return pilotruntime.ActorPrincipal{}, fmt.Errorf("%w: %v", ErrProfileAccessDenied, err)
	}
	return principal, nil
}

func isProfileAccessDenied(err error) bool {
	return errors.Is(err, ErrProfileAccessDenied) || errors.Is(err, identity.ErrPrincipalUnauthorized) ||
		errors.Is(err, sharediam.ErrAgentProfileUnauthorized)
}

func (c *Coordinator) cancelUnauthorized(
	guard *leaseGuard,
	cause error,
) (*model.AgentRun, error) {
	guard.stop()
	current := guard.snapshot()
	now := c.now().UTC()
	var cleanupErrors []error

	binding, err := c.deps.Runs.GetAgentSessionBinding(context.Background(), current.TenantID, current.ConversationID)
	if err == nil && binding.Status == model.AgentSessionBindingStatusActive {
		_, err = c.deps.Runs.MarkAgentSessionBindingDirty(
			context.Background(), current.TenantID, current.ConversationID, binding.Generation, now,
		)
	}
	if err != nil && !errors.Is(err, repo.ErrAgentSessionBindingNotFound) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("invalidate privileged session binding: %w", err))
	}

	_, err = c.deps.Runs.CancelPendingAgentRuns(context.Background(), repo.AgentPendingRunCancellation{
		TenantID: current.TenantID, ActorID: current.ActorID,
		ProfileID: current.ProfileID, ProfileVersion: current.ProfileVersion,
		ErrorCode: "principal_revoked", ErrorSummary: "agent profile access is no longer authorized", Now: now,
	})
	if err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("cancel queued profile runs: %w", err))
	}

	cancelled, err := c.deps.Runs.CancelAgentRun(context.Background(), repo.AgentRunCancellation{
		Lease: repo.AgentRunLease{
			TenantID: current.TenantID, RunID: current.RunID,
			WorkerID: current.LeaseOwner, LeaseToken: current.LeaseToken,
			ExpectedVersion: current.Version, Now: now,
		},
		ErrorCode: "principal_revoked", ErrorSummary: "agent profile access is no longer authorized",
	})
	if err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("cancel active profile run: %w", err))
	}
	combined := errors.Join(append([]error{cause}, cleanupErrors...)...)
	if cancelled == nil {
		return nil, combined
	}
	return cancelled, combined
}

func normalizedBudgetUsage(usage *pilotruntime.Usage) *pilotruntime.Usage {
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

func runtimeStartErrorUsage(err error) *pilotruntime.Usage {
	var runErr *pilotruntime.RunError
	if errors.As(err, &runErr) && runErr.Usage != nil {
		return normalizedBudgetUsage(runErr.Usage)
	}
	return &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}
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
		if _, err := c.authorizeRun(ctx, current); err != nil {
			if isProfileAccessDenied(err) {
				return c.cancelUnauthorized(guard, err)
			}
			return current, fmt.Errorf("recheck actor authorization before final commit: %w", err)
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

func (c *Coordinator) newClaim() (repo.AgentRunClaim, error) {
	token, err := c.token()
	if err != nil {
		return repo.AgentRunClaim{}, fmt.Errorf("generate lease token: %w", err)
	}
	return repo.AgentRunClaim{
		TenantID: c.config.TenantID, ProfileID: c.config.ProfileID, ProfileVersion: c.config.ProfileVersion,
		WorkerID: c.config.WorkerID, LeaseToken: token,
		Now: c.now().UTC(), LeaseDuration: c.config.LeaseDuration,
	}, nil
}

func validateConfig(config Config) error {
	if config.TenantID == "" || config.ProfileID == "" || config.ProfileVersion < 1 || config.WorkerID == "" ||
		config.MaxProviderCalls < 1 || config.MaxProviderCalls > 128 {
		return fmt.Errorf("coordinator tenant, profile snapshot and worker_id are required")
	}
	if config.LeaseDuration <= 0 || config.HeartbeatInterval <= 0 || config.RunTimeout <= 0 || config.RetryBackoff < 0 {
		return fmt.Errorf("coordinator durations are invalid")
	}
	if config.LeaseDuration < 3*config.HeartbeatInterval {
		return fmt.Errorf("lease duration must be at least three heartbeat intervals")
	}
	return nil
}
