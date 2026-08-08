package repo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/model"
)

func TestAgentRunRepo_EnqueueIsIdempotentPerTenantSourceEvent(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)

	ctx := context.Background()
	queuedAt := time.Date(2026, 8, 8, 1, 0, 0, 0, time.UTC)
	first := newTestAgentRun("run-enqueue-1", "tenant-a", "conversation-a", 101, 1, queuedAt)
	created, err := runRepo.EnqueueAgentRun(ctx, first)
	require.NoError(t, err)
	require.True(t, created.Created)
	require.Equal(t, first.RunID, created.Run.RunID)
	require.Equal(t, model.AgentRunStatusQueued, created.Run.Status)
	require.Equal(t, int64(1), created.Run.Version)

	retry := *first
	retry.RunID = "run-enqueue-retry"
	retry.ProfileVersion = 2 // 部署变化不能改变第一次为该事件冻结的 Run。
	idempotent, err := runRepo.EnqueueAgentRun(ctx, &retry)
	require.NoError(t, err)
	require.False(t, idempotent.Created)
	require.Equal(t, first.RunID, idempotent.Run.RunID)
	require.Equal(t, int64(1), idempotent.Run.ProfileVersion)

	conflict := retry
	conflict.RunID = "run-enqueue-conflict"
	conflict.Prompt = "different prompt"
	conflict.SourceHash = testDigest(conflict.Prompt)
	_, err = runRepo.EnqueueAgentRun(ctx, &conflict)
	require.ErrorIs(t, err, ErrAgentRunSourceConflict)

	otherTenant := *first
	otherTenant.RunID = "run-enqueue-other-tenant"
	otherTenant.TenantID = "tenant-b"
	other, err := runRepo.EnqueueAgentRun(ctx, &otherTenant)
	require.NoError(t, err)
	require.True(t, other.Created)
}

func TestAgentRunRepo_ClaimPreservesConversationOrderAndSerializesActiveRun(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	base := time.Date(2026, 8, 8, 2, 0, 0, 0, time.UTC)

	// 即使 seq=2 更早入库，seq=1 仍必须先执行。
	later := newTestAgentRun("run-seq-2", "tenant-a", "conversation-a", 202, 2, base)
	earlier := newTestAgentRun("run-seq-1", "tenant-a", "conversation-a", 201, 1, base.Add(time.Second))
	_, err = runRepo.EnqueueAgentRun(ctx, later)
	require.NoError(t, err)
	_, err = runRepo.EnqueueAgentRun(ctx, earlier)
	require.NoError(t, err)

	claimed, err := runRepo.ClaimNextAgentRun(ctx, testClaim("tenant-a", "worker-1", "token-1", base.Add(2*time.Second)))
	require.NoError(t, err)
	require.Equal(t, earlier.RunID, claimed.RunID)
	require.Equal(t, 1, claimed.Attempt)

	_, err = runRepo.ClaimNextAgentRun(ctx, testClaim("tenant-a", "worker-2", "token-2", base.Add(3*time.Second)))
	require.ErrorIs(t, err, ErrNoAgentRunAvailable)

	failed, err := runRepo.FailAgentRun(ctx, AgentRunFailure{
		Lease:        leaseFor(claimed, base.Add(4*time.Second)),
		ErrorCode:    "non_retryable",
		ErrorSummary: "test terminal failure",
		Retryable:    false,
	})
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusFailedFinal, failed.Status)

	next, err := runRepo.ClaimNextAgentRun(ctx, testClaim("tenant-a", "worker-2", "token-2", base.Add(5*time.Second)))
	require.NoError(t, err)
	require.Equal(t, later.RunID, next.RunID)
}

