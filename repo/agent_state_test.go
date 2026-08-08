package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/model"
)

func TestAgentApprovalRepo_CreateConcurrentIdempotentAndConflict(t *testing.T) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)
	repository, err := NewAgentApprovalRepo(database)
	require.NoError(t, err)

	ctx := context.Background()
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	const workers = 12
	var created atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, createErr := repository.CreateAgentApproval(ctx, newTestAgentApproval("tenant-a", "run-1", "call-1", testHash("a"), expiresAt))
			if createErr == nil {
				if result.Created {
					created.Add(1)
				}
				require.Equal(t, model.AgentApprovalStatusPending, result.Approval.Status)
			}
			errs <- createErr
		}()
	}
	wg.Wait()
	close(errs)
	for createErr := range errs {
		require.NoError(t, createErr)
	}
	require.Equal(t, int64(1), created.Load())

	conflict := newTestAgentApproval("tenant-a", "run-1", "call-1", testHash("b"), expiresAt)
	_, err = repository.CreateAgentApproval(ctx, conflict)
	require.ErrorIs(t, err, ErrAgentStateConflict)

	otherTenant, err := repository.CreateAgentApproval(ctx, newTestAgentApproval("tenant-b", "run-1", "call-1", testHash("b"), expiresAt))
	require.NoError(t, err)
	require.True(t, otherTenant.Created)
}

func TestAgentApprovalRepo_StrictTransitionsAndExpiry(t *testing.T) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)
	repository, err := NewAgentApprovalRepo(database)
	require.NoError(t, err)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	hash := testHash("c")
	_, err = repository.CreateAgentApproval(ctx, newTestAgentApproval("tenant-a", "run-1", "call-approve", hash, now.Add(time.Hour)))
	require.NoError(t, err)

	approve := AgentApprovalTransition{
		TenantID:        "tenant-a",
		CallID:          "call-approve",
		ArgsHash:        hash,
		ExpectedStatus:  model.AgentApprovalStatusPending,
		ExpectedVersion: 1,
		NextStatus:      model.AgentApprovalStatusApproved,
		ActorID:         "admin-1",
		Reason:          "reviewed",
		OccurredAt:      now,
	}
	approved, err := repository.TransitionAgentApproval(ctx, approve)
	require.NoError(t, err)
	require.True(t, approved.Changed)
	require.Equal(t, model.AgentApprovalDecisionApprove, approved.Approval.Decision)
	require.Equal(t, int64(2), approved.Approval.Version)

	retry, err := repository.TransitionAgentApproval(ctx, approve)
	require.NoError(t, err)
	require.False(t, retry.Changed)
	require.Equal(t, approved.Approval.ID, retry.Approval.ID)

	replacedDecision := approve
	replacedDecision.ActorID = "admin-2"
	_, err = repository.TransitionAgentApproval(ctx, replacedDecision)
	require.ErrorIs(t, err, ErrAgentStateConflict)

	revoke := AgentApprovalTransition{
		TenantID:        "tenant-a",
		CallID:          "call-approve",
		ArgsHash:        hash,
		ExpectedStatus:  model.AgentApprovalStatusApproved,
		ExpectedVersion: 2,
		NextStatus:      model.AgentApprovalStatusRevoked,
		ActorID:         "admin-1",
		Reason:          "request withdrawn",
		OccurredAt:      now.Add(time.Minute),
	}
	revoked, err := repository.TransitionAgentApproval(ctx, revoke)
	require.NoError(t, err)
	require.True(t, revoked.Changed)
	require.Equal(t, model.AgentApprovalDecisionApprove, revoked.Approval.Decision, "撤销不能覆盖原始批准决定")

	_, err = repository.TransitionAgentApproval(ctx, AgentApprovalTransition{
		TenantID:        "tenant-a",
		CallID:          "call-approve",
		ArgsHash:        hash,
		ExpectedStatus:  model.AgentApprovalStatusRevoked,
		ExpectedVersion: 3,
		NextStatus:      model.AgentApprovalStatusApproved,
		ActorID:         "admin-1",
		OccurredAt:      now.Add(2 * time.Minute),
	})
	require.ErrorIs(t, err, ErrAgentInvalidTransition)

	expiredAt := now.Add(-time.Minute)
	_, err = repository.CreateAgentApproval(ctx, newTestAgentApproval("tenant-a", "run-1", "call-expired", hash, expiredAt))
	require.NoError(t, err)
	_, err = repository.TransitionAgentApproval(ctx, AgentApprovalTransition{
		TenantID:        "tenant-a",
		CallID:          "call-expired",
		ArgsHash:        hash,
		ExpectedStatus:  model.AgentApprovalStatusPending,
		ExpectedVersion: 1,
		NextStatus:      model.AgentApprovalStatusApproved,
		ActorID:         "admin-1",
		OccurredAt:      now,
	})
	require.ErrorIs(t, err, ErrAgentApprovalExpired)

	expired, err := repository.TransitionAgentApproval(ctx, AgentApprovalTransition{
		TenantID:        "tenant-a",
		CallID:          "call-expired",
		ArgsHash:        hash,
		ExpectedStatus:  model.AgentApprovalStatusPending,
		ExpectedVersion: 1,
		NextStatus:      model.AgentApprovalStatusExpired,
		OccurredAt:      now,
	})
	require.NoError(t, err)
	require.Equal(t, model.AgentApprovalStatusExpired, expired.Approval.Status)
}

