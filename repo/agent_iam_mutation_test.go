package repo

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/pkg/agentmutation"
)

func TestAgentMutationPreparationRepo_FreezesArgsAndExecutionAtomically(t *testing.T) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)
	preparations, err := NewAgentMutationPreparationRepo(database)
	require.NoError(t, err)
	executions, err := NewAgentToolExecutionRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	args := agentmutation.NewMembershipStatusArgs(
		"tenant-a", "run-prepare", "call-prepare", "admin", "member",
		model.TenantMembershipStatusDisabled, 1, false,
	)
	payload, err := args.CanonicalPayload()
	require.NoError(t, err)
	hash, err := args.Hash()
	require.NoError(t, err)
	ref := agentmutation.FrozenArgsRef(args.TenantID, args.CallID, hash)
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	frozen := &model.AgentFrozenToolArgs{
		TenantID: args.TenantID, Ref: ref, RunID: args.RunID, CallID: args.CallID,
		RequesterID: args.RequesterID, ArgsHash: hash, Payload: payload, ApprovalExpiresAt: expires,
	}
	execution := &model.AgentToolExecution{
		TenantID: args.TenantID, RunID: args.RunID, CallID: args.CallID, RuntimeToolCallID: args.CallID,
		ToolName: args.ToolName, ToolVersion: "1", SchemaVersion: "1", ArgsHash: hash,
		FrozenArgsRef: ref, ArgsSummary: "disable member", IdempotencyKey: agentmutation.IdempotencyKey(args.TenantID, args.CallID),
	}

	const workers = 8
	var created atomic.Int64
	var wait sync.WaitGroup
	errorsCh := make(chan error, workers)
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, prepareErr := preparations.PrepareAgentMutation(ctx, frozen, execution)
			if prepareErr == nil && result.Created {
				created.Add(1)
			}
			errorsCh <- prepareErr
		}()
	}
	wait.Wait()
	close(errorsCh)
	for prepareErr := range errorsCh {
		require.NoError(t, prepareErr)
	}
	require.Equal(t, int64(1), created.Load())
	persisted, err := executions.GetAgentToolExecution(ctx, args.TenantID, args.CallID)
	require.NoError(t, err)
	require.Equal(t, model.AgentToolExecutionStatusPrepared, persisted.Status)
	persistedFrozen, err := preparations.GetAgentFrozenToolArgs(ctx, args.TenantID, ref)
	require.NoError(t, err)
	require.Equal(t, payload, persistedFrozen.Payload)

	substituted := *frozen
	substituted.Payload = append([]byte(nil), payload...)
	substituted.Payload[len(substituted.Payload)-2] = 'x'
	_, err = preparations.PrepareAgentMutation(ctx, &substituted, execution)
	require.Error(t, err)

	listed, err := preparations.ListAgentToolExecutions(ctx, AgentToolExecutionListFilter{
		TenantID: args.TenantID, Statuses: []string{model.AgentToolExecutionStatusPrepared}, Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, listed, 1)
}