func TestAgentRunRepo_ClaimPinsProfileSnapshot(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	base := time.Date(2026, 8, 9, 1, 0, 0, 0, time.UTC)
	iamRun := newTestAgentRun("run-profile-iam", "tenant-profile", "conversation-iam", 9101, 1, base)
	iamRun.ProfileID = model.AgentProfileIAMAdmin
	userRun := newTestAgentRun("run-profile-user", "tenant-profile", "conversation-user", 9102, 1, base.Add(time.Second))
	_, err = runRepo.EnqueueAgentRun(ctx, iamRun)
	require.NoError(t, err)
	_, err = runRepo.EnqueueAgentRun(ctx, userRun)
	require.NoError(t, err)

	userClaim := testClaim("tenant-profile", "worker-user", "token-user", base.Add(2*time.Second))
	claimed, err := runRepo.ClaimNextAgentRun(ctx, userClaim)
	require.NoError(t, err)
	require.Equal(t, userRun.RunID, claimed.RunID)

	iamClaim := testClaim("tenant-profile", "worker-iam", "token-iam", base.Add(2*time.Second))
	iamClaim.ProfileID = model.AgentProfileIAMAdmin
	claimed, err = runRepo.ClaimNextAgentRun(ctx, iamClaim)
	require.NoError(t, err)
	require.Equal(t, iamRun.RunID, claimed.RunID)
}

func TestAgentRunRepo_ProfileRevocationCancelsClaimedAndPendingRunsOnlyWithinBoundary(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	base := time.Date(2026, 8, 9, 2, 0, 0, 0, time.UTC)
	for index := int64(1); index <= 3; index++ {
		run := newTestAgentRun(fmt.Sprintf("run-revoked-%d", index), "tenant-a", "conversation-revoked", 9200+index, index, base.Add(time.Duration(index)*time.Millisecond))
		run.ProfileID = model.AgentProfileIAMAdmin
		_, err = runRepo.EnqueueAgentRun(ctx, run)
		require.NoError(t, err)
	}
	other := newTestAgentRun("run-other-actor", "tenant-a", "conversation-other", 9301, 1, base)
	other.ProfileID = model.AgentProfileIAMAdmin
	other.ActorID = "other-user"
	other.ActorUsername = "other-user"
	_, err = runRepo.EnqueueAgentRun(ctx, other)
	require.NoError(t, err)

	claim := testClaim("tenant-a", "worker-iam", "token-iam", base.Add(time.Second))
	claim.ProfileID = model.AgentProfileIAMAdmin
	claimed, err := runRepo.ClaimNextAgentRun(ctx, claim)
	require.NoError(t, err)
	require.Equal(t, "run-revoked-1", claimed.RunID)

	cancelledPending, err := runRepo.CancelPendingAgentRuns(ctx, AgentPendingRunCancellation{
		TenantID: "tenant-a", ActorID: claimed.ActorID, ProfileID: claimed.ProfileID, ProfileVersion: claimed.ProfileVersion,
		ErrorCode: "principal_revoked", ErrorSummary: "profile access was revoked", Now: base.Add(2 * time.Second),
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), cancelledPending)

	cancelled, err := runRepo.CancelAgentRun(ctx, AgentRunCancellation{
		Lease: leaseFor(claimed, base.Add(2*time.Second)), ErrorCode: "principal_revoked", ErrorSummary: "profile access was revoked",
	})
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusCancelled, cancelled.Status)
	require.NotNil(t, cancelled.CompletedAt)
	require.Empty(t, cancelled.LeaseToken)

	for index := int64(1); index <= 3; index++ {
		stored, getErr := runRepo.GetAgentRun(ctx, "tenant-a", fmt.Sprintf("run-revoked-%d", index))
		require.NoError(t, getErr)
		require.Equal(t, model.AgentRunStatusCancelled, stored.Status)
	}
	untouched, err := runRepo.GetAgentRun(ctx, "tenant-a", other.RunID)
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusQueued, untouched.Status)
}

func TestAgentRunRepo_ConcurrentClaimAllowsOneActiveRunPerConversation(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	base := time.Date(2026, 8, 8, 3, 0, 0, 0, time.UTC)
	for seq := int64(1); seq <= 5; seq++ {
		run := newTestAgentRun(fmt.Sprintf("run-concurrent-%d", seq), "tenant-a", "conversation-a", 300+seq, seq, base.Add(time.Duration(seq)*time.Millisecond))
		_, err := runRepo.EnqueueAgentRun(ctx, run)
		require.NoError(t, err)
	}

	type claimResult struct {
		run *model.AgentRun
		err error
	}
	results := make(chan claimResult, 8)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		worker := worker
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			run, claimErr := runRepo.ClaimNextAgentRun(ctx, testClaim(
				"tenant-a",
				fmt.Sprintf("worker-%d", worker),
				fmt.Sprintf("token-%d", worker),
				base.Add(time.Second),
			))
			results <- claimResult{run: run, err: claimErr}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	claimed := make([]*model.AgentRun, 0, 1)
	for result := range results {
		if result.err == nil {
			claimed = append(claimed, result.run)
			continue
		}
		require.ErrorIs(t, result.err, ErrNoAgentRunAvailable)
	}
	require.Len(t, claimed, 1)
	require.Equal(t, int64(1), claimed[0].SourceSeqID)
}