func TestAgentApprovalRepo_CreateAndDecideWithOutboxAreAtomicAndIdempotent(t *testing.T) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)
	repository, err := NewAgentApprovalRepo(database)
	require.NoError(t, err)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	argsHash := testHash("f")
	approval := newTestAgentApproval("tenant-a", "run-outbox", "call-outbox", argsHash, now.Add(time.Hour))
	requestedOutbox := newTestApprovalOutbox(7001, "resonance.agent.approval.requested.v1", now)
	created, err := repository.CreateAgentApprovalWithOutbox(ctx, approval, requestedOutbox)
	require.NoError(t, err)
	require.True(t, created.Created)
	require.NotNil(t, created.Outbox)

	retry, err := repository.CreateAgentApprovalWithOutbox(ctx, approval, newTestApprovalOutbox(7002, "resonance.agent.approval.requested.v1", now))
	require.NoError(t, err)
	require.False(t, retry.Created)
	require.Nil(t, retry.Outbox, "幂等重投不能追加第二条 requested outbox")

	transition := AgentApprovalTransition{
		TenantID:        "tenant-a",
		CallID:          "call-outbox",
		ArgsHash:        argsHash,
		ExpectedStatus:  model.AgentApprovalStatusPending,
		ExpectedVersion: 1,
		NextStatus:      model.AgentApprovalStatusApproved,
		ActorID:         "admin-1",
		Reason:          "reviewed",
		OccurredAt:      now.Add(time.Minute),
	}
	decided, err := repository.TransitionAgentApprovalWithOutbox(
		ctx,
		transition,
		newTestApprovalOutbox(7003, "resonance.agent.approval.decided.v1", now.Add(time.Minute)),
	)
	require.NoError(t, err)
	require.True(t, decided.Changed)
	require.NotNil(t, decided.Outbox)
	require.Equal(t, int64(2), decided.Approval.Version)

	retryTransition := transition
	retryTransition.OccurredAt = now.Add(2 * time.Minute)
	decideRetry, err := repository.TransitionAgentApprovalWithOutbox(
		ctx,
		retryTransition,
		newTestApprovalOutbox(7004, "resonance.agent.approval.decided.v1", now.Add(2*time.Minute)),
	)
	require.NoError(t, err)
	require.False(t, decideRetry.Changed)
	require.Nil(t, decideRetry.Outbox, "幂等决定重投不能追加第二条 decided outbox")
	require.NotNil(t, decideRetry.Approval.DecidedAt)
	require.True(t, decideRetry.Approval.DecidedAt.Equal(transition.OccurredAt), "重投不能覆盖权威决定时间")

	var outboxCount int64
	require.NoError(t, database.DB(ctx).Model(&model.MessageOutbox{}).Count(&outboxCount).Error)
	require.Equal(t, int64(2), outboxCount)
}

