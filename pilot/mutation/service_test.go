package mutation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/pilot/runtime"
	"github.com/ceyewan/resonance/pilot/toolbroker"
	"github.com/ceyewan/resonance/pkg/agentmutation"
	"github.com/ceyewan/resonance/repo"
)

func TestService_CrashAfterPrepareAndLostEventAreRecoveredByReconcile(t *testing.T) {
	fixture := newMutationFixture(t)
	fixture.logic.createFailures = 1
	_, err := fixture.service.PrepareTenantMembershipStatus(context.Background(), fixture.request())
	require.Error(t, err, "审批 RPC 前后的崩溃不能回滚已冻结的 PREPARED 事实")
	require.Equal(t, model.AgentToolExecutionStatusPrepared, fixture.store.execution("tenant-a", "call-1").Status)

	// 新实例模拟重启；没有审批事件，Reconcile 必须先补建审批。
	restarted := fixture.newService(t)
	require.NoError(t, restarted.Reconcile(context.Background()))
	fixture.logic.approve("tenant-a", "call-1", "approver")
	require.NoError(t, restarted.Reconcile(context.Background()))
	require.Equal(t, model.AgentToolExecutionStatusSucceeded, fixture.store.execution("tenant-a", "call-1").Status)
	require.Equal(t, 1, fixture.logic.commits)

	completed, err := restarted.PrepareTenantMembershipStatus(context.Background(), fixture.request())
	require.NoError(t, err)
	require.Equal(t, model.AgentToolExecutionStatusSucceeded, completed.ExecutionStatus)
	require.NotEmpty(t, completed.OperationID)
	require.NotEmpty(t, completed.ExecutionSummary)
}

func TestService_ResponseLossUsesNewAttemptAndSameIdempotencyFact(t *testing.T) {
	fixture := newMutationFixture(t)
	prepared, err := fixture.service.PrepareTenantMembershipStatus(context.Background(), fixture.request())
	require.NoError(t, err)
	require.Equal(t, model.AgentApprovalStatusPending, prepared.Status)
	fixture.logic.approve("tenant-a", "call-1", "approver")
	fixture.logic.loseFirstResponse = true

	err = fixture.service.ProcessCall(context.Background(), "tenant-a", "call-1")
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.Equal(t, model.AgentToolExecutionStatusFailedRetryable, fixture.store.execution("tenant-a", "call-1").Status)
	require.Equal(t, 1, fixture.logic.commits, "响应丢失时下游事实已经提交一次")
	fixture.logic.mu.Lock()
	fixture.logic.approvals[storeKey("tenant-a", "call-1")].Status = model.AgentApprovalStatusRevoked
	fixture.logic.approvals[storeKey("tenant-a", "call-1")].Version = 3
	fixture.logic.mu.Unlock()
	fixture.principals.authorized = false

	require.NoError(t, fixture.service.ProcessCall(context.Background(), "tenant-a", "call-1"))
	require.NoError(t, fixture.service.ProcessCall(context.Background(), "tenant-a", "call-1"), "至少一次重投必须幂等")
	execution := fixture.store.execution("tenant-a", "call-1")
	require.Equal(t, model.AgentToolExecutionStatusSucceeded, execution.Status)
	require.Equal(t, 2, execution.Attempt)
	require.Equal(t, 1, fixture.logic.commits)
	require.Equal(t, 2, fixture.logic.executeCalls)
}

func TestService_DowngradeBeforeExecutionFailsClosed(t *testing.T) {
	fixture := newMutationFixture(t)
	_, err := fixture.service.PrepareTenantMembershipStatus(context.Background(), fixture.request())
	require.NoError(t, err)
	fixture.logic.approve("tenant-a", "call-1", "approver")
	fixture.principals.authorized = false

	require.NoError(t, fixture.service.ProcessCall(context.Background(), "tenant-a", "call-1"))
	require.Equal(t, model.AgentToolExecutionStatusFailedFinal, fixture.store.execution("tenant-a", "call-1").Status)
	require.Zero(t, fixture.logic.executeCalls)
}