func TestAgentRunRepo_LeaseFencesStaleWorkerAndRecoversExecution(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	base := time.Date(2026, 8, 8, 4, 0, 0, 0, time.UTC)
	run := newTestAgentRun("run-fencing", "tenant-a", "conversation-a", 401, 1, base)
	_, err = runRepo.EnqueueAgentRun(ctx, run)
	require.NoError(t, err)

	claimed, err := runRepo.ClaimNextAgentRun(ctx, testClaim("tenant-a", "worker-old", "token-old", base.Add(time.Second)))
	require.NoError(t, err)

	wrongToken := leaseFor(claimed, base.Add(2*time.Second))
	wrongToken.LeaseToken = "wrong-token"
	_, err = runRepo.HeartbeatAgentRun(ctx, wrongToken)
	require.ErrorIs(t, err, ErrAgentRunFenceLost)

	heartbeat := leaseFor(claimed, base.Add(2*time.Second))
	heartbeat.LeaseDuration = time.Minute
	heartbeated, err := runRepo.HeartbeatAgentRun(ctx, heartbeat)
	require.NoError(t, err)
	require.Greater(t, heartbeated.Version, claimed.Version)

	stale := leaseFor(claimed, base.Add(3*time.Second))
	_, err = runRepo.AdvanceAgentRun(ctx, AgentRunTransition{
		Lease: stale,
		From:  model.AgentRunStatusClaimed,
		To:    model.AgentRunStatusStartingRuntime,
	})
	require.ErrorIs(t, err, ErrAgentRunFenceLost)

	starting, err := runRepo.AdvanceAgentRun(ctx, AgentRunTransition{
		Lease: leaseFor(heartbeated, base.Add(3*time.Second)),
		From:  model.AgentRunStatusClaimed,
		To:    model.AgentRunStatusStartingRuntime,
	})
	require.NoError(t, err)

	recovery, err := runRepo.RecoverExpiredAgentRuns(ctx, "tenant-a", base.Add(2*time.Minute))
	require.NoError(t, err)
	require.Equal(t, int64(1), recovery.RetryableExecutions)
	require.Zero(t, recovery.PreparedRuns)

	recovered, err := runRepo.GetAgentRun(ctx, "tenant-a", run.RunID)
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusFailedRetryable, recovered.Status)
	require.Empty(t, recovered.LeaseToken)

	oldLease := leaseFor(starting, base.Add(2*time.Minute))
	_, err = runRepo.HeartbeatAgentRun(ctx, oldLease)
	require.ErrorIs(t, err, ErrAgentRunFenceLost)

	reclaimed, err := runRepo.ClaimNextAgentRun(ctx, testClaim("tenant-a", "worker-new", "token-new", base.Add(2*time.Minute)))
	require.NoError(t, err)
	require.Equal(t, 2, reclaimed.Attempt)
	require.Equal(t, "token-new", reclaimed.LeaseToken)
}

