package coordinator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/pilot/identity"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
	"github.com/ceyewan/resonance/pilot/session"
	"github.com/ceyewan/resonance/repo"
)

func TestCoordinator_ExecutesPrepareThenCommitEndToEnd(t *testing.T) {
	base := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
	clock := newTestClock(base)
	recorder := &callRecorder{}
	store := newMemoryRunStore(testCoordinatorRun(base), recorder)
	runtimeHarness := &fakeRuntime{recorder: recorder, result: testRuntimeResult()}
	sessions := &fakeSessionManager{recorder: recorder}
	writer := &fakeFinalWriter{recorder: recorder, ack: FinalMessageAck{EventID: 8001, SeqID: 88, TimestampMs: 1786152000000}}
	coordinator := newTestCoordinator(t, clock.Now, store, runtimeHarness, sessions, writer, recorder)

	completed, err := coordinator.ProcessOne(context.Background())
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusSucceeded, completed.Status)
	require.Equal(t, int64(8001), completed.FinalEventID)
	require.Equal(t, int64(1), completed.CommittedGeneration)
	require.Equal(t, int64(1), runtimeHarness.runCount.Load())
	require.Equal(t, pilotruntime.ExecutionLimits{MaxTotalTokens: 1_000, MaxCostMicros: 500_000, MaxProviderCalls: 8},
		runtimeHarness.requestSnapshot().Limits)
	require.Len(t, writer.requests, 1)
	require.Equal(t, "agent:run-coordinator:final", writer.requests[0].ClientMsgID)
	require.Equal(t, "settled final answer", writer.requests[0].Content)
	require.True(t, sessions.discarded.Load())

	requireOrder(t, recorder.snapshot(),
		"claim-prepared", "claim-execution", "profile", "binding", "principal", "capability",
		"session-start", "advance-STARTING_RUNTIME", "budget-reserve", "runtime-run", "advance-RUNNING", "budget-settle",
		"principal", "session-prepare", "prepare-result", "begin-commit", "principal", "final-message", "record-final", "complete",
	)
}

func TestCoordinator_BudgetDenialPreventsRuntimeStart(t *testing.T) {
	base := time.Date(2026, 8, 8, 8, 30, 0, 0, time.UTC)
	recorder := &callRecorder{}
	store := newMemoryRunStore(testCoordinatorRun(base), recorder)
	store.budgetReserveErr = repo.ErrAgentBudgetExceeded
	runtimeHarness := &fakeRuntime{recorder: recorder, result: testRuntimeResult()}
	coordinator := newTestCoordinator(t, func() time.Time { return base }, store, runtimeHarness,
		&fakeSessionManager{recorder: recorder}, &fakeFinalWriter{recorder: recorder}, recorder)

	failed, err := coordinator.ProcessOne(context.Background())
	require.ErrorIs(t, err, repo.ErrAgentBudgetExceeded)
	require.Equal(t, model.AgentRunStatusFailedFinal, failed.Status)
	require.Equal(t, "budget_reservation_failed", failed.LastErrorCode)
	require.Zero(t, runtimeHarness.runCount.Load())
	requireOrder(t, recorder.snapshot(), "advance-STARTING_RUNTIME", "budget-reserve", "fail")
}

func TestCoordinator_ProfileDowngradeCancelsRunQueueAndInvalidatesSession(t *testing.T) {
	base := time.Date(2026, 8, 9, 3, 0, 0, 0, time.UTC)
	recorder := &callRecorder{}
	run := testCoordinatorRun(base)
	run.ProfileID = model.AgentProfileIAMAdmin
	store := newMemoryRunStore(run, recorder)
	store.binding = activeTestBinding(run)
	runtimeHarness := &fakeRuntime{recorder: recorder, result: testRuntimeResult()}
	writer := &fakeFinalWriter{recorder: recorder}
	dependencies := testDependencies(store, runtimeHarness, &fakeSessionManager{recorder: recorder}, writer, recorder)
	dependencies.Profiles = fakeProfileResolver{recorder: recorder, profile: pilotruntime.ProfileSnapshot{
		ID: model.AgentProfileIAMAdmin, Version: 1, Provider: run.ModelProvider, Model: run.ModelID, SystemPrompt: "IAM administrator",
	}}
	dependencies.Principals = fakePrincipalResolver{recorder: recorder, principal: pilotruntime.ActorPrincipal{
		TenantID: run.TenantID, ActorID: run.ActorID, Username: run.ActorUsername,
		Roles: []string{model.SystemRoleUser}, Scopes: []string{model.ScopeChatUse},
	}}
	config := testCoordinatorConfig()
	config.ProfileID = model.AgentProfileIAMAdmin
	coordinator, err := New(config, dependencies, WithClock(func() time.Time { return base }), WithLeaseTokenSource(func() (string, error) { return "downgrade-token", nil }))
	require.NoError(t, err)

	cancelled, err := coordinator.ProcessOne(context.Background())
	require.ErrorIs(t, err, ErrProfileAccessDenied)
	require.Equal(t, model.AgentRunStatusCancelled, cancelled.Status)
	require.Equal(t, model.AgentSessionBindingStatusDirty, store.binding.Status)
	require.Zero(t, runtimeHarness.runCount.Load())
	require.Empty(t, writer.requests)
	requireOrder(t, recorder.snapshot(), "binding", "principal", "binding-dirty", "cancel-pending", "cancel-active")
}