func TestAgentApprovalRepo_OutboxFailureRollsBackFactAndExpectedVersionFences(t *testing.T) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)
	repository, err := NewAgentApprovalRepo(database)
	require.NoError(t, err)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	argsHash := testHash("1")
	approval := newTestAgentApproval("tenant-a", "run-rollback", "call-rollback", argsHash, now.Add(time.Hour))
	_, err = repository.CreateAgentApprovalWithOutbox(ctx, approval, &model.MessageOutbox{
		EventID:       7101,
		Topic:         "resonance.agent.approval.requested.v1",
		NextRetryTime: now,
	})
	require.Error(t, err)
	_, err = repository.GetAgentApproval(ctx, "tenant-a", "call-rollback")
	require.ErrorIs(t, err, ErrAgentApprovalNotFound)

	created, err := repository.CreateAgentApprovalWithOutbox(
		ctx,
		approval,
		newTestApprovalOutbox(7102, "resonance.agent.approval.requested.v1", now),
	)
	require.NoError(t, err)
	require.True(t, created.Created)
	_, err = repository.TransitionAgentApprovalWithOutbox(ctx, AgentApprovalTransition{
		TenantID:        "tenant-a",
		CallID:          "call-rollback",
		ArgsHash:        argsHash,
		ExpectedStatus:  model.AgentApprovalStatusPending,
		ExpectedVersion: 2,
		NextStatus:      model.AgentApprovalStatusRejected,
		ActorID:         "admin-1",
		OccurredAt:      now.Add(time.Minute),
	}, newTestApprovalOutbox(7103, "resonance.agent.approval.decided.v1", now.Add(time.Minute)))
	require.ErrorIs(t, err, ErrAgentStateConflict)

	persisted, err := repository.GetAgentApproval(ctx, "tenant-a", "call-rollback")
	require.NoError(t, err)
	require.Equal(t, model.AgentApprovalStatusPending, persisted.Status)
	var outboxCount int64
	require.NoError(t, database.DB(ctx).Model(&model.MessageOutbox{}).Count(&outboxCount).Error)
	require.Equal(t, int64(1), outboxCount)
}

func TestAgentApprovalRepo_ListIsTenantScopedAndCursorOrdered(t *testing.T) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)
	repository, err := NewAgentApprovalRepo(database)
	require.NoError(t, err)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, callID := range []string{"call-list-1", "call-list-2", "call-list-3"} {
		_, err = repository.CreateAgentApproval(ctx, newTestAgentApproval("tenant-a", "run-list", callID, testHash("2"), now.Add(time.Hour)))
		require.NoError(t, err)
	}
	_, err = repository.CreateAgentApproval(ctx, newTestAgentApproval("tenant-b", "run-list", "call-list-other", testHash("3"), now.Add(time.Hour)))
	require.NoError(t, err)

	first, err := repository.ListAgentApprovals(ctx, AgentApprovalListFilter{TenantID: "tenant-a", Limit: 2})
	require.NoError(t, err)
	require.Len(t, first, 2)
	require.Greater(t, first[0].ID, first[1].ID)
	second, err := repository.ListAgentApprovals(ctx, AgentApprovalListFilter{TenantID: "tenant-a", BeforeID: first[1].ID, Limit: 2})
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, "tenant-a", second[0].TenantID)
}