func TestAgentIAMMutationRepo_AuthoritativeApprovalSafetyAndResponseLossRecovery(t *testing.T) {
	database, cleanup := setupTestContext(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	identityRepo, err := NewIdentityRepo(database)
	require.NoError(t, err)
	approvalRepo, err := NewAgentApprovalRepo(database)
	require.NoError(t, err)
	mutations, err := NewAgentIAMMutationRepo(database, "agent-bot")
	require.NoError(t, err)

	createIdentityForMutation(t, identityRepo, "tenant-a", "admin", model.UserKindHuman, []string{model.SystemRoleIAMAdmin})
	createIdentityForMutation(t, identityRepo, "tenant-a", "approver", model.UserKindHuman, []string{model.SystemRoleIAMAdmin})
	createIdentityForMutation(t, identityRepo, "tenant-a", "member", model.UserKindHuman, []string{model.SystemRoleUser})
	createIdentityForMutation(t, identityRepo, "tenant-a", "agent-bot", model.UserKindAgentBot, []string{model.SystemRoleUser})
	createIdentityForMutation(t, identityRepo, "tenant-b", "other-member", model.UserKindHuman, []string{model.SystemRoleUser})
	preview, err := mutations.PreviewTenantMembershipStatus(ctx, AgentIAMMembershipPreview{
		TenantID: "tenant-a", RunID: "run-preview", CallID: "call-preview", RequesterID: "admin",
		TargetUsername: "member", DesiredStatus: model.TenantMembershipStatusDisabled, ExpectedVersion: 1,
	})
	require.NoError(t, err)
	require.True(t, preview.WouldChange)
	previewMember, err := identityRepo.GetTenantMembership(ctx, "tenant-a", "member")
	require.NoError(t, err)
	require.Equal(t, model.TenantMembershipStatusActive, previewMember.Status)
	for _, tableModel := range []any{&model.AgentApproval{}, &model.AgentToolExecution{}, &model.AgentIAMMutationReceipt{}} {
		var count int64
		require.NoError(t, database.DB(ctx).Model(tableModel).Count(&count).Error)
		require.Zero(t, count, "dry_run preview 不得创建审批、执行或收据事实")
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	args := agentmutation.NewMembershipStatusArgs(
		"tenant-a", "run-mutation", "call-mutation", "admin", "member",
		model.TenantMembershipStatusDisabled, 1, false,
	)
	hash, err := args.Hash()
	require.NoError(t, err)
	createApprovedMutationApproval(t, approvalRepo, args, hash, now.Add(time.Hour), now)
	request := AgentIAMMembershipMutation{
		TenantID: args.TenantID, RunID: args.RunID, CallID: args.CallID, ArgsHash: hash,
		ToolName: args.ToolName, IdempotencyKey: agentmutation.IdempotencyKey(args.TenantID, args.CallID),
		RequesterID: args.RequesterID, TargetUsername: args.TargetUsername, DesiredStatus: args.DesiredStatus,
		ExpectedVersion: args.ExpectedVersion, ApprovalVersion: 2, OccurredAt: now.Add(time.Minute),
	}

	first, err := mutations.ExecuteTenantMembershipStatus(ctx, request)
	require.NoError(t, err)
	require.False(t, first.Repeated)
	require.Equal(t, int64(2), first.Receipt.ResultVersion)
	_, err = approvalRepo.TransitionAgentApproval(ctx, AgentApprovalTransition{
		TenantID: args.TenantID, CallID: args.CallID, ArgsHash: hash,
		ExpectedStatus: model.AgentApprovalStatusApproved, ExpectedVersion: 2,
		NextStatus: model.AgentApprovalStatusRevoked, ActorID: "approver", OccurredAt: now.Add(90 * time.Second),
	})
	require.NoError(t, err)
	require.NoError(t, identityRepo.DeleteSystemRoleBinding(ctx, "tenant-a", "admin", model.SystemRoleIAMAdmin))
	retry := request
	retry.OccurredAt = now.Add(2 * time.Minute)
	second, err := mutations.ExecuteTenantMembershipStatus(ctx, retry)
	require.NoError(t, err)
	require.True(t, second.Repeated, "RPC 响应丢失后的新签名重试必须返回同一收据")
	require.Equal(t, first.Receipt.OperationID, second.Receipt.OperationID)
	membership, err := identityRepo.GetTenantMembership(ctx, "tenant-a", "member")
	require.NoError(t, err)
	require.Equal(t, model.TenantMembershipStatusDisabled, membership.Status)
	require.Equal(t, int64(2), membership.Version, "响应重试不能再次推进版本")
	var receiptCount int64
	require.NoError(t, database.DB(ctx).Model(&model.AgentIAMMutationReceipt{}).Count(&receiptCount).Error)
	require.Equal(t, int64(1), receiptCount)

	// 参数替换即使复用 call_id 也必须在进入 IAM 事务前失败。
	substitution := request
	substitution.DesiredStatus = model.TenantMembershipStatusActive
	_, err = mutations.ExecuteTenantMembershipStatus(ctx, substitution)
	require.ErrorIs(t, err, ErrAgentIAMMutationConflict)

	// 同一个用户在另一个 tenant 下没有对应审批，不能越权命中 tenant-a 事实。
	crossTenant := request
	crossTenant.TenantID = "tenant-b"
	crossTenant.IdempotencyKey = agentmutation.IdempotencyKey("tenant-b", crossTenant.CallID)
	_, err = mutations.ExecuteTenantMembershipStatus(ctx, crossTenant)
	require.Error(t, err)
}

func TestAgentIAMMutationRepo_FailsClosedForDowngradeSelfBotExpiryAndRevoke(t *testing.T) {
	tests := []struct {
		name   string
		id     string
		target string
		mutate func(t *testing.T, identity IdentityRepo, approval AgentApprovalRepo, args agentmutation.MembershipStatusArgs, hash string, now time.Time)
	}{
		{name: "requester downgraded", id: "downgraded", target: "member", mutate: func(t *testing.T, identity IdentityRepo, _ AgentApprovalRepo, _ agentmutation.MembershipStatusArgs, _ string, _ time.Time) {
			require.NoError(t, identity.DeleteSystemRoleBinding(context.Background(), "tenant-a", "admin", model.SystemRoleIAMAdmin))
		}},
		{name: "approver downgraded", id: "approver-downgraded", target: "member", mutate: func(t *testing.T, identity IdentityRepo, _ AgentApprovalRepo, _ agentmutation.MembershipStatusArgs, _ string, _ time.Time) {
			require.NoError(t, identity.DeleteSystemRoleBinding(context.Background(), "tenant-a", "approver", model.SystemRoleIAMAdmin))
		}},
		{name: "self mutation", id: "self", target: "admin"},
		{name: "agent bot", id: "bot", target: "agent-bot"},
		{name: "approval expired", id: "expired", target: "member", mutate: func(_ *testing.T, _ IdentityRepo, _ AgentApprovalRepo, _ agentmutation.MembershipStatusArgs, _ string, _ time.Time) {
		}},
		{name: "approval revoked", id: "revoked", target: "member", mutate: func(t *testing.T, _ IdentityRepo, approval AgentApprovalRepo, args agentmutation.MembershipStatusArgs, hash string, now time.Time) {
			_, err := approval.TransitionAgentApproval(context.Background(), AgentApprovalTransition{
				TenantID: args.TenantID, CallID: args.CallID, ArgsHash: hash, ExpectedStatus: model.AgentApprovalStatusApproved,
				ExpectedVersion: 2, NextStatus: model.AgentApprovalStatusRevoked, ActorID: "approver", OccurredAt: now.Add(time.Minute),
			})
			require.NoError(t, err)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, cleanup := setupTestContext(t)
			t.Cleanup(cleanup)
			ctx := context.Background()
			identityRepo, err := NewIdentityRepo(database)
			require.NoError(t, err)
			approvalRepo, err := NewAgentApprovalRepo(database)
			require.NoError(t, err)
			mutations, err := NewAgentIAMMutationRepo(database, "agent-bot")
			require.NoError(t, err)
			createIdentityForMutation(t, identityRepo, "tenant-a", "admin", model.UserKindHuman, []string{model.SystemRoleIAMAdmin})
			createIdentityForMutation(t, identityRepo, "tenant-a", "approver", model.UserKindHuman, []string{model.SystemRoleIAMAdmin})
			createIdentityForMutation(t, identityRepo, "tenant-a", "member", model.UserKindHuman, []string{model.SystemRoleUser})
			createIdentityForMutation(t, identityRepo, "tenant-a", "agent-bot", model.UserKindAgentBot, []string{model.SystemRoleUser})

			now := time.Now().UTC().Truncate(time.Microsecond)
			expires := now.Add(time.Hour)
			if test.name == "approval expired" {
				expires = now.Add(-time.Minute)
			}
			args := agentmutation.NewMembershipStatusArgs(
				"tenant-a", "run-"+test.id, "call-"+test.id, "admin", test.target,
				model.TenantMembershipStatusDisabled, 1, false,
			)
			hash, err := args.Hash()
			require.NoError(t, err)
			decisionAt := now
			if expires.Before(now) {
				decisionAt = expires.Add(-time.Minute)
			}
			createApprovedMutationApproval(t, approvalRepo, args, hash, expires, decisionAt)
			if test.mutate != nil {
				test.mutate(t, identityRepo, approvalRepo, args, hash, now)
			}
			_, err = mutations.ExecuteTenantMembershipStatus(ctx, AgentIAMMembershipMutation{
				TenantID: args.TenantID, RunID: args.RunID, CallID: args.CallID, ArgsHash: hash,
				ToolName: args.ToolName, IdempotencyKey: agentmutation.IdempotencyKey(args.TenantID, args.CallID),
				RequesterID: args.RequesterID, TargetUsername: args.TargetUsername, DesiredStatus: args.DesiredStatus,
				ExpectedVersion: 1, ApprovalVersion: map[bool]int64{true: 3, false: 2}[test.name == "approval revoked"], OccurredAt: now.Add(2 * time.Minute),
			})
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrAgentIAMMutationNotAllowed) || errors.Is(err, ErrAgentIAMMutationConflict))
		})
	}
}

