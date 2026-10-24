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
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
	"github.com/ceyewan/resonance/pilot/session"
	"github.com/ceyewan/resonance/repo"
)

func TestCoordinator_ExecutesEndToEnd(t *testing.T) {
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
	require.Equal(t, int64(8192), runtimeHarness.requestSnapshot().MaxOutputTokens)
	require.Len(t, writer.requests, 1)
	require.Equal(t, "agent:run-coordinator:final", writer.requests[0].ClientMsgID)
	require.Equal(t, "settled final answer", writer.requests[0].Content)
	require.True(t, sessions.discarded.Load())

	requireOrder(t, recorder.snapshot(),
		"claim-prepared", "claim-execution", "binding", "history",
		"session-start", "advance-STARTING_RUNTIME", "runtime-run", "advance-RUNNING",
		"session-prepare", "prepare-result", "begin-commit", "final-message", "record-final", "complete",
	)
}

func TestCoordinator_PreparedRecoveryDoesNotRerunModel(t *testing.T) {
	base := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	clock := newTestClock(base)
	recorder := &callRecorder{}
	run := testCoordinatorRun(base)
	run.Status = model.AgentRunStatusReadyToCommit
	run.CandidateSessionID = "candidate-session"
	run.CandidateSessionRef = "objects/candidate.jsonl"
	run.CandidateChecksum = testCoordinatorDigest
	run.CandidateLeafEntryID = "leaf-candidate"
	run.CandidateSessionBytes = 100
	run.CandidateEntryCount = 2
	run.FrozenFinalText = "settled final answer"
	run.FinalClientMsgID = "agent:" + run.RunID + ":final"
	store := newMemoryRunStore(run, recorder)
	runtimeHarness := &fakeRuntime{recorder: recorder, result: testRuntimeResult()}
	sessions := &fakeSessionManager{recorder: recorder}
	writer := &fakeFinalWriter{recorder: recorder, ack: FinalMessageAck{EventID: 9001, SeqID: 99, TimestampMs: 1786153000000}}
	coordinator := newTestCoordinator(t, clock.Now, store, runtimeHarness, sessions, writer, recorder)

	completed, err := coordinator.ProcessOne(context.Background())
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusSucceeded, completed.Status)
	require.Zero(t, runtimeHarness.runCount.Load(), "prepared recovery must not rerun the model")
	require.Len(t, writer.requests, 1)
	require.Equal(t, "agent:run-coordinator:final", writer.requests[0].ClientMsgID)
}