func TestCoordinator_RevocationAfterRuntimeSettlesPreventsStreamCommitAndInvalidatesSession(t *testing.T) {
	base := time.Date(2026, 8, 9, 3, 30, 0, 0, time.UTC)
	recorder := &callRecorder{}
	run := testCoordinatorRun(base)
	run.ProfileID = model.AgentProfileIAMAdmin
	store := newMemoryRunStore(run, recorder)
	store.binding = activeTestBinding(run)
	runtimeHarness := &fakeRuntime{recorder: recorder, result: testRuntimeResult()}
	writer := &fakeFinalWriter{recorder: recorder}
	dependencies := testDependencies(store, runtimeHarness, &fakeSessionManager{recorder: recorder}, writer, recorder)
	dependencies.Profiles = fakeProfileResolver{recorder: recorder, profile: pilotruntime.ProfileSnapshot{
		ID: model.AgentProfileIAMAdmin, Version: 1, Provider: run.ModelProvider, Model: run.ModelID, SystemPrompt: "IAM administrator",
	}}
	dependencies.Principals = &revokingPrincipalResolver{recorder: recorder, allowedCalls: 1, principal: pilotruntime.ActorPrincipal{
		TenantID: run.TenantID, ActorID: run.ActorID, Username: run.ActorUsername,
		Roles: []string{model.SystemRoleIAMAdmin}, Scopes: []string{model.ScopeIAMUsersRead},
	}}
	config := testCoordinatorConfig()
	config.ProfileID = model.AgentProfileIAMAdmin
	coordinator, err := New(config, dependencies, WithClock(func() time.Time { return base }), WithLeaseTokenSource(func() (string, error) { return "settled-revoke-token", nil }))
	require.NoError(t, err)

	cancelled, err := coordinator.ProcessOne(context.Background())
	require.ErrorIs(t, err, ErrProfileAccessDenied)
	require.Equal(t, model.AgentRunStatusCancelled, cancelled.Status)
	require.Equal(t, model.AgentSessionBindingStatusDirty, store.binding.Status)
	require.Equal(t, int64(1), runtimeHarness.runCount.Load())
	require.Empty(t, writer.requests)
	require.Equal(t, model.AgentBudgetAttemptStatusSettled, store.budgetAttempt.Status)
	requireOrder(t, recorder.snapshot(), "runtime-run", "budget-settle", "principal", "binding-dirty", "cancel-pending", "cancel-active")
}

func TestCoordinator_PreparedResultCannotCommitAfterProfileRevocation(t *testing.T) {
	base := time.Date(2026, 8, 9, 4, 0, 0, 0, time.UTC)
	recorder := &callRecorder{}
	run := testCoordinatorRun(base)
	run.ProfileID = model.AgentProfileIAMAdmin
	run.Status = model.AgentRunStatusReadyToCommit
	run.CandidateSessionID = "candidate-session"
	run.CandidateSessionRef = "objects/candidate.jsonl"
	run.CandidateChecksum = testCoordinatorDigest
	run.CandidateLeafEntryID = "leaf-candidate"
	run.CandidateSessionBytes = 100
	run.CandidateEntryCount = 2
	run.FrozenFinalText = "privileged result"
	run.FinalClientMsgID = "agent:" + run.RunID + ":final"
	store := newMemoryRunStore(run, recorder)
	store.binding = activeTestBinding(run)
	runtimeHarness := &fakeRuntime{recorder: recorder, result: testRuntimeResult()}
	writer := &fakeFinalWriter{recorder: recorder}
	dependencies := testDependencies(store, runtimeHarness, &fakeSessionManager{recorder: recorder}, writer, recorder)
	dependencies.Principals = fakePrincipalResolver{recorder: recorder, err: identity.ErrPrincipalUnauthorized}
	config := testCoordinatorConfig()
	config.ProfileID = model.AgentProfileIAMAdmin
	coordinator, err := New(config, dependencies, WithClock(func() time.Time { return base }), WithLeaseTokenSource(func() (string, error) { return "prepared-revoke-token", nil }))
	require.NoError(t, err)

	cancelled, err := coordinator.ProcessOne(context.Background())
	require.ErrorIs(t, err, ErrProfileAccessDenied)
	require.Equal(t, model.AgentRunStatusCancelled, cancelled.Status)
	require.Equal(t, model.AgentSessionBindingStatusDirty, store.binding.Status)
	require.Zero(t, runtimeHarness.runCount.Load())
	require.Empty(t, writer.requests)
}

func TestCoordinator_FinalMessageFactCompletesAfterLaterProfileRevocation(t *testing.T) {
	base := time.Date(2026, 8, 9, 4, 15, 0, 0, time.UTC)
	recorder := &callRecorder{}
	run := testCoordinatorRun(base)
	run.ProfileID = model.AgentProfileIAMAdmin
	run.Status = model.AgentRunStatusCommitting
	run.BaseSessionGeneration = 7
	run.CandidateSessionID = "candidate-session"
	run.CandidateSessionRef = "objects/candidate.jsonl"
	run.CandidateChecksum = testCoordinatorDigest
	run.CandidateLeafEntryID = "leaf-candidate"
	run.CandidateSessionBytes = 100
	run.CandidateEntryCount = 2
	run.FrozenFinalText = "already committed privileged result"
	run.FinalClientMsgID = "agent:" + run.RunID + ":final"
	run.FinalEventID = 42
	run.FinalSeqID = 24
	run.FinalTimestampMs = base.UnixMilli()
	store := newMemoryRunStore(run, recorder)
	store.binding = activeTestBinding(run)
	runtimeHarness := &fakeRuntime{recorder: recorder, result: testRuntimeResult()}
	writer := &fakeFinalWriter{recorder: recorder}
	dependencies := testDependencies(store, runtimeHarness, &fakeSessionManager{recorder: recorder}, writer, recorder)
	dependencies.Principals = fakePrincipalResolver{recorder: recorder, err: identity.ErrPrincipalUnauthorized}
	config := testCoordinatorConfig()
	config.ProfileID = model.AgentProfileIAMAdmin
	coordinator, err := New(config, dependencies, WithClock(func() time.Time { return base }), WithLeaseTokenSource(func() (string, error) { return "committed-fact-token", nil }))
	require.NoError(t, err)

	completed, err := coordinator.ProcessOne(context.Background())
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusSucceeded, completed.Status)
	require.Equal(t, int64(42), completed.FinalEventID)
	require.Equal(t, int64(8), completed.CommittedGeneration)
	require.Zero(t, runtimeHarness.runCount.Load())
	require.Empty(t, writer.requests, "an acknowledged final message must never be published twice")
	require.NotContains(t, recorder.snapshot(), "principal", "authorization cannot roll back a durable user-visible fact")
}