func TestService_RejectsArgsSubstitutionCrossTenantSelfBotAndDryRunHasNoDurableSideEffects(t *testing.T) {
	fixture := newMutationFixture(t)
	request := fixture.request()
	_, err := fixture.service.PrepareTenantMembershipStatus(context.Background(), request)
	require.NoError(t, err)
	fixture.logic.approve("tenant-a", "call-1", "approver")
	fixture.store.mu.Lock()
	fixture.store.frozen[storeKey("tenant-a", agentmutation.FrozenArgsRef("tenant-a", "call-1", fixture.store.executions[storeKey("tenant-a", "call-1")].ArgsHash))].Payload = []byte(`{"domain":"substituted"}`)
	fixture.store.mu.Unlock()
	require.NoError(t, fixture.service.ProcessCall(context.Background(), "tenant-a", "call-1"))
	require.Equal(t, model.AgentToolExecutionStatusFailedFinal, fixture.store.execution("tenant-a", "call-1").Status)
	require.Zero(t, fixture.logic.executeCalls)

	request.CallID = "call-self"
	request.TargetUsername = request.RequesterID
	_, err = fixture.service.PrepareTenantMembershipStatus(context.Background(), request)
	require.ErrorIs(t, err, ErrRequesterUnauthorized)
	request.CallID = "call-bot"
	request.TargetUsername = "agent-bot"
	_, err = fixture.service.PrepareTenantMembershipStatus(context.Background(), request)
	require.ErrorIs(t, err, ErrRequesterUnauthorized)
	request.CallID = "call-dry-run"
	request.TargetUsername = "member"
	request.DryRun = true
	dryRun, err := fixture.service.PrepareTenantMembershipStatus(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, "DRY_RUN", dryRun.Status)
	require.False(t, dryRun.Created)
	require.Equal(t, 1, fixture.logic.previewCalls)
	_, err = fixture.store.GetAgentToolExecution(context.Background(), "tenant-a", "call-dry-run")
	require.ErrorIs(t, err, repo.ErrAgentToolExecutionNotFound)
	fixture.logic.mu.Lock()
	_, approvalCreated := fixture.logic.approvals[storeKey("tenant-a", "call-dry-run")]
	fixture.logic.mu.Unlock()
	require.False(t, approvalCreated)

	err = fixture.service.ProcessCall(context.Background(), "tenant-b", "call-1")
	require.ErrorIs(t, err, repo.ErrAgentToolExecutionNotFound)
}

type mutationFixture struct {
	store      *fakeMutationStore
	audits     *fakeAuditStore
	principals *fakePrincipalReader
	logic      *fakeLogicClient
	service    *Service
	now        time.Time
}

func newMutationFixture(t *testing.T) *mutationFixture {
	t.Helper()
	fixture := &mutationFixture{
		store:      &fakeMutationStore{executions: map[string]*model.AgentToolExecution{}, frozen: map[string]*model.AgentFrozenToolArgs{}},
		audits:     &fakeAuditStore{entries: map[string]*model.AgentAuditLog{}},
		principals: &fakePrincipalReader{authorized: true},
		logic:      &fakeLogicClient{approvals: map[string]*ApprovalFact{}, receipts: map[string]*MutationReceipt{}},
		now:        time.Date(2026, 8, 9, 1, 0, 0, 0, time.UTC),
	}
	fixture.service = fixture.newService(t)
	return fixture
}

func (f *mutationFixture) newService(t *testing.T) *Service {
	t.Helper()
	service, err := NewService(Config{
		TenantID: "tenant-a", AgentBotUsername: "agent-bot", ApprovalTTL: 15 * time.Minute,
		ReconcileEvery: time.Second, BatchSize: 20,
	}, f.store, f.store, f.audits, f.principals, f.logic)
	require.NoError(t, err)
	service.SetClockForTest(func() time.Time { return f.now })
	return service
}

func (f *mutationFixture) request() toolbroker.MembershipMutationPrepareRequest {
	return toolbroker.MembershipMutationPrepareRequest{
		TenantID: "tenant-a", RunID: "run-1", CallID: "call-1", RequesterID: "admin",
		TargetUsername: "member", DesiredStatus: model.TenantMembershipStatusDisabled,
		ExpectedVersion: 3, DryRun: false,
	}
}

type fakeMutationStore struct {
	mu         sync.Mutex
	executions map[string]*model.AgentToolExecution
	frozen     map[string]*model.AgentFrozenToolArgs
}

func (s *fakeMutationStore) PrepareAgentMutation(_ context.Context, frozen *model.AgentFrozenToolArgs, execution *model.AgentToolExecution) (*repo.AgentMutationPreparationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := storeKey(execution.TenantID, execution.CallID)
	if existing := s.executions[key]; existing != nil {
		return &repo.AgentMutationPreparationResult{Execution: cloneExecution(existing), Frozen: cloneFrozen(s.frozen[storeKey(frozen.TenantID, frozen.Ref)])}, nil
	}
	now := time.Date(2026, 8, 9, 1, 0, 0, 0, time.UTC)
	execution = cloneExecution(execution)
	execution.ID = int64(len(s.executions) + 1)
	execution.Status = model.AgentToolExecutionStatusPrepared
	execution.Version = 1
	execution.CreatedAt = now
	execution.UpdatedAt = now
	frozen = cloneFrozen(frozen)
	frozen.ID = int64(len(s.frozen) + 1)
	frozen.CreatedAt = now
	s.executions[key] = execution
	s.frozen[storeKey(frozen.TenantID, frozen.Ref)] = frozen
	return &repo.AgentMutationPreparationResult{Execution: cloneExecution(execution), Frozen: cloneFrozen(frozen), Created: true}, nil
}