func TestAgentRunRepo_PreparedResultNeverReturnsToExecutionQueue(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	base := time.Date(2026, 8, 8, 5, 0, 0, 0, time.UTC)
	run := newTestAgentRun("run-prepare", "tenant-a", "conversation-a", 501, 1, base)
	_, err = runRepo.EnqueueAgentRun(ctx, run)
	require.NoError(t, err)

	claimed, err := runRepo.ClaimNextAgentRun(ctx, testClaim("tenant-a", "worker-runtime", "token-runtime", base.Add(time.Second)))
	require.NoError(t, err)
	_, err = runRepo.AdvanceAgentRun(ctx, AgentRunTransition{
		Lease: leaseFor(claimed, base.Add(2*time.Second)),
		From:  model.AgentRunStatusClaimed,
		To:    model.AgentRunStatusRunning,
	})
	require.ErrorIs(t, err, ErrAgentRunInvalidTransition)

	starting, err := runRepo.AdvanceAgentRun(ctx, AgentRunTransition{
		Lease: leaseFor(claimed, base.Add(2*time.Second)),
		From:  model.AgentRunStatusClaimed,
		To:    model.AgentRunStatusStartingRuntime,
	})
	require.NoError(t, err)
	running, err := runRepo.AdvanceAgentRun(ctx, AgentRunTransition{
		Lease: leaseFor(starting, base.Add(3*time.Second)),
		From:  model.AgentRunStatusStartingRuntime,
		To:    model.AgentRunStatusRunning,
	})
	require.NoError(t, err)

	prepared, err := runRepo.PrepareAgentRun(ctx, AgentRunPreparedResult{
		Lease:                 leaseFor(running, base.Add(4*time.Second)),
		BaseSessionGeneration: 0,
		CandidateSessionID:    "pi-session-1",
		CandidateSessionRef:   "sessions/tenant-a/conversation-a/1.jsonl",
		CandidateChecksum:     testDigest("candidate-session"),
		CandidateLeafEntryID:  "leaf-1",
		CandidateSessionBytes: 1024,
		CandidateEntryCount:   12,
		FrozenFinalText:       "authoritative final answer",
		FinalClientMsgID:      "agent:run-prepare:final",
		UsageState:            model.AgentUsageStateExact,
		InputTokens:           10,
		OutputTokens:          20,
		TotalTokens:           30,
		CostMicros:            250_000,
		Cost:                  0.25,
	})
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusReadyToCommit, prepared.Status)
	require.Equal(t, "authoritative final answer", prepared.FrozenFinalText)
	require.Zero(t, prepared.BaseSessionGeneration)

	_, err = runRepo.FailAgentRun(ctx, AgentRunFailure{
		Lease:        leaseFor(prepared, base.Add(5*time.Second)),
		ErrorCode:    "must_not_rerun",
		ErrorSummary: "prepared output cannot return to inference",
		Retryable:    true,
		RetryAt:      base.Add(6 * time.Second),
	})
	require.ErrorIs(t, err, ErrAgentRunFenceLost)

	recovery, err := runRepo.RecoverExpiredAgentRuns(ctx, "tenant-a", base.Add(2*time.Minute))
	require.NoError(t, err)
	require.Zero(t, recovery.RetryableExecutions)
	require.Equal(t, int64(1), recovery.PreparedRuns)

	stillPrepared, err := runRepo.GetAgentRun(ctx, "tenant-a", run.RunID)
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusReadyToCommit, stillPrepared.Status)
	require.Equal(t, prepared.CandidateChecksum, stillPrepared.CandidateChecksum)
	require.Equal(t, prepared.FrozenFinalText, stillPrepared.FrozenFinalText)

	_, err = runRepo.ClaimNextAgentRun(ctx, testClaim("tenant-a", "worker-model-2", "token-model-2", base.Add(2*time.Minute)))
	require.ErrorIs(t, err, ErrNoAgentRunAvailable)

	committing, err := runRepo.ClaimPreparedAgentRun(ctx, testClaim("tenant-a", "worker-commit", "token-commit", base.Add(2*time.Minute)))
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusCommitting, committing.Status)
	require.Equal(t, prepared.FrozenFinalText, committing.FrozenFinalText)

	messageResult := AgentRunFinalMessage{
		Lease:       leaseFor(committing, base.Add(2*time.Minute+time.Second)),
		EventID:     9001,
		SeqID:       42,
		TimestampMs: 1786150000000,
	}
	recorded, err := runRepo.RecordAgentRunFinalMessage(ctx, messageResult)
	require.NoError(t, err)
	require.Equal(t, int64(9001), recorded.FinalEventID)

	// Logic 响应送达而 Repo 响应丢失时，同一 ACK 可以安全重放。
	replayed, err := runRepo.RecordAgentRunFinalMessage(ctx, messageResult)
	require.NoError(t, err)
	require.Equal(t, recorded.Version, replayed.Version)

	conflictingMessage := messageResult
	conflictingMessage.EventID++
	_, err = runRepo.RecordAgentRunFinalMessage(ctx, conflictingMessage)
	require.ErrorIs(t, err, ErrAgentRunCommitConflict)

	completed, err := runRepo.CompleteAgentRun(ctx, AgentRunCompletion{
		Lease:               leaseFor(recorded, base.Add(2*time.Minute+2*time.Second)),
		CommittedGeneration: 1,
	})
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusSucceeded, completed.Status)
	require.Equal(t, int64(1), completed.CommittedGeneration)
	require.NotNil(t, completed.CompletedAt)
	binding, err := runRepo.GetAgentSessionBinding(ctx, "tenant-a", run.ConversationID)
	require.NoError(t, err)
	require.Equal(t, int64(1), binding.Generation)
	require.Equal(t, prepared.CandidateSessionRef, binding.SessionRef)
	require.Equal(t, prepared.CandidateChecksum, binding.Checksum)
	require.Equal(t, prepared.CandidateSessionBytes, binding.SessionBytes)
	require.Equal(t, prepared.CandidateEntryCount, binding.EntryCount)
	require.Equal(t, model.AgentSessionBindingStatusActive, binding.Status)

	idempotentCompletion, err := runRepo.CompleteAgentRun(ctx, AgentRunCompletion{
		Lease:               messageResult.Lease,
		CommittedGeneration: 1,
	})
	require.NoError(t, err)
	require.Equal(t, completed.Version, idempotentCompletion.Version)
}