func TestAgentToolExecutionRepo_IdempotencyAndStateMachine(t *testing.T) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)
	repository, err := NewAgentToolExecutionRepo(database)
	require.NoError(t, err)

	ctx := context.Background()
	hash := testHash("d")
	created, err := repository.CreateAgentToolExecution(ctx, newTestAgentExecution("tenant-a", "run-1", "call-1", "idem-1", hash))
	require.NoError(t, err)
	require.True(t, created.Created)
	require.Equal(t, model.AgentToolExecutionStatusPrepared, created.Execution.Status)

	retry, err := repository.CreateAgentToolExecution(ctx, newTestAgentExecution("tenant-a", "run-1", "call-1", "idem-1", hash))
	require.NoError(t, err)
	require.False(t, retry.Created)
	require.Equal(t, created.Execution.ID, retry.Execution.ID)

	_, err = repository.CreateAgentToolExecution(ctx, newTestAgentExecution("tenant-a", "run-1", "call-2", "idem-1", hash))
	require.ErrorIs(t, err, ErrAgentStateConflict, "执行幂等键不能绑定到另一 call_id")

	now := time.Now().UTC().Truncate(time.Microsecond)
	ready := transitionTestExecution(t, repository, AgentToolExecutionTransition{
		TenantID:        "tenant-a",
		CallID:          "call-1",
		ArgsHash:        hash,
		ExpectedStatus:  model.AgentToolExecutionStatusPrepared,
		NextStatus:      model.AgentToolExecutionStatusReady,
		ApprovalVersion: 2,
		OccurredAt:      now,
	})
	require.Equal(t, int64(2), ready.Execution.Version)

	readyRetry := transitionTestExecution(t, repository, AgentToolExecutionTransition{
		TenantID:        "tenant-a",
		CallID:          "call-1",
		ArgsHash:        hash,
		ExpectedStatus:  model.AgentToolExecutionStatusPrepared,
		NextStatus:      model.AgentToolExecutionStatusReady,
		ApprovalVersion: 2,
		OccurredAt:      now,
	})
	require.False(t, readyRetry.Changed)

	executing := transitionTestExecution(t, repository, AgentToolExecutionTransition{
		TenantID:       "tenant-a",
		CallID:         "call-1",
		ArgsHash:       hash,
		ExpectedStatus: model.AgentToolExecutionStatusReady,
		NextStatus:     model.AgentToolExecutionStatusExecuting,
		OccurredAt:     now.Add(time.Second),
	})
	require.Equal(t, 1, executing.Execution.Attempt)

	failed := transitionTestExecution(t, repository, AgentToolExecutionTransition{
		TenantID:       "tenant-a",
		CallID:         "call-1",
		ArgsHash:       hash,
		ExpectedStatus: model.AgentToolExecutionStatusExecuting,
		NextStatus:     model.AgentToolExecutionStatusFailedRetryable,
		OccurredAt:     now.Add(2 * time.Second),
		ErrorCode:      "downstream_unavailable",
		ErrorSummary:   "temporary failure",
	})
	require.Equal(t, model.AgentToolExecutionStatusFailedRetryable, failed.Execution.Status)

	executing = transitionTestExecution(t, repository, AgentToolExecutionTransition{
		TenantID:       "tenant-a",
		CallID:         "call-1",
		ArgsHash:       hash,
		ExpectedStatus: model.AgentToolExecutionStatusFailedRetryable,
		NextStatus:     model.AgentToolExecutionStatusExecuting,
		OccurredAt:     now.Add(3 * time.Second),
	})
	require.Equal(t, 2, executing.Execution.Attempt)

	resultHash := testHash("e")
	succeeded := transitionTestExecution(t, repository, AgentToolExecutionTransition{
		TenantID:              "tenant-a",
		CallID:                "call-1",
		ArgsHash:              hash,
		ExpectedStatus:        model.AgentToolExecutionStatusExecuting,
		NextStatus:            model.AgentToolExecutionStatusSucceeded,
		OccurredAt:            now.Add(4 * time.Second),
		ResultRef:             "object://tool-results/result-1",
		ResultSummary:         "updated one object",
		ResultHash:            resultHash,
		DownstreamOperationID: "operation-1",
	})
	require.Equal(t, model.AgentToolExecutionStatusSucceeded, succeeded.Execution.Status)
	require.Equal(t, "operation-1", succeeded.Execution.DownstreamOperationID)

	successRetry := transitionTestExecution(t, repository, AgentToolExecutionTransition{
		TenantID:              "tenant-a",
		CallID:                "call-1",
		ArgsHash:              hash,
		ExpectedStatus:        model.AgentToolExecutionStatusExecuting,
		NextStatus:            model.AgentToolExecutionStatusSucceeded,
		OccurredAt:            now.Add(4 * time.Second),
		ResultRef:             "object://tool-results/result-1",
		ResultSummary:         "updated one object",
		ResultHash:            resultHash,
		DownstreamOperationID: "operation-1",
	})
	require.False(t, successRetry.Changed)

	_, err = repository.TransitionAgentToolExecution(ctx, AgentToolExecutionTransition{
		TenantID:       "tenant-a",
		CallID:         "call-1",
		ArgsHash:       hash,
		ExpectedStatus: model.AgentToolExecutionStatusSucceeded,
		NextStatus:     model.AgentToolExecutionStatusExecuting,
		OccurredAt:     now.Add(5 * time.Second),
	})
	require.ErrorIs(t, err, ErrAgentInvalidTransition)
}