func TestCoordinator_RuntimeStartUsageStateSettlesReservation(t *testing.T) {
	tests := []struct {
		name       string
		usage      *pilotruntime.Usage
		wantStatus string
	}{
		{name: "not started releases", usage: &pilotruntime.Usage{State: pilotruntime.UsageStateNotStarted}, wantStatus: model.AgentBudgetAttemptStatusReleased},
		{name: "unknown holds", usage: &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}, wantStatus: model.AgentBudgetAttemptStatusUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := time.Date(2026, 8, 8, 8, 45, 0, 0, time.UTC)
			recorder := &callRecorder{}
			store := newMemoryRunStore(testCoordinatorRun(base), recorder)
			runtimeHarness := &fakeRuntime{recorder: recorder, runErr: pilotruntime.NewRunError(errors.New("runtime start failed"), test.usage)}
			coordinator := newTestCoordinator(t, func() time.Time { return base }, store, runtimeHarness,
				&fakeSessionManager{recorder: recorder}, &fakeFinalWriter{recorder: recorder}, recorder)

			_, err := coordinator.ProcessOne(context.Background())
			require.Error(t, err)
			require.NotNil(t, store.budgetAttempt)
			require.Equal(t, test.wantStatus, store.budgetAttempt.Status)
			requireOrder(t, recorder.snapshot(), "budget-reserve", "runtime-run", "budget-settle", "fail")
		})
	}
}

func TestCoordinator_PostPromptFailureKeepsUnknownReservation(t *testing.T) {
	base := time.Date(2026, 8, 8, 8, 50, 0, 0, time.UTC)
	recorder := &callRecorder{}
	store := newMemoryRunStore(testCoordinatorRun(base), recorder)
	runtimeHarness := &fakeRuntime{
		recorder: recorder,
		result: pilotruntime.RunResult{
			Usage: &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown},
		},
		waitErr: errors.New("runtime exited after prompt"),
	}
	coordinator := newTestCoordinator(t, func() time.Time { return base }, store, runtimeHarness,
		&fakeSessionManager{recorder: recorder}, &fakeFinalWriter{recorder: recorder}, recorder)

	_, err := coordinator.ProcessOne(context.Background())
	require.ErrorContains(t, err, "runtime exited after prompt")
	require.NotNil(t, store.budgetAttempt)
	require.Equal(t, model.AgentBudgetAttemptStatusUnknown, store.budgetAttempt.Status)
	require.Equal(t, string(pilotruntime.UsageStateUnknown), store.budgetAttempt.UsageState)
	requireOrder(t, recorder.snapshot(), "budget-reserve", "runtime-run", "advance-RUNNING", "budget-settle", "fail")
}

func TestCoordinator_FinalMessageFailureReconcilesFrozenResultWithoutRerunningModel(t *testing.T) {
	base := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	clock := newTestClock(base)
	recorder := &callRecorder{}
	store := newMemoryRunStore(testCoordinatorRun(base), recorder)
	runtimeHarness := &fakeRuntime{recorder: recorder, result: testRuntimeResult()}
	sessions := &fakeSessionManager{recorder: recorder}
	writer := &fakeFinalWriter{
		recorder: recorder,
		ack:      FinalMessageAck{EventID: 9001, SeqID: 99, TimestampMs: 1786153000000},
		failures: 1,
	}
	coordinator := newTestCoordinator(t, clock.Now, store, runtimeHarness, sessions, writer, recorder)

	first, err := coordinator.ProcessOne(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "commit frozen final message")
	require.Equal(t, model.AgentRunStatusCommitting, first.Status)
	require.Equal(t, "settled final answer", first.FrozenFinalText)
	require.Equal(t, int64(1), runtimeHarness.runCount.Load())

	clock.Advance(2 * time.Minute) // 让 COMMITTING 租约过期，交给 Reconciler。
	completed, err := coordinator.ProcessOne(context.Background())
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusSucceeded, completed.Status)
	require.Equal(t, int64(1), runtimeHarness.runCount.Load(), "prepared recovery must not invoke the model again")
	require.Len(t, writer.requests, 2)
	require.Equal(t, writer.requests[0], writer.requests[1])
	require.Equal(t, "agent:run-coordinator:final", writer.requests[1].ClientMsgID)
}