func TestAgentRunRepo_ExpiredFinalAttemptBecomesTerminal(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	base := time.Date(2026, 8, 8, 6, 0, 0, 0, time.UTC)
	run := newTestAgentRun("run-final-attempt", "tenant-a", "conversation-a", 601, 1, base)
	run.MaxAttempts = 1
	_, err = runRepo.EnqueueAgentRun(ctx, run)
	require.NoError(t, err)
	_, err = runRepo.ClaimNextAgentRun(ctx, testClaim("tenant-a", "worker-1", "token-1", base.Add(time.Second)))
	require.NoError(t, err)
	otherTenant := newTestAgentRun("run-other-tenant", "tenant-b", "conversation-a", 601, 1, base)
	otherTenant.MaxAttempts = 1
	_, err = runRepo.EnqueueAgentRun(ctx, otherTenant)
	require.NoError(t, err)
	_, err = runRepo.ClaimNextAgentRun(ctx, testClaim("tenant-b", "worker-2", "token-2", base.Add(time.Second)))
	require.NoError(t, err)

	recovery, err := runRepo.RecoverExpiredAgentRuns(ctx, "tenant-a", base.Add(2*time.Minute))
	require.NoError(t, err)
	require.Equal(t, int64(1), recovery.FinalFailures)

	terminal, err := runRepo.GetAgentRun(ctx, "tenant-a", run.RunID)
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusFailedFinal, terminal.Status)
	require.Equal(t, "lease_expired", terminal.LastErrorCode)
	require.False(t, terminal.LastErrorRetryable)
	require.NotNil(t, terminal.CompletedAt)
	untouched, err := runRepo.GetAgentRun(ctx, "tenant-b", otherTenant.RunID)
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusClaimed, untouched.Status)
}

