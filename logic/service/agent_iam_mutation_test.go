package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/pkg/agentmutation"
	"github.com/ceyewan/resonance/repo"
)

func TestAgentIAMMutationService_RechecksScopeAndFrozenBinding(t *testing.T) {
	now := time.Date(2026, 8, 9, 3, 0, 0, 0, time.UTC)
	args := agentmutation.NewMembershipStatusArgs(
		"tenant-a", "run-1", "call-1", "admin", "member",
		model.TenantMembershipStatusDisabled, 4, false,
	)
	hash, err := args.Hash()
	require.NoError(t, err)
	approval := &model.AgentApproval{
		ID: 1, TenantID: args.TenantID, RunID: args.RunID, CallID: args.CallID,
		ToolName: args.ToolName, RequesterID: args.RequesterID, ArgsHash: hash,
		ArgsSummary: "disable member", Status: model.AgentApprovalStatusApproved,
		Decision: model.AgentApprovalDecisionApprove, DecisionBy: "approver", Version: 2,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}
	decidedAt := now
	approval.DecidedAt = &decidedAt
	store := &testAgentApprovalStore{getFn: func(_ context.Context, tenantID, callID string) (*model.AgentApproval, error) {
		require.Equal(t, "tenant-a", tenantID)
		require.Equal(t, "call-1", callID)
		copy := *approval
		return &copy, nil
	}}
	mutations := &testAgentIAMMutationRepo{executeFn: func(_ context.Context, request repo.AgentIAMMembershipMutation) (*repo.AgentIAMMutationResult, error) {
		require.Equal(t, hash, request.ArgsHash)
		require.Equal(t, "admin", request.RequesterID)
		return &repo.AgentIAMMutationResult{Receipt: &model.AgentIAMMutationReceipt{
			TenantID: request.TenantID, OperationID: request.IdempotencyKey, IdempotencyKey: request.IdempotencyKey,
			RunID: request.RunID, CallID: request.CallID, ArgsHash: request.ArgsHash, ToolName: request.ToolName,
			RequesterID: request.RequesterID, TargetUsername: request.TargetUsername,
			PreviousStatus: model.TenantMembershipStatusActive, ResultStatus: request.DesiredStatus,
			PreviousVersion: request.ExpectedVersion, ResultVersion: request.ExpectedVersion + 1,
			ApprovalVersion: request.ApprovalVersion, DownstreamCommittedAt: now,
		}}, nil
	}}
	authorizer := &testSystemScopeAuthorizer{allowed: false}
	service := NewAgentIAMMutationService(store, mutations, authorizer, testLogger(), AgentIAMMutationPolicy{
		PilotServiceID: "pilot-service", WriteScope: model.ScopeIAMUsersWrite,
	}, WithAgentIAMMutationClock(func() time.Time { return now }))
	trusted := WithServicePrincipal(context.Background(), "pilot-service", "tenant-a", "agent-bot")
	request := &logicv1.ExecuteTenantMembershipStatusRequest{
		TenantId: args.TenantID, RunId: args.RunID, CallId: args.CallID, RequesterId: args.RequesterID,
		ArgsHash: hash, ToolName: args.ToolName, IdempotencyKey: agentmutation.IdempotencyKey(args.TenantID, args.CallID),
		TargetUsername: args.TargetUsername, DesiredStatus: args.DesiredStatus,
		ExpectedVersion: args.ExpectedVersion, ApprovalVersion: 2, DryRun: false,
	}

	_, err = service.ExecuteTenantMembershipStatus(trusted, request)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Equal(t, 0, mutations.calls)
	authorizer.allowed = true

	substitution := proto.Clone(request).(*logicv1.ExecuteTenantMembershipStatusRequest)
	substitution.TargetUsername = "other-member"
	_, err = service.ExecuteTenantMembershipStatus(trusted, substitution)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Equal(t, 0, mutations.calls)

	response, err := service.ExecuteTenantMembershipStatus(trusted, request)
	require.NoError(t, err)
	require.Equal(t, request.IdempotencyKey, response.GetOperationId())
	require.Equal(t, int64(5), response.GetResultVersion())
	require.Equal(t, 1, mutations.calls)
	require.Equal(t, []scopeCheck{
		{tenantID: "tenant-a", actorID: "admin", scope: model.ScopeIAMUsersWrite},
		{tenantID: "tenant-a", actorID: "admin", scope: model.ScopeIAMUsersWrite},
		{tenantID: "tenant-a", actorID: "approver", scope: model.ScopeAgentApprovalDecide},
	}, authorizer.checks)

	wrongTenant := WithServicePrincipal(context.Background(), "pilot-service", "tenant-b", "agent-bot")
	_, err = service.ExecuteTenantMembershipStatus(wrongTenant, request)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestAgentIAMMutationService_ReplaysCommittedReceiptBeforeExpiryOrScopeChecks(t *testing.T) {
	now := time.Date(2026, 8, 9, 5, 0, 0, 0, time.UTC)
	args := agentmutation.NewMembershipStatusArgs(
		"tenant-a", "run-replay", "call-replay", "admin", "member",
		model.TenantMembershipStatusDisabled, 7, false,
	)
	hash, err := args.Hash()
	require.NoError(t, err)
	getApprovalCalls := 0
	store := &testAgentApprovalStore{getFn: func(context.Context, string, string) (*model.AgentApproval, error) {
		getApprovalCalls++
		return nil, repo.ErrAgentApprovalNotFound
	}}
	receipt := &model.AgentIAMMutationReceipt{
		TenantID: args.TenantID, OperationID: agentmutation.IdempotencyKey(args.TenantID, args.CallID),
		IdempotencyKey: agentmutation.IdempotencyKey(args.TenantID, args.CallID), RunID: args.RunID, CallID: args.CallID,
		ArgsHash: hash, ToolName: args.ToolName, RequesterID: args.RequesterID, TargetUsername: args.TargetUsername,
		PreviousStatus: model.TenantMembershipStatusActive, ResultStatus: args.DesiredStatus,
		PreviousVersion: args.ExpectedVersion, ResultVersion: args.ExpectedVersion + 1,
		ApprovalVersion: 2, DownstreamCommittedAt: now.Add(-time.Hour),
	}
	mutations := &testAgentIAMMutationRepo{lookupFn: func(context.Context, repo.AgentIAMMembershipMutation) (*repo.AgentIAMMutationResult, error) {
		return &repo.AgentIAMMutationResult{Receipt: receipt, Repeated: true}, nil
	}}
	authorizer := &testSystemScopeAuthorizer{allowed: false}
	service := NewAgentIAMMutationService(store, mutations, authorizer, testLogger(), AgentIAMMutationPolicy{PilotServiceID: "pilot-service"},
		WithAgentIAMMutationClock(func() time.Time { return now }))
	response, err := service.ExecuteTenantMembershipStatus(
		WithServicePrincipal(context.Background(), "pilot-service", "tenant-a", "agent-bot"),
		&logicv1.ExecuteTenantMembershipStatusRequest{
			TenantId: args.TenantID, RunId: args.RunID, CallId: args.CallID, RequesterId: args.RequesterID,
			ArgsHash: hash, ToolName: args.ToolName, IdempotencyKey: receipt.IdempotencyKey,
			TargetUsername: args.TargetUsername, DesiredStatus: args.DesiredStatus,
			ExpectedVersion: args.ExpectedVersion, ApprovalVersion: 2, DryRun: false,
		},
	)
	require.NoError(t, err)
	require.True(t, response.GetRepeated())
	require.Equal(t, receipt.OperationID, response.GetOperationId())
	require.Zero(t, getApprovalCalls, "已提交事实不能再被当前审批 expiry/revoke 阻断")
	require.Empty(t, authorizer.checks, "已提交事实不能再被 requester/approver 后续降权阻断")
	require.Zero(t, mutations.calls)
}

type testAgentIAMMutationRepo struct {
	executeFn func(context.Context, repo.AgentIAMMembershipMutation) (*repo.AgentIAMMutationResult, error)
	lookupFn  func(context.Context, repo.AgentIAMMembershipMutation) (*repo.AgentIAMMutationResult, error)
	calls     int
}

func (r *testAgentIAMMutationRepo) PreviewTenantMembershipStatus(_ context.Context, request repo.AgentIAMMembershipPreview) (*repo.AgentIAMMembershipPreviewResult, error) {
	return &repo.AgentIAMMembershipPreviewResult{
		TargetUsername: request.TargetUsername, CurrentStatus: model.TenantMembershipStatusActive,
		DesiredStatus: request.DesiredStatus, CurrentVersion: request.ExpectedVersion, WouldChange: true,
	}, nil
}

func (r *testAgentIAMMutationRepo) GetTenantMembershipStatusResult(ctx context.Context, request repo.AgentIAMMembershipMutation) (*repo.AgentIAMMutationResult, error) {
	if r.lookupFn != nil {
		return r.lookupFn(ctx, request)
	}
	return nil, repo.ErrAgentIAMMutationReceiptNotFound
}

func (r *testAgentIAMMutationRepo) ExecuteTenantMembershipStatus(ctx context.Context, request repo.AgentIAMMembershipMutation) (*repo.AgentIAMMutationResult, error) {
	r.calls++
	return r.executeFn(ctx, request)
}

func (*testAgentIAMMutationRepo) Close() error { return nil }