func TestCoordinator_HeartbeatsLongRuntimeAndUsesLatestFenceVersion(t *testing.T) {
	base := time.Now().UTC()
	recorder := &callRecorder{}
	store := newMemoryRunStore(testCoordinatorRun(base), recorder)
	runtimeHarness := &fakeRuntime{recorder: recorder, result: testRuntimeResult(), delay: 80 * time.Millisecond}
	sessions := &fakeSessionManager{recorder: recorder}
	writer := &fakeFinalWriter{recorder: recorder, ack: FinalMessageAck{EventID: 10001, SeqID: 100, TimestampMs: time.Now().UnixMilli()}}

	config := testCoordinatorConfig()
	config.LeaseDuration = 90 * time.Millisecond
	config.HeartbeatInterval = 20 * time.Millisecond
	config.RunTimeout = time.Second
	coordinator, err := New(config, testDependencies(store, runtimeHarness, sessions, writer, recorder))
	require.NoError(t, err)

	completed, err := coordinator.ProcessOne(context.Background())
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusSucceeded, completed.Status)
	require.GreaterOrEqual(t, store.heartbeatCount.Load(), int64(2))
}

func TestCoordinator_ProfileMismatchFailsClosedBeforeRuntime(t *testing.T) {
	base := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	recorder := &callRecorder{}
	store := newMemoryRunStore(testCoordinatorRun(base), recorder)
	runtimeHarness := &fakeRuntime{recorder: recorder, result: testRuntimeResult()}
	sessions := &fakeSessionManager{recorder: recorder}
	writer := &fakeFinalWriter{recorder: recorder}
	deps := testDependencies(store, runtimeHarness, sessions, writer, recorder)
	deps.Profiles = fakeProfileResolver{recorder: recorder, profile: pilotruntime.ProfileSnapshot{
		ID: "iam-admin", Version: 1, Provider: "anthropic", Model: "claude-sonnet-4-5", SystemPrompt: "admin",
	}}
	coordinator, err := New(testCoordinatorConfig(), deps, WithClock(func() time.Time { return base }), WithLeaseTokenSource(func() (string, error) { return "token-fixed", nil }))
	require.NoError(t, err)

	failed, err := coordinator.ProcessOne(context.Background())
	require.ErrorIs(t, err, ErrProfileMismatch)
	require.Equal(t, model.AgentRunStatusFailedFinal, failed.Status)
	require.Equal(t, "profile_mismatch", failed.LastErrorCode)
	require.Zero(t, runtimeHarness.runCount.Load())
	require.False(t, sessions.started.Load())
}

func TestCoordinator_DirtyBindingRebuildsFromAuthoritativeHistoryAndReplacesByGeneration(t *testing.T) {
	base := time.Date(2026, 8, 8, 11, 0, 0, 0, time.UTC)
	recorder := &callRecorder{}
	store := newMemoryRunStore(testCoordinatorRun(base), recorder)
	store.binding = &model.AgentSessionBinding{
		TenantID: "tenant-a", ConversationID: "conversation-a", Generation: 7,
		Status: model.AgentSessionBindingStatusDirty,
	}
	runtimeHarness := &fakeRuntime{recorder: recorder, result: testRuntimeResult()}
	sessions := &fakeSessionManager{recorder: recorder}
	writer := &fakeFinalWriter{recorder: recorder, ack: FinalMessageAck{EventID: 11001, SeqID: 101, TimestampMs: base.UnixMilli()}}
	coordinator := newTestCoordinator(t, func() time.Time { return base }, store, runtimeHarness, sessions, writer, recorder)

	completed, err := coordinator.ProcessOne(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(8), completed.CommittedGeneration)
	require.Equal(t, "rebuilt authoritative history", runtimeHarness.requestSnapshot().Prompt)
	requireOrder(t, recorder.snapshot(), "binding", "session-start", "history", "session-start", "runtime-run")
}

func TestCoordinator_SessionRolloverRebuildsAtSafeRunBoundary(t *testing.T) {
	base := time.Date(2026, 8, 9, 9, 0, 0, 0, time.UTC)
	recorder := &callRecorder{}
	store := newMemoryRunStore(testCoordinatorRun(base), recorder)
	store.binding = activeTestBinding(store.run)
	runtimeHarness := &fakeRuntime{recorder: recorder, result: testRuntimeResult()}
	sessions := &fakeSessionManager{recorder: recorder, rollover: true}
	writer := &fakeFinalWriter{recorder: recorder, ack: FinalMessageAck{EventID: 12001, SeqID: 102, TimestampMs: base.UnixMilli()}}
	coordinator := newTestCoordinator(t, func() time.Time { return base }, store, runtimeHarness, sessions, writer, recorder)

	completed, err := coordinator.ProcessOne(context.Background())
	require.NoError(t, err)
	require.Equal(t, store.binding.Generation+1, completed.CommittedGeneration)
	require.Equal(t, "rebuilt authoritative history", runtimeHarness.requestSnapshot().Prompt)
	requireOrder(t, recorder.snapshot(), "binding", "session-start", "history", "session-start", "runtime-run")
}

func newTestCoordinator(
	t *testing.T,
	now func() time.Time,
	store *memoryRunStore,
	runtimeHarness *fakeRuntime,
	sessions *fakeSessionManager,
	writer *fakeFinalWriter,
	recorder *callRecorder,
) *Coordinator {
	t.Helper()
	coordinator, err := New(
		testCoordinatorConfig(),
		testDependencies(store, runtimeHarness, sessions, writer, recorder),
		WithClock(now),
		WithLeaseTokenSource(func() (string, error) { return fmt.Sprintf("token-%d", store.claims.Load()+1), nil }),
	)
	require.NoError(t, err)
	return coordinator
}