func createIdentityForMutation(t *testing.T, identity IdentityRepo, tenantID, username string, kind int, roles []string) {
	t.Helper()
	err := identity.CreateIdentity(context.Background(),
		&model.User{Username: username, Password: "hashed", Kind: kind},
		&model.TenantMembership{TenantID: tenantID, Username: username, Status: model.TenantMembershipStatusActive, Version: 1},
		roles,
	)
	require.NoError(t, err)
}

func createApprovedMutationApproval(t *testing.T, approvals AgentApprovalRepo, args agentmutation.MembershipStatusArgs, hash string, expiresAt, decidedAt time.Time) {
	t.Helper()
	_, err := approvals.CreateAgentApproval(context.Background(), &model.AgentApproval{
		TenantID: args.TenantID, RunID: args.RunID, CallID: args.CallID, ToolName: args.ToolName,
		RequesterID: args.RequesterID, ArgsHash: hash, ArgsSummary: "membership mutation", ExpiresAt: expiresAt,
	})
	require.NoError(t, err)
	_, err = approvals.TransitionAgentApproval(context.Background(), AgentApprovalTransition{
		TenantID: args.TenantID, CallID: args.CallID, ArgsHash: hash, ExpectedStatus: model.AgentApprovalStatusPending,
		ExpectedVersion: 1, NextStatus: model.AgentApprovalStatusApproved, ActorID: "approver", OccurredAt: decidedAt,
	})
	require.NoError(t, err)
}