func (s *fakeMutationStore) GetAgentFrozenToolArgs(_ context.Context, tenantID, ref string) (*model.AgentFrozenToolArgs, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	frozen := s.frozen[storeKey(tenantID, ref)]
	if frozen == nil {
		return nil, repo.ErrAgentFrozenArgsNotFound
	}
	return cloneFrozen(frozen), nil
}

func (s *fakeMutationStore) ListAgentToolExecutions(_ context.Context, filter repo.AgentToolExecutionListFilter) ([]*model.AgentToolExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wanted := map[string]bool{}
	for _, value := range filter.Statuses {
		wanted[value] = true
	}
	var result []*model.AgentToolExecution
	for _, execution := range s.executions {
		if execution.TenantID == filter.TenantID && wanted[execution.Status] {
			result = append(result, cloneExecution(execution))
		}
	}
	return result, nil
}

func (s *fakeMutationStore) GetAgentToolExecution(_ context.Context, tenantID, callID string) (*model.AgentToolExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	execution := s.executions[storeKey(tenantID, callID)]
	if execution == nil {
		return nil, repo.ErrAgentToolExecutionNotFound
	}
	return cloneExecution(execution), nil
}

func (s *fakeMutationStore) TransitionAgentToolExecution(_ context.Context, transition repo.AgentToolExecutionTransition) (*repo.AgentToolExecutionTransitionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	execution := s.executions[storeKey(transition.TenantID, transition.CallID)]
	if execution == nil {
		return nil, repo.ErrAgentToolExecutionNotFound
	}
	if execution.Status != transition.ExpectedStatus || execution.ArgsHash != transition.ArgsHash {
		return nil, repo.ErrAgentInvalidTransition
	}
	execution.Status = transition.NextStatus
	execution.Version++
	execution.UpdatedAt = transition.OccurredAt
	switch transition.NextStatus {
	case model.AgentToolExecutionStatusReady:
		execution.ReadyAt = new(transition.OccurredAt)
		execution.ApprovalVersion = transition.ApprovalVersion
	case model.AgentToolExecutionStatusExecuting:
		execution.StartedAt = new(transition.OccurredAt)
		execution.Attempt++
	case model.AgentToolExecutionStatusFailedRetryable:
		execution.LastFailedAt = new(transition.OccurredAt)
		execution.ErrorCode, execution.ErrorSummary = transition.ErrorCode, transition.ErrorSummary
	case model.AgentToolExecutionStatusFailedFinal:
		execution.LastFailedAt = new(transition.OccurredAt)
		execution.FinishedAt = new(transition.OccurredAt)
		execution.ErrorCode, execution.ErrorSummary = transition.ErrorCode, transition.ErrorSummary
	case model.AgentToolExecutionStatusSucceeded:
		execution.FinishedAt = new(transition.OccurredAt)
		execution.ResultRef, execution.ResultSummary, execution.ResultHash = transition.ResultRef, transition.ResultSummary, transition.ResultHash
		execution.DownstreamOperationID = transition.DownstreamOperationID
	}
	return &repo.AgentToolExecutionTransitionResult{Execution: cloneExecution(execution), Changed: true}, nil
}

func (s *fakeMutationStore) execution(tenantID, callID string) *model.AgentToolExecution {
	execution, _ := s.GetAgentToolExecution(context.Background(), tenantID, callID)
	return execution
}

type fakeAuditStore struct {
	mu      sync.Mutex
	entries map[string]*model.AgentAuditLog
}

func (s *fakeAuditStore) AppendAgentAudit(_ context.Context, entry *model.AgentAuditLog) (*repo.AgentAuditAppendResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := storeKey(entry.TenantID, entry.AuditID)
	if existing := s.entries[key]; existing != nil {
		return &repo.AgentAuditAppendResult{Entry: existing}, nil
	}
	copy := *entry
	s.entries[key] = &copy
	return &repo.AgentAuditAppendResult{Entry: &copy, Created: true}, nil
}

type fakePrincipalReader struct{ authorized bool }