func testCoordinatorConfig() Config {
	return Config{
		TenantID:          "tenant-a",
		ProfileID:         "user-assistant",
		ProfileVersion:    1,
		WorkerID:          "worker-a",
		LeaseDuration:     3 * time.Second,
		HeartbeatInterval: time.Second,
		RunTimeout:        5 * time.Second,
		RetryBackoff:      time.Second,
		MaxProviderCalls:  8,
	}
}

func testDependencies(
	store *memoryRunStore,
	runtimeHarness *fakeRuntime,
	sessions *fakeSessionManager,
	writer *fakeFinalWriter,
	recorder *callRecorder,
) Dependencies {
	return Dependencies{
		Runs: store, Runtime: runtimeHarness, Sessions: sessions,
		Profiles: fakeProfileResolver{recorder: recorder, profile: pilotruntime.ProfileSnapshot{
			ID: "user-assistant", Version: 1, Provider: "anthropic", Model: "claude-sonnet-4-5", SystemPrompt: "business assistant",
		}},
		Principals: fakePrincipalResolver{recorder: recorder, principal: pilotruntime.ActorPrincipal{
			TenantID: "tenant-a", ActorID: "user-1", Username: "user-1", Roles: []string{"user"}, Scopes: []string{model.ScopeChatUse, model.ScopeProfileSelfRead},
		}},
		Capabilities: fakeCapabilityIssuer{recorder: recorder}, History: fakeHistoryResolver{recorder: recorder}, FinalMessages: writer,
	}
}

func testCoordinatorRun(now time.Time) *model.AgentRun {
	return &model.AgentRun{
		RunID: "run-coordinator", TenantID: "tenant-a", ConversationID: "conversation-a",
		SourceEventID: 1001, SourceSeqID: 1, SourceTimestampMs: now.UnixMilli(), SourceHash: testCoordinatorDigest,
		Prompt: "tell me about my profile", ActorID: "user-1", ActorUsername: "user-1",
		ProfileID: "user-assistant", ProfileVersion: 1,
		RuntimeKind: "pi", RuntimeVersion: "0.50.1", BridgeVersion: "1.0.0",
		ModelProvider: "anthropic", ModelID: "claude-sonnet-4-5",
		Status: model.AgentRunStatusQueued, MaxAttempts: 3, Version: 1,
		AvailableAt: now, QueuedAt: now,
	}
}

func activeTestBinding(run *model.AgentRun) *model.AgentSessionBinding {
	return &model.AgentSessionBinding{
		TenantID: run.TenantID, ConversationID: run.ConversationID,
		RuntimeKind: run.RuntimeKind, RuntimeVersion: run.RuntimeVersion, BridgeVersion: run.BridgeVersion,
		RuntimeSessionID: "old-privileged-session", SessionRef: "objects/old-privileged-session.jsonl",
		Checksum: testCoordinatorDigest, ProfileID: run.ProfileID, ProfileVersion: run.ProfileVersion,
		Generation: 3, LastCommittedEntryID: "leaf-old", Status: model.AgentSessionBindingStatusActive, Version: 1,
	}
}

const testCoordinatorDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testRuntimeResult() pilotruntime.RunResult {
	return pilotruntime.RunResult{
		FinalText: "settled final answer", SessionID: "pi-session-1", SessionFile: "/staging/session.jsonl", LeafEntryID: "leaf-1",
		Usage: &pilotruntime.Usage{State: pilotruntime.UsageStateExact, InputTokens: 10, OutputTokens: 20, TotalTokens: 30, CostMicros: 200_000, Cost: 0.2},
	}
}

type memoryRunStore struct {
	mu               sync.Mutex
	run              *model.AgentRun
	recorder         *callRecorder
	claims           atomic.Int64
	heartbeatCount   atomic.Int64
	binding          *model.AgentSessionBinding
	budgetAttempt    *model.AgentBudgetAttempt
	budgetReserveErr error
}

func newMemoryRunStore(run *model.AgentRun, recorder *callRecorder) *memoryRunStore {
	return &memoryRunStore{run: cloneAgentRun(run), recorder: recorder}
}

func (s *memoryRunStore) GetAgentSessionBinding(context.Context, string, string) (*model.AgentSessionBinding, error) {
	s.recorder.add("binding")
	if s.binding != nil {
		value := *s.binding
		return &value, nil
	}
	return nil, repo.ErrAgentSessionBindingNotFound
}

func (s *memoryRunStore) MarkAgentSessionBindingDirty(
	_ context.Context,
	_, _ string,
	expectedGeneration int64,
	_ time.Time,
) (*model.AgentSessionBinding, error) {
	s.recorder.add("binding-dirty")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.binding == nil {
		return nil, repo.ErrAgentSessionBindingNotFound
	}
	if s.binding.Generation != expectedGeneration {
		return nil, repo.ErrAgentSessionBindingConflict
	}
	s.binding.Status = model.AgentSessionBindingStatusDirty
	s.binding.Version++
	clone := *s.binding
	return &clone, nil
}

func (s *memoryRunStore) ClaimNextAgentRun(_ context.Context, claim repo.AgentRunClaim) (*model.AgentRun, error) {
	s.recorder.add("claim-execution")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run.Status != model.AgentRunStatusQueued && s.run.Status != model.AgentRunStatusFailedRetryable {
		return nil, repo.ErrNoAgentRunAvailable
	}
	s.run.Status = model.AgentRunStatusClaimed
	s.run.Attempt++
	s.run.LeaseOwner = claim.WorkerID
	s.run.LeaseToken = claim.LeaseToken
	expires := claim.Now.Add(claim.LeaseDuration)
	s.run.LeaseExpiresAt = &expires
	s.run.Version++
	s.claims.Add(1)
	return cloneAgentRun(s.run), nil
}