func TestAgentRunRepo_SessionBindingAdvancesByCASAndCanBeMarkedDirty(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	base := time.Date(2026, 8, 8, 6, 30, 0, 0, time.UTC)

	initial := &model.AgentSessionBinding{
		TenantID:             "tenant-a",
		ConversationID:       "conversation-a",
		RuntimeKind:          "pi",
		RuntimeVersion:       "0.50.1",
		BridgeVersion:        "1.0.0",
		RuntimeSessionID:     "pi-session-3",
		SessionRef:           "sessions/tenant-a/conversation-a/3.jsonl",
		Checksum:             testDigest("session-3"),
		ProfileID:            "user-assistant",
		ProfileVersion:       1,
		Generation:           3,
		LastCommittedEntryID: "leaf-3",
		Status:               model.AgentSessionBindingStatusActive,
		Version:              1,
	}
	require.NoError(t, database.DB(ctx).Create(initial).Error)

	prepared := prepareTestAgentRun(t, ctx, runRepo, newTestAgentRun(
		"run-binding-4", "tenant-a", "conversation-a", 651, 1, base,
	), 3, "session-4", base)
	committing, err := runRepo.BeginAgentRunCommit(ctx, leaseFor(prepared, base.Add(5*time.Second)))
	require.NoError(t, err)
	recorded, err := runRepo.RecordAgentRunFinalMessage(ctx, AgentRunFinalMessage{
		Lease:       leaseFor(committing, base.Add(6*time.Second)),
		EventID:     9100,
		SeqID:       44,
		TimestampMs: 1786151000000,
	})
	require.NoError(t, err)
	_, err = runRepo.CompleteAgentRun(ctx, AgentRunCompletion{
		Lease:               leaseFor(recorded, base.Add(7*time.Second)),
		CommittedGeneration: 4,
	})
	require.NoError(t, err)

	binding, err := runRepo.GetAgentSessionBinding(ctx, "tenant-a", "conversation-a")
	require.NoError(t, err)
	require.Equal(t, int64(4), binding.Generation)
	require.Equal(t, "session-4", binding.RuntimeSessionID)
	require.Equal(t, model.AgentSessionBindingStatusActive, binding.Status)

	dirty, err := runRepo.MarkAgentSessionBindingDirty(ctx, "tenant-a", "conversation-a", 4, base.Add(8*time.Second))
	require.NoError(t, err)
	require.Equal(t, model.AgentSessionBindingStatusDirty, dirty.Status)
	replayedDirty, err := runRepo.MarkAgentSessionBindingDirty(ctx, "tenant-a", "conversation-a", 4, base.Add(9*time.Second))
	require.NoError(t, err)
	require.Equal(t, dirty.Version, replayedDirty.Version)
	_, err = runRepo.MarkAgentSessionBindingDirty(ctx, "tenant-a", "conversation-a", 3, base.Add(9*time.Second))
	require.ErrorIs(t, err, ErrAgentSessionBindingConflict)
	_, err = runRepo.GetAgentSessionBinding(ctx, "tenant-b", "conversation-a")
	require.ErrorIs(t, err, ErrAgentSessionBindingNotFound)

	// 一个基于旧 generation 准备的候选不能覆盖已经提交的 binding。
	stalePrepared := prepareTestAgentRun(t, ctx, runRepo, newTestAgentRun(
		"run-binding-stale", "tenant-a", "conversation-a", 652, 2, base.Add(10*time.Second),
	), 3, "stale-session-4", base.Add(10*time.Second))
	staleCommit, err := runRepo.BeginAgentRunCommit(ctx, leaseFor(stalePrepared, base.Add(15*time.Second)))
	require.NoError(t, err)
	staleRecorded, err := runRepo.RecordAgentRunFinalMessage(ctx, AgentRunFinalMessage{
		Lease:       leaseFor(staleCommit, base.Add(16*time.Second)),
		EventID:     9101,
		SeqID:       45,
		TimestampMs: 1786151001000,
	})
	require.NoError(t, err)
	_, err = runRepo.CompleteAgentRun(ctx, AgentRunCompletion{
		Lease:               leaseFor(staleRecorded, base.Add(17*time.Second)),
		CommittedGeneration: 4,
	})
	require.ErrorIs(t, err, ErrAgentSessionBindingConflict)
	unchanged, err := runRepo.GetAgentSessionBinding(ctx, "tenant-a", "conversation-a")
	require.NoError(t, err)
	require.Equal(t, "session-4", unchanged.RuntimeSessionID)
	stuckForDiagnosis, err := runRepo.GetAgentRun(ctx, "tenant-a", "run-binding-stale")
	require.NoError(t, err)
	require.Equal(t, model.AgentRunStatusCommitting, stuckForDiagnosis.Status)
}