// 撤回/编辑发生在 Run 执行期间：最终回复绝不能写进 Logic，Run 也不能把基于旧
// 快照的候选晋升成新基线。两种失效都要挡住：ingress 取消 Run，以及仅快照被置脏。
// 两条路径的终态都是"取消"，不重试——撤回窗口只有 2 分钟，被撤回的必然是刚说的话。
func TestCoordinator_HistoryInvalidatedDuringRunBlocksFinalMessage(t *testing.T) {
	mutations := map[string]func(*memoryRunStore){
		"run cancelled": func(store *memoryRunStore) { store.invalidate() },
		"binding dirtied only": func(store *memoryRunStore) {
			store.mu.Lock()
			defer store.mu.Unlock()
			store.binding.Status = model.AgentSessionBindingStatusDirty
			store.binding.Version++
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
			recorder := &callRecorder{}
			run := testCoordinatorRun(base)
			store := newMemoryRunStore(run, recorder)
			store.binding = &model.AgentSessionBinding{
				TenantID: run.TenantID, ConversationID: run.ConversationID,
				RuntimeVersion: run.RuntimeVersion, BridgeVersion: run.BridgeVersion,
				RuntimeSessionID: "old-session", SessionRef: "objects/old.jsonl", Checksum: testCoordinatorDigest,
				ProfileID: run.ProfileID, ProfileVersion: run.ProfileVersion,
				Generation: 1, LastCommittedEntryID: "leaf-old",
				Status: model.AgentSessionBindingStatusActive, Version: 5,
			}
			runtimeHarness := &fakeRuntime{
				recorder: recorder, result: testRuntimeResult(),
				afterRun: func() { mutate(store) },
			}
			sessions := &fakeSessionManager{recorder: recorder}
			writer := &fakeFinalWriter{recorder: recorder, ack: FinalMessageAck{EventID: 8101, SeqID: 89, TimestampMs: 1786153000000}}
			coordinator := newTestCoordinator(t, func() time.Time { return base }, store, runtimeHarness, sessions, writer, recorder)

			_, err := coordinator.ProcessOne(context.Background())
			require.Error(t, err)
			require.Empty(t, writer.requests, "被失效的 Run 不允许写出最终回复")
			require.NotContains(t, recorder.snapshot(), "final-message")
			require.NotContains(t, recorder.snapshot(), "complete")

			// 不能占着租约等它过期：worker 对非致命错误只记 Warn，Run 会一直挂在
			// COMMITTING 里，直到 lease_ttl + recovery_interval 后才被回收。
			stored, getErr := store.GetAgentRun(context.Background(), run.TenantID, run.RunID)
			require.NoError(t, getErr)
			require.Empty(t, stored.LeaseToken)
			require.Nil(t, stored.LeaseExpiresAt)
			// 两种路径都收敛到"取消"：撤回只可能命中刚说的话，补一条回答是错的。
			require.Equal(t, model.AgentRunStatusCancelled, stored.Status)
			if name == "run cancelled" {
				// ingress 抢先取消：worker 后续推进只会 fence lost，不再改写状态。
				return
			}
			require.ErrorIs(t, err, errHistoryInvalidated)
			require.Equal(t, "history_invalidated", stored.LastErrorCode)
			require.False(t, stored.LastErrorRetryable)
			require.Empty(t, stored.FinalClientMsgID, "取消的 Run 不得留下待写出的最终消息")
		})
	}
}

func TestCoordinator_HistoryVerificationFailureFailsRun(t *testing.T) {
	base := time.Date(2026, 8, 8, 11, 0, 0, 0, time.UTC)
	recorder := &callRecorder{}
	run := testCoordinatorRun(base)
	store := newMemoryRunStore(run, recorder)
	runtimeHarness := &fakeRuntime{recorder: recorder, result: testRuntimeResult()}
	writer := &fakeFinalWriter{recorder: recorder}
	deps := testDependencies(store, runtimeHarness, &fakeSessionManager{recorder: recorder}, writer, recorder)
	deps.History = fakeHistoryResolver{recorder: recorder, err: errors.New("source message was recalled")}
	coordinator, err := New(testCoordinatorConfig(), deps, WithClock(func() time.Time { return base }), WithLeaseTokenSource(func() (string, error) { return "token-fixed", nil }))
	require.NoError(t, err)

	failed, err := coordinator.ProcessOne(context.Background())
	require.ErrorContains(t, err, "source message was recalled")
	require.Equal(t, model.AgentRunStatusFailedRetryable, failed.Status)
	require.Equal(t, "history_unavailable", failed.LastErrorCode)
	require.Zero(t, runtimeHarness.runCount.Load())
}

func TestCoordinator_HeartbeatsLongRuntime(t *testing.T) {
	recorder := &callRecorder{}
	store := newMemoryRunStore(testCoordinatorRun(time.Now().UTC()), recorder)
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
		Assistant: pilotruntime.ProfileSnapshot{
			ID: "user-assistant", Version: 1, Provider: "dashscope", Model: "qwen3.8-max", SystemPrompt: "pilot assistant",
		},
		ProfileID:         "user-assistant",
		ProfileVersion:    1,
		WorkerID:          "worker-a",
		LeaseDuration:     3 * time.Second,
		HeartbeatInterval: time.Second,
		RunTimeout:        5 * time.Second,
		RetryBackoff:      time.Second,
		MaxOutputTokens:   8192,
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
		History: fakeHistoryResolver{recorder: recorder}, FinalMessages: writer,
	}
}