func (s *memoryRunStore) ClaimPreparedAgentRun(_ context.Context, claim repo.AgentRunClaim) (*model.AgentRun, error) {
	s.recorder.add("claim-prepared")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run.Status != model.AgentRunStatusReadyToCommit && s.run.Status != model.AgentRunStatusCommitting {
		return nil, repo.ErrNoAgentRunAvailable
	}
	if s.run.LeaseExpiresAt != nil && s.run.LeaseExpiresAt.After(claim.Now) {
		return nil, repo.ErrNoAgentRunAvailable
	}
	s.run.Status = model.AgentRunStatusCommitting
	s.run.LeaseOwner = claim.WorkerID
	s.run.LeaseToken = claim.LeaseToken
	expires := claim.Now.Add(claim.LeaseDuration)
	s.run.LeaseExpiresAt = &expires
	s.run.Version++
	s.claims.Add(1)
	return cloneAgentRun(s.run), nil
}

func (s *memoryRunStore) HeartbeatAgentRun(_ context.Context, lease repo.AgentRunLease) (*model.AgentRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.matchesLease(lease) {
		return nil, repo.ErrAgentRunFenceLost
	}
	expires := lease.Now.Add(lease.LeaseDuration)
	s.run.LeaseExpiresAt = &expires
	s.run.Version++
	s.heartbeatCount.Add(1)
	return cloneAgentRun(s.run), nil
}

func (s *memoryRunStore) AdvanceAgentRun(_ context.Context, transition repo.AgentRunTransition) (*model.AgentRun, error) {
	s.recorder.add("advance-" + transition.To)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.matchesLease(transition.Lease) || s.run.Status != transition.From {
		return nil, repo.ErrAgentRunFenceLost
	}
	s.run.Status = transition.To
	s.run.Version++
	return cloneAgentRun(s.run), nil
}

func (s *memoryRunStore) FailAgentRun(_ context.Context, failure repo.AgentRunFailure) (*model.AgentRun, error) {
	s.recorder.add("fail")
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.matchesLease(failure.Lease) {
		return nil, repo.ErrAgentRunFenceLost
	}
	if failure.Retryable && s.run.Attempt < s.run.MaxAttempts {
		s.run.Status = model.AgentRunStatusFailedRetryable
		s.run.LastErrorRetryable = true
	} else {
		s.run.Status = model.AgentRunStatusFailedFinal
		s.run.LastErrorRetryable = false
	}
	s.run.LastErrorCode = failure.ErrorCode
	s.run.LastErrorSummary = failure.ErrorSummary
	s.run.LeaseOwner, s.run.LeaseToken, s.run.LeaseExpiresAt = "", "", nil
	s.run.Version++
	return cloneAgentRun(s.run), nil
}

func (s *memoryRunStore) CancelAgentRun(_ context.Context, cancellation repo.AgentRunCancellation) (*model.AgentRun, error) {
	s.recorder.add("cancel-active")
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.matchesLease(cancellation.Lease) {
		return nil, repo.ErrAgentRunFenceLost
	}
	s.run.Status = model.AgentRunStatusCancelled
	s.run.LastErrorCode = cancellation.ErrorCode
	s.run.LastErrorSummary = cancellation.ErrorSummary
	s.run.LastErrorRetryable = false
	s.run.LeaseOwner, s.run.LeaseToken, s.run.LeaseExpiresAt = "", "", nil
	s.run.Version++
	return cloneAgentRun(s.run), nil
}

func (s *memoryRunStore) CancelPendingAgentRuns(_ context.Context, _ repo.AgentPendingRunCancellation) (int64, error) {
	s.recorder.add("cancel-pending")
	return 0, nil
}

func (s *memoryRunStore) PrepareAgentRun(_ context.Context, prepared repo.AgentRunPreparedResult) (*model.AgentRun, error) {
	s.recorder.add("prepare-result")
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.matchesLease(prepared.Lease) || s.run.Status != model.AgentRunStatusRunning {
		return nil, repo.ErrAgentRunFenceLost
	}
	s.run.Status = model.AgentRunStatusReadyToCommit
	s.run.BaseSessionGeneration = prepared.BaseSessionGeneration
	s.run.CandidateSessionID = prepared.CandidateSessionID
	s.run.CandidateSessionRef = prepared.CandidateSessionRef
	s.run.CandidateChecksum = prepared.CandidateChecksum
	s.run.CandidateLeafEntryID = prepared.CandidateLeafEntryID
	s.run.CandidateSessionBytes = prepared.CandidateSessionBytes
	s.run.CandidateEntryCount = prepared.CandidateEntryCount
	s.run.FrozenFinalText = prepared.FrozenFinalText
	s.run.FinalClientMsgID = prepared.FinalClientMsgID
	s.run.Version++
	return cloneAgentRun(s.run), nil
}

func (s *memoryRunStore) BeginAgentRunCommit(_ context.Context, lease repo.AgentRunLease) (*model.AgentRun, error) {
	s.recorder.add("begin-commit")
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.matchesLease(lease) || s.run.Status != model.AgentRunStatusReadyToCommit {
		return nil, repo.ErrAgentRunFenceLost
	}
	s.run.Status = model.AgentRunStatusCommitting
	s.run.Version++
	return cloneAgentRun(s.run), nil
}