func TestAgentRunSchema_EnforcesPartialActiveConversationUniqueness(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	base := time.Date(2026, 8, 8, 7, 0, 0, 0, time.UTC)
	first := newTestAgentRun("run-index-1", "tenant-a", "conversation-a", 701, 1, base)
	second := newTestAgentRun("run-index-2", "tenant-a", "conversation-a", 702, 2, base.Add(time.Second))
	_, err = runRepo.EnqueueAgentRun(ctx, first)
	require.NoError(t, err)
	_, err = runRepo.EnqueueAgentRun(ctx, second)
	require.NoError(t, err)
	_, err = runRepo.ClaimNextAgentRun(ctx, testClaim("tenant-a", "worker-1", "token-1", base.Add(2*time.Second)))
	require.NoError(t, err)

	update := database.DB(ctx).Model(&model.AgentRun{}).
		Where("tenant_id = ? AND run_id = ?", second.TenantID, second.RunID).
		Update("status", model.AgentRunStatusClaimed)
	require.Error(t, update.Error)

	type indexRow struct {
		IndexDef string `gorm:"column:indexdef"`
	}
	var index indexRow
	err = database.DB(ctx).Raw(`
		SELECT indexdef
		FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND tablename = 't_agent_run'
		  AND indexname = 'uniq_agent_run_active_conversation'
	`).Take(&index).Error
	require.NoError(t, err)
	require.Contains(t, index.IndexDef, "UNIQUE INDEX")
	require.Contains(t, index.IndexDef, "WHERE")
	require.Contains(t, index.IndexDef, "READY_TO_COMMIT")
}

func TestAgentRunRepo_SessionGCLockSnapshotsAllTenantReferencesAndSerializesCollectors(t *testing.T) {
	database, cleanup := setupTestContext(t)
	defer cleanup()

	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	liveBindingRef := "objects/aa/" + testDigest("binding") + ".jsonl"
	liveCandidateRef := "objects/bb/" + testDigest("candidate") + ".jsonl"
	terminalOrphanRef := "objects/cc/" + testDigest("orphan") + ".jsonl"
	require.NoError(t, database.DB(ctx).Create(&model.AgentSessionBinding{
		TenantID: "tenant-b", ConversationID: "conversation-gc", RuntimeKind: "pi", RuntimeVersion: "0.84.1",
		BridgeVersion: "1.0.0", RuntimeSessionID: "session-gc", SessionRef: liveBindingRef,
		Checksum: testDigest("binding"), ProfileID: "user-assistant", ProfileVersion: 1,
		Generation: 1, LastCommittedEntryID: "leaf-gc", Status: model.AgentSessionBindingStatusActive, Version: 1,
	}).Error)
	base := time.Date(2026, 8, 9, 13, 0, 0, 0, time.UTC)
	for _, candidate := range []struct {
		id     string
		status string
		ref    string
	}{
		{"run-gc-live", model.AgentRunStatusReadyToCommit, liveCandidateRef},
		{"run-gc-terminal", model.AgentRunStatusFailedFinal, terminalOrphanRef},
	} {
		run := newTestAgentRun(candidate.id, "tenant-a", candidate.id+"-conversation", int64(800+len(candidate.id)), 1, base)
		run.Status = candidate.status
		run.CandidateSessionRef = candidate.ref
		run.CandidateSessionID = candidate.id + "-session"
		run.CandidateChecksum = testDigest(candidate.id)
		run.CandidateLeafEntryID = candidate.id + "-leaf"
		run.FrozenFinalText = "frozen"
		run.FinalClientMsgID = "agent:" + candidate.id + ":final"
		require.NoError(t, database.DB(ctx).Create(run).Error)
	}

	entered := make(chan []string, 1)
	release := make(chan struct{})
	type lockResult struct {
		acquired bool
		err      error
	}
	firstDone := make(chan lockResult, 1)
	go func() {
		acquired, lockErr := runRepo.WithAgentSessionGCLock(ctx, "session-gc:test-root", func(_ context.Context, references []string) error {
			entered <- append([]string(nil), references...)
			<-release
			return nil
		})
		firstDone <- lockResult{acquired: acquired, err: lockErr}
	}()

	references := <-entered
	require.Equal(t, []string{liveBindingRef, liveCandidateRef}, references)
	secondCallbackCalled := false
	secondAcquired, err := runRepo.WithAgentSessionGCLock(ctx, "session-gc:test-root", func(context.Context, []string) error {
		secondCallbackCalled = true
		return nil
	})
	require.NoError(t, err)
	require.False(t, secondAcquired)
	require.False(t, secondCallbackCalled)

	close(release)
	first := <-firstDone
	require.NoError(t, first.err)
	require.True(t, first.acquired)

	thirdAcquired, err := runRepo.WithAgentSessionGCLock(ctx, "session-gc:test-root", func(context.Context, []string) error { return nil })
	require.NoError(t, err)
	require.True(t, thirdAcquired)
}