func testCoordinatorRun(now time.Time) *model.AgentRun {
	return &model.AgentRun{
		RunID: "run-coordinator", TenantID: "tenant-a", ConversationID: "conversation-a",
		SourceEventID: 1001, SourceSeqID: 1, SourceTimestampMs: now.UnixMilli(), SourceHash: testCoordinatorDigest,
		Prompt: "hello pilot", ActorID: "user-1", ActorUsername: "user-1",
		ProfileID: "user-assistant", ProfileVersion: 1,
		RuntimeVersion: "0.84.1", BridgeVersion: "1", ModelID: "qwen3.8-max",
		Status: model.AgentRunStatusQueued, MaxAttempts: 3, Version: 1,
		AvailableAt: now, QueuedAt: now,
	}
}

const testCoordinatorDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testRuntimeResult() pilotruntime.RunResult {
	return pilotruntime.RunResult{
		FinalText: "settled final answer", SessionID: "pi-session-1", SessionFile: "/staging/session.jsonl", LeafEntryID: "leaf-1",
	}
}

type memoryRunStore struct {
	mu             sync.Mutex
	run            *model.AgentRun
	recorder       *callRecorder
	claims         atomic.Int64
	heartbeatCount atomic.Int64
	binding        *model.AgentSessionBinding
}

func newMemoryRunStore(run *model.AgentRun, recorder *callRecorder) *memoryRunStore {
	return &memoryRunStore{run: cloneAgentRun(run), recorder: recorder}
}

func (s *memoryRunStore) GetAgentRun(context.Context, string, string) (*model.AgentRun, error) {
	s.recorder.add("reload-run")
	s.mu.Lock()
	defer s.mu.Unlock()
	value := *s.run
	return &value, nil
}

// invalidate 模拟 ingress 收到撤回/编辑后的自我失效：取消未落地的 Run 并把快照置脏。
func (s *memoryRunStore) invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.run.Status {
	case model.AgentRunStatusQueued, model.AgentRunStatusFailedRetryable, model.AgentRunStatusClaimed,
		model.AgentRunStatusStartingRuntime, model.AgentRunStatusRunning,
		model.AgentRunStatusReadyToCommit, model.AgentRunStatusCommitting:
		s.run.Status = model.AgentRunStatusCancelled
		// 与 repo.InvalidateAgentConversation 一致：取消同时清空租约，
		// 之后 worker 再想收尾只能拿到 fence lost。
		s.run.LeaseOwner, s.run.LeaseToken, s.run.LeaseExpiresAt = "", "", nil
	}
	if s.binding != nil && s.binding.Status == model.AgentSessionBindingStatusActive {
		s.binding.Status = model.AgentSessionBindingStatusDirty
		s.binding.Version++
	}
}

func (s *memoryRunStore) GetAgentSessionBinding(context.Context, string, string) (*model.AgentSessionBinding, error) {
	s.recorder.add("binding")
	if s.binding != nil {
		value := *s.binding
		return &value, nil
	}
	return nil, repo.ErrAgentSessionBindingNotFound
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
	// 与 repo.CancelAgentRun 一致：取消意味着这一轮永远不会提交，冻结的候选一起清掉。
	s.run.FrozenFinalText, s.run.FinalClientMsgID, s.run.CandidateSessionID = "", "", ""
	s.run.Version++
	return cloneAgentRun(s.run), nil
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
	afterRun func()
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
	if request.Prompt == "" || request.Profile.SystemPrompt == "" {
		return nil, fmt.Errorf("runtime request incomplete")
	}
	if r.afterRun != nil {
		r.afterRun()
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
}

func (m *fakeSessionManager) Start(_ context.Context, run *model.AgentRun, binding *model.AgentSessionBinding) (session.Staging, error) {
	m.recorder.add("session-start")
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

type fakeHistoryResolver struct {
	recorder *callRecorder
	err      error
}

func (r fakeHistoryResolver) RebuildPrompt(context.Context, *model.AgentRun) (string, error) {
	r.recorder.add("history")
	if r.err != nil {
		return "", r.err
	}
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