func TestAgentAuditRepo_ConcurrentAppendIdempotencyAndTamperDetection(t *testing.T) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)
	repository, err := NewAgentAuditRepo(database)
	require.NoError(t, err)

	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Microsecond)
	const count = 16
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := range count {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, appendErr := repository.AppendAgentAudit(ctx, &model.AgentAuditLog{
				TenantID:   "tenant-a",
				AuditID:    fmt.Sprintf("audit-%02d", index),
				RunID:      "run-1",
				CallID:     "call-1",
				EventType:  "tool.prepared",
				ActorType:  "service",
				ActorID:    "pilot",
				Summary:    fmt.Sprintf("prepared call %d", index),
				DetailRef:  fmt.Sprintf("object://audit/%d", index),
				OccurredAt: base.Add(time.Duration(index) * time.Microsecond),
			})
			errs <- appendErr
		}(i)
	}
	wg.Wait()
	close(errs)
	for appendErr := range errs {
		require.NoError(t, appendErr)
	}
	require.NoError(t, repository.VerifyAgentAuditChain(ctx, "tenant-a", "run-1"))

	first, err := repository.GetAgentAudit(ctx, "tenant-a", "audit-00")
	require.NoError(t, err)
	retry, err := repository.AppendAgentAudit(ctx, &model.AgentAuditLog{
		TenantID:   first.TenantID,
		AuditID:    first.AuditID,
		RunID:      first.RunID,
		CallID:     first.CallID,
		EventType:  first.EventType,
		ActorType:  first.ActorType,
		ActorID:    first.ActorID,
		Summary:    first.Summary,
		DetailRef:  first.DetailRef,
		OccurredAt: first.OccurredAt,
	})
	require.NoError(t, err)
	require.False(t, retry.Created)
	require.Equal(t, first.EntryHash, retry.Entry.EntryHash)

	_, err = repository.AppendAgentAudit(ctx, &model.AgentAuditLog{
		TenantID:   first.TenantID,
		AuditID:    first.AuditID,
		RunID:      first.RunID,
		CallID:     first.CallID,
		EventType:  first.EventType,
		ActorType:  first.ActorType,
		ActorID:    first.ActorID,
		Summary:    "replaced summary",
		DetailRef:  first.DetailRef,
		OccurredAt: first.OccurredAt,
	})
	require.ErrorIs(t, err, ErrAgentStateConflict)

	require.NoError(t, database.DB(ctx).Model(&model.AgentAuditLog{}).
		Where("tenant_id = ? AND audit_id = ?", "tenant-a", "audit-07").
		Update("summary", "tampered").Error)
	require.ErrorIs(t, repository.VerifyAgentAuditChain(ctx, "tenant-a", "run-1"), ErrAgentAuditChainBroken)
}

func newTestAgentApproval(tenantID, runID, callID, argsHash string, expiresAt time.Time) *model.AgentApproval {
	return &model.AgentApproval{
		TenantID:    tenantID,
		RunID:       runID,
		CallID:      callID,
		ToolName:    "iam.disable_user",
		RequesterID: "requester-1",
		ArgsHash:    argsHash,
		ArgsSummary: "disable one user",
		ExpiresAt:   expiresAt,
	}
}

func newTestAgentExecution(tenantID, runID, callID, idempotencyKey, argsHash string) *model.AgentToolExecution {
	return &model.AgentToolExecution{
		TenantID:          tenantID,
		RunID:             runID,
		CallID:            callID,
		RuntimeToolCallID: "runtime-" + callID,
		ToolName:          "iam.disable_user",
		ToolVersion:       "1",
		SchemaVersion:     "1",
		ArgsHash:          argsHash,
		FrozenArgsRef:     "object://frozen-args/" + callID,
		ArgsSummary:       "disable one user",
		IdempotencyKey:    idempotencyKey,
	}
}

func newTestApprovalOutbox(eventID int64, topic string, nextRetry time.Time) *model.MessageOutbox {
	return &model.MessageOutbox{
		EventID:       eventID,
		Topic:         topic,
		Payload:       []byte("approval-event"),
		NextRetryTime: nextRetry,
	}
}

func transitionTestExecution(t *testing.T, repository AgentToolExecutionRepo, transition AgentToolExecutionTransition) *AgentToolExecutionTransitionResult {
	t.Helper()
	result, err := repository.TransitionAgentToolExecution(context.Background(), transition)
	require.NoError(t, err)
	return result
}

func testHash(character string) string {
	return strings.Repeat(character, 64)
}

func TestAgentStateValidationRejectsMalformedHashes(t *testing.T) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)
	repository, err := NewAgentApprovalRepo(database)
	require.NoError(t, err)
	_, err = repository.CreateAgentApproval(context.Background(), newTestAgentApproval("tenant-a", "run-1", "call-1", "not-a-hash", time.Now().Add(time.Hour)))
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrAgentStateConflict))
}