func newTestAgentRun(runID, tenantID, conversationID string, eventID, seqID int64, queuedAt time.Time) *model.AgentRun {
	prompt := fmt.Sprintf("prompt for event %d", eventID)
	return &model.AgentRun{
		RunID:             runID,
		TenantID:          tenantID,
		ConversationID:    conversationID,
		SourceEventID:     eventID,
		SourceSeqID:       seqID,
		SourceTimestampMs: queuedAt.UnixMilli(),
		SourceHash:        testDigest(prompt),
		Prompt:            prompt,
		TraceContext:      []byte(`{"traceparent":"00-test"}`),
		ActorID:           "user-1",
		ActorUsername:     "user-1",
		ProfileID:         "user-assistant",
		ProfileVersion:    1,
		RuntimeKind:       "pi",
		RuntimeVersion:    "0.50.1",
		BridgeVersion:     "1.0.0",
		ModelProvider:     "anthropic",
		ModelID:           "claude-sonnet-4-5",
		MaxAttempts:       3,
		QueuedAt:          queuedAt,
		AvailableAt:       queuedAt,
	}
}

func testClaim(tenantID, workerID, token string, now time.Time) AgentRunClaim {
	return AgentRunClaim{
		TenantID:       tenantID,
		ProfileID:      "user-assistant",
		ProfileVersion: 1,
		WorkerID:       workerID,
		LeaseToken:     token,
		Now:            now,
		LeaseDuration:  time.Minute,
	}
}

func leaseFor(run *model.AgentRun, now time.Time) AgentRunLease {
	return AgentRunLease{
		TenantID:        run.TenantID,
		RunID:           run.RunID,
		WorkerID:        run.LeaseOwner,
		LeaseToken:      run.LeaseToken,
		ExpectedVersion: run.Version,
		Now:             now,
		LeaseDuration:   time.Minute,
	}
}

func prepareTestAgentRun(
	t *testing.T,
	ctx context.Context,
	runRepo AgentRunRepo,
	run *model.AgentRun,
	baseGeneration int64,
	candidateSessionID string,
	base time.Time,
) *model.AgentRun {
	t.Helper()
	_, err := runRepo.EnqueueAgentRun(ctx, run)
	require.NoError(t, err)
	claimed, err := runRepo.ClaimNextAgentRun(ctx, testClaim(run.TenantID, "worker-prepare", "token-"+run.RunID, base.Add(time.Second)))
	require.NoError(t, err)
	starting, err := runRepo.AdvanceAgentRun(ctx, AgentRunTransition{
		Lease: leaseFor(claimed, base.Add(2*time.Second)),
		From:  model.AgentRunStatusClaimed,
		To:    model.AgentRunStatusStartingRuntime,
	})
	require.NoError(t, err)
	running, err := runRepo.AdvanceAgentRun(ctx, AgentRunTransition{
		Lease: leaseFor(starting, base.Add(3*time.Second)),
		From:  model.AgentRunStatusStartingRuntime,
		To:    model.AgentRunStatusRunning,
	})
	require.NoError(t, err)
	prepared, err := runRepo.PrepareAgentRun(ctx, AgentRunPreparedResult{
		Lease:                 leaseFor(running, base.Add(4*time.Second)),
		BaseSessionGeneration: baseGeneration,
		CandidateSessionID:    candidateSessionID,
		CandidateSessionRef:   "sessions/" + run.TenantID + "/" + run.ConversationID + "/" + candidateSessionID + ".jsonl",
		CandidateChecksum:     testDigest(candidateSessionID),
		CandidateLeafEntryID:  "leaf-" + candidateSessionID,
		CandidateSessionBytes: 1024,
		CandidateEntryCount:   12,
		FrozenFinalText:       "final answer for " + run.RunID,
		FinalClientMsgID:      "agent:" + run.RunID + ":final",
		UsageState:            model.AgentUsageStateExact,
		InputTokens:           10,
		OutputTokens:          20,
		TotalTokens:           30,
		CostMicros:            250_000,
	})
	require.NoError(t, err)
	return prepared
}

func testDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