func (r *fakePrincipalReader) ResolvePrincipal(_ context.Context, tenantID, actorID string) (runtime.ActorPrincipal, error) {
	if !r.authorized {
		return runtime.ActorPrincipal{}, errors.New("downgraded")
	}
	return runtime.ActorPrincipal{
		TenantID: tenantID, ActorID: actorID, Username: actorID,
		Roles: []string{model.SystemRoleIAMAdmin}, Scopes: []string{model.ScopeIAMUsersWrite},
	}, nil
}

type fakeLogicClient struct {
	mu                sync.Mutex
	approvals         map[string]*ApprovalFact
	receipts          map[string]*MutationReceipt
	createFailures    int
	loseFirstResponse bool
	executeCalls      int
	commits           int
	previewCalls      int
	observeExecute    func(context.Context)
}

func (c *fakeLogicClient) PreviewTenantMembershipStatus(_ context.Context, args agentmutation.MembershipStatusArgs) (*MutationPreview, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.previewCalls++
	hash, err := args.Hash()
	if err != nil {
		return nil, err
	}
	return &MutationPreview{
		TargetUsername: args.TargetUsername, CurrentStatus: model.TenantMembershipStatusActive,
		DesiredStatus: args.DesiredStatus, CurrentVersion: args.ExpectedVersion,
		WouldChange: args.DesiredStatus != model.TenantMembershipStatusActive, ArgsHash: hash,
	}, nil
}

func (c *fakeLogicClient) CreateApproval(_ context.Context, request CreateApprovalRequest) (*ApprovalFact, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.createFailures > 0 {
		c.createFailures--
		return nil, false, status.Error(codes.Unavailable, "response lost")
	}
	key := storeKey(request.TenantID, request.CallID)
	if approval := c.approvals[key]; approval != nil {
		copy := *approval
		return &copy, false, nil
	}
	approval := &ApprovalFact{
		TenantID: request.TenantID, RunID: request.RunID, CallID: request.CallID, ToolName: request.ToolName,
		RequesterID: request.RequesterID, ArgsHash: request.ArgsHash, ArgsSummary: request.ArgsSummary,
		Status: model.AgentApprovalStatusPending, Decision: model.AgentApprovalDecisionNone,
		Version: 1, ExpiresAt: request.ExpiresAt, CreatedAt: time.Date(2026, 8, 9, 1, 0, 1, 0, time.UTC),
	}
	c.approvals[key] = approval
	copy := *approval
	return &copy, true, nil
}

func (c *fakeLogicClient) GetExecutionApproval(_ context.Context, tenantID, callID, argsHash string) (*ApprovalFact, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	approval := c.approvals[storeKey(tenantID, callID)]
	if approval == nil {
		return nil, status.Error(codes.NotFound, "approval not found")
	}
	if approval.ArgsHash != argsHash {
		return nil, status.Error(codes.FailedPrecondition, "hash differs")
	}
	copy := *approval
	return &copy, nil
}

func (c *fakeLogicClient) ExecuteTenantMembershipStatus(ctx context.Context, args agentmutation.MembershipStatusArgs, idempotencyKey string, approvalVersion int64) (*MutationReceipt, error) {
	if c.observeExecute != nil {
		c.observeExecute(ctx)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.executeCalls++
	if receipt := c.receipts[idempotencyKey]; receipt != nil {
		copy := *receipt
		copy.Repeated = true
		return &copy, nil
	}
	receipt := &MutationReceipt{
		OperationID: idempotencyKey, TargetUsername: args.TargetUsername,
		PreviousStatus: model.TenantMembershipStatusActive, ResultStatus: args.DesiredStatus,
		PreviousVersion: args.ExpectedVersion, ResultVersion: args.ExpectedVersion + 1,
		ApprovalVersion: approvalVersion, CommittedAt: time.Date(2026, 8, 9, 1, 0, 3, 0, time.UTC),
	}
	c.receipts[idempotencyKey] = receipt
	c.commits++
	if c.loseFirstResponse {
		c.loseFirstResponse = false
		return nil, status.Error(codes.Unavailable, "response lost after commit")
	}
	copy := *receipt
	return &copy, nil
}

func (c *fakeLogicClient) approve(tenantID, callID, approver string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	approval := c.approvals[storeKey(tenantID, callID)]
	approval.Status = model.AgentApprovalStatusApproved
	approval.Decision = model.AgentApprovalDecisionApprove
	approval.DecisionBy = approver
	approval.Version = 2
	approval.DecidedAt = time.Date(2026, 8, 9, 1, 0, 2, 0, time.UTC)
}

func storeKey(first, second string) string { return first + "\x00" + second }
func cloneExecution(value *model.AgentToolExecution) *model.AgentToolExecution {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func cloneFrozen(value *model.AgentFrozenToolArgs) *model.AgentFrozenToolArgs {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Payload = append([]byte(nil), value.Payload...)
	return &copy
}