func (s *memoryRunStore) RecordAgentRunFinalMessage(_ context.Context, result repo.AgentRunFinalMessage) (*model.AgentRun, error) {
	s.recorder.add("record-final")
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.matchesLease(result.Lease) || s.run.Status != model.AgentRunStatusCommitting {
		return nil, repo.ErrAgentRunFenceLost
	}
	s.run.FinalEventID = result.EventID
	s.run.FinalSeqID = result.SeqID
	s.run.FinalTimestampMs = result.TimestampMs
	s.run.Version++
	return cloneAgentRun(s.run), nil
}

func (s *memoryRunStore) CompleteAgentRun(_ context.Context, completion repo.AgentRunCompletion) (*model.AgentRun, error) {
	s.recorder.add("complete")
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.matchesLease(completion.Lease) || s.run.Status != model.AgentRunStatusCommitting {
		return nil, repo.ErrAgentRunFenceLost
	}
	s.run.Status = model.AgentRunStatusSucceeded
	s.run.CommittedGeneration = completion.CommittedGeneration
	s.run.LeaseOwner, s.run.LeaseToken, s.run.LeaseExpiresAt = "", "", nil
	s.run.Version++
	return cloneAgentRun(s.run), nil
}

func (s *memoryRunStore) RecoverExpiredAgentRuns(context.Context, string, time.Time) (repo.AgentRunRecoveryResult, error) {
	return repo.AgentRunRecoveryResult{}, nil
}

func (s *memoryRunStore) ReserveAgentBudget(_ context.Context, reservation repo.AgentBudgetReservation) (*model.AgentBudgetAttempt, error) {
	s.recorder.add("budget-reserve")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.budgetReserveErr != nil {
		return nil, s.budgetReserveErr
	}
	if s.run.Status != model.AgentRunStatusStartingRuntime || !s.matchesLease(reservation.Lease) ||
		s.run.Attempt != reservation.Attempt || s.run.ProfileID != reservation.ProfileID || s.run.ProfileVersion != reservation.ProfileVersion {
		return nil, repo.ErrAgentRunFenceLost
	}
	if s.budgetAttempt == nil {
		s.budgetAttempt = &model.AgentBudgetAttempt{
			TenantID: s.run.TenantID, RunID: s.run.RunID, Attempt: s.run.Attempt,
			ProfileID: s.run.ProfileID, ProfileVersion: s.run.ProfileVersion,
			LeaseOwner: s.run.LeaseOwner, LeaseToken: s.run.LeaseToken,
			ReservedTokens: 1_000, ReservedCostMicros: 500_000,
			Status: model.AgentBudgetAttemptStatusReserved, Version: 1,
		}
	}
	clone := *s.budgetAttempt
	return &clone, nil
}

func (s *memoryRunStore) SettleAgentBudget(_ context.Context, settlement repo.AgentBudgetSettlement) (*model.AgentBudgetAttempt, error) {
	s.recorder.add("budget-settle")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.budgetAttempt == nil || !s.matchesLease(settlement.Lease) || settlement.Attempt != s.run.Attempt {
		return nil, repo.ErrAgentBudgetAttemptConflict
	}
	s.budgetAttempt.UsageState = settlement.Usage.State
	s.budgetAttempt.ActualTotalTokens = settlement.Usage.TotalTokens
	s.budgetAttempt.ActualCostMicros = settlement.Usage.CostMicros
	switch settlement.Usage.State {
	case string(pilotruntime.UsageStateExact):
		s.budgetAttempt.Status = model.AgentBudgetAttemptStatusSettled
	case string(pilotruntime.UsageStateNotStarted):
		s.budgetAttempt.Status = model.AgentBudgetAttemptStatusReleased
	case string(pilotruntime.UsageStateUnknown):
		s.budgetAttempt.Status = model.AgentBudgetAttemptStatusUnknown
	}
	s.budgetAttempt.Version++
	clone := *s.budgetAttempt
	return &clone, nil
}

func (s *memoryRunStore) RecoverExpiredAgentBudgetAttempts(context.Context, string, time.Time) (int64, error) {
	return 0, nil
}

func (s *memoryRunStore) matchesLease(lease repo.AgentRunLease) bool {
	return s.run.TenantID == lease.TenantID && s.run.RunID == lease.RunID &&
		s.run.LeaseOwner == lease.WorkerID && s.run.LeaseToken == lease.LeaseToken &&
		s.run.Version == lease.ExpectedVersion && s.run.LeaseExpiresAt != nil && s.run.LeaseExpiresAt.After(lease.Now)
}

type fakeRuntime struct {
	recorder *callRecorder
	result   pilotruntime.RunResult
	delay    time.Duration
	runCount atomic.Int64
	mu       sync.Mutex
	request  pilotruntime.RunRequest
	runErr   error
	waitErr  error
}

func (r *fakeRuntime) Run(ctx context.Context, request pilotruntime.RunRequest) (pilotruntime.EventStream, error) {
	r.recorder.add("runtime-run")
	r.runCount.Add(1)
	r.mu.Lock()
	r.request = request
	r.mu.Unlock()
	if r.runErr != nil {
		return nil, r.runErr
	}
	if request.Capability.IsZero() || request.Prompt == "" || request.Profile.SystemPrompt == "" {
		return nil, fmt.Errorf("runtime request incomplete")
	}
	return newFakeEventStream(ctx, r.result, r.delay, r.waitErr), nil
}

func (r *fakeRuntime) requestSnapshot() pilotruntime.RunRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.request
}

func (*fakeRuntime) Abort(context.Context, string) error { return nil }
func (*fakeRuntime) Probe(context.Context) error         { return nil }
func (*fakeRuntime) Shutdown(context.Context) error      { return nil }

type fakeEventStream struct {
	events chan pilotruntime.RuntimeEvent
	done   chan struct{}
	result pilotruntime.RunResult
	err    error
}

func newFakeEventStream(ctx context.Context, result pilotruntime.RunResult, delay time.Duration, waitErr error) *fakeEventStream {
	stream := &fakeEventStream{events: make(chan pilotruntime.RuntimeEvent, 1), done: make(chan struct{}), result: result, err: waitErr}
	go func() {
		defer close(stream.done)
		defer close(stream.events)
		stream.events <- pilotruntime.RuntimeEvent{Kind: pilotruntime.EventStarted, Sequence: 1}
		if delay == 0 {
			return
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			stream.err = ctx.Err()
		case <-timer.C:
		}
	}()
	return stream
}

func (s *fakeEventStream) Events() <-chan pilotruntime.RuntimeEvent { return s.events }
func (s *fakeEventStream) Wait() (pilotruntime.RunResult, error) {
	<-s.done
	return s.result, s.err
}

type fakeSessionManager struct {
	recorder  *callRecorder
	started   atomic.Bool
	discarded atomic.Bool
	rollover  bool
}

func (m *fakeSessionManager) Start(_ context.Context, run *model.AgentRun, binding *model.AgentSessionBinding) (session.Staging, error) {
	m.recorder.add("session-start")
	if binding != nil && m.rollover {
		m.rollover = false
		return session.Staging{}, errors.Join(session.ErrBindingNeedsRebuild, session.ErrSessionRollover)
	}
	if binding != nil && binding.Status == model.AgentSessionBindingStatusDirty {
		return session.Staging{}, session.ErrBindingNeedsRebuild
	}
	m.started.Store(true)
	return session.Staging{RunID: run.RunID, TenantID: run.TenantID, ConversationID: run.ConversationID, Snapshot: pilotruntime.SessionSnapshot{Directory: "/staging"}}, nil
}

func (m *fakeSessionManager) PrepareCandidate(_ context.Context, _ session.Staging, result pilotruntime.RunResult) (session.Candidate, error) {
	m.recorder.add("session-prepare")
	return session.Candidate{
		SessionID: result.SessionID, SessionRef: "objects/aa/session.jsonl",
		Checksum: testCoordinatorDigest, LeafEntryID: result.LeafEntryID, ByteSize: 100, EntryCount: 2,
	}, nil
}

func (m *fakeSessionManager) Discard(context.Context, session.Staging) error {
	m.recorder.add("session-discard")
	m.discarded.Store(true)
	return nil
}
func (*fakeSessionManager) Close() error { return nil }

type fakeProfileResolver struct {
	recorder *callRecorder
	profile  pilotruntime.ProfileSnapshot
}

func (r fakeProfileResolver) ResolveProfile(context.Context, string, string, int64) (pilotruntime.ProfileSnapshot, error) {
	r.recorder.add("profile")
	return r.profile, nil
}

type fakePrincipalResolver struct {
	recorder  *callRecorder
	principal pilotruntime.ActorPrincipal
	err       error
}

func (r fakePrincipalResolver) ResolvePrincipal(context.Context, string, string) (pilotruntime.ActorPrincipal, error) {
	r.recorder.add("principal")
	return r.principal, r.err
}

type revokingPrincipalResolver struct {
	recorder     *callRecorder
	allowedCalls int64
	calls        atomic.Int64
	principal    pilotruntime.ActorPrincipal
}

func (r *revokingPrincipalResolver) ResolvePrincipal(context.Context, string, string) (pilotruntime.ActorPrincipal, error) {
	r.recorder.add("principal")
	if r.calls.Add(1) > r.allowedCalls {
		return pilotruntime.ActorPrincipal{}, identity.ErrPrincipalUnauthorized
	}
	return r.principal, nil
}

type fakeCapabilityIssuer struct{ recorder *callRecorder }

func (i fakeCapabilityIssuer) IssueCapability(context.Context, *model.AgentRun, pilotruntime.ActorPrincipal) (pilotruntime.Secret, error) {
	i.recorder.add("capability")
	return pilotruntime.NewSecret("signed-capability"), nil
}

type fakeHistoryResolver struct{ recorder *callRecorder }

func (r fakeHistoryResolver) RebuildPrompt(context.Context, *model.AgentRun) (string, error) {
	r.recorder.add("history")
	return "rebuilt authoritative history", nil
}

type fakeFinalWriter struct {
	mu       sync.Mutex
	recorder *callRecorder
	ack      FinalMessageAck
	failures int
	requests []FinalMessageRequest
}

func (w *fakeFinalWriter) CommitFinalMessage(_ context.Context, request FinalMessageRequest) (FinalMessageAck, error) {
	w.recorder.add("final-message")
	w.mu.Lock()
	defer w.mu.Unlock()
	w.requests = append(w.requests, request)
	if w.failures > 0 {
		w.failures--
		return FinalMessageAck{}, errors.New("injected logic failure")
	}
	return w.ack, nil
}

type callRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *callRecorder) add(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

func (r *callRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func requireOrder(t *testing.T, calls []string, expected ...string) {
	t.Helper()
	position := 0
	for _, call := range calls {
		if position < len(expected) && call == expected[position] {
			position++
		}
	}
	require.Equal(t, len(expected), position, "calls=%v expected subsequence=%v", calls, expected)
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(now time.Time) *testClock { return &testClock{now: now} }
func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *testClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}
