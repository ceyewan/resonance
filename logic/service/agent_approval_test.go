package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
)

func TestAgentApprovalService_CreateRequiresTrustedPilotAndWritesRequestedOutbox(t *testing.T) {
	now := time.Date(2026, 8, 9, 1, 0, 0, 0, time.UTC)
	argsHash := strings.Repeat("a", 64)
	called := 0
	store := &testAgentApprovalStore{
		createFn: func(_ context.Context, approval *model.AgentApproval, outbox *model.MessageOutbox) (*repo.AgentApprovalCreateResult, error) {
			called++
			require.Equal(t, "tenant-a", approval.TenantID)
			require.Equal(t, argsHash, approval.ArgsHash)
			require.Equal(t, int64(9001), outbox.EventID)
			require.Equal(t, "resonance.agent.approval.requested.v1", outbox.Topic)
			event := new(mqv1.AgentApprovalRequestedEvent)
			require.NoError(t, proto.Unmarshal(outbox.Payload, event))
			require.Equal(t, "call-1", event.GetCallId())
			require.Equal(t, argsHash, event.GetArgsHash())
			require.Equal(t, int64(1), event.GetApprovalVersion())
			persisted := *approval
			persisted.ID = 10
			persisted.Status = model.AgentApprovalStatusPending
			persisted.Decision = model.AgentApprovalDecisionNone
			persisted.Version = 1
			outbox.ID = 20
			return &repo.AgentApprovalCreateResult{Approval: &persisted, Outbox: outbox, Created: true}, nil
		},
	}
	svc := newTestAgentApprovalService(store, allowAgentApprovalScopes(true), now)
	req := &logicv1.CreateApprovalRequest{
		TenantId:    "tenant-a",
		RunId:       "run-1",
		CallId:      "call-1",
		ToolName:    "iam.disable_user",
		RequesterId: "alice",
		ArgsHash:    argsHash,
		ArgsSummary: "disable one user",
		ExpiresAtMs: now.Add(time.Hour).UnixMilli(),
	}

	_, err := svc.CreateApproval(newTestApprovalUserContext("resonance-agent", "tenant-a"), req)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Equal(t, 0, called)

	wrongTenant := WithServicePrincipal(context.Background(), "pilot-service", "tenant-b", "resonance-agent")
	_, err = svc.CreateApproval(wrongTenant, req)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Equal(t, 0, called)

	trusted := WithServicePrincipal(context.Background(), "pilot-service", "tenant-a", "resonance-agent")
	response, err := svc.CreateApproval(trusted, req)
	require.NoError(t, err)
	require.True(t, response.GetCreated())
	require.Equal(t, int64(10), response.GetApproval().GetId())
	require.Equal(t, logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_PENDING, response.GetApproval().GetStatus())
	require.Equal(t, 1, called)
}

func TestAgentApprovalService_CreateExpiredRetryReturnsOriginalFact(t *testing.T) {
	now := time.Date(2026, 8, 9, 1, 30, 0, 0, time.UTC)
	argsHash := strings.Repeat("e", 64)
	existing := testApprovalFact(now, argsHash)
	existing.ExpiresAt = now.Add(-time.Minute)
	existing.Status = model.AgentApprovalStatusExpired
	createCalls := 0
	store := &testAgentApprovalStore{
		getFn: func(context.Context, string, string) (*model.AgentApproval, error) {
			copy := *existing
			return &copy, nil
		},
		createFn: func(context.Context, *model.AgentApproval, *model.MessageOutbox) (*repo.AgentApprovalCreateResult, error) {
			createCalls++
			return nil, nil
		},
	}
	svc := newTestAgentApprovalService(store, allowAgentApprovalScopes(true), now)
	trusted := WithServicePrincipal(context.Background(), "pilot-service", "tenant-a", "resonance-agent")
	response, err := svc.CreateApproval(trusted, &logicv1.CreateApprovalRequest{
		TenantId:    existing.TenantID,
		RunId:       existing.RunID,
		CallId:      existing.CallID,
		ToolName:    existing.ToolName,
		RequesterId: existing.RequesterID,
		ArgsHash:    existing.ArgsHash,
		ArgsSummary: existing.ArgsSummary,
		ExpiresAtMs: existing.ExpiresAt.UnixMilli(),
	})
	require.NoError(t, err)
	require.False(t, response.GetCreated())
	require.Equal(t, logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_EXPIRED, response.GetApproval().GetStatus())
	require.Equal(t, 0, createCalls)
}

func TestAgentApprovalService_DecideReauthorizesBindsAndWritesDecisionOutbox(t *testing.T) {
	now := time.Date(2026, 8, 9, 2, 0, 0, 0, time.UTC)
	argsHash := strings.Repeat("b", 64)
	current := testApprovalFact(now, argsHash)
	authorizer := &testSystemScopeAuthorizer{allowed: true}
	store := &testAgentApprovalStore{
		getFn: func(_ context.Context, tenantID, callID string) (*model.AgentApproval, error) {
			require.Equal(t, "tenant-a", tenantID)
			require.Equal(t, "call-1", callID)
			copy := *current
			return &copy, nil
		},
		transitionFn: func(_ context.Context, transition repo.AgentApprovalTransition, outbox *model.MessageOutbox) (*repo.AgentApprovalTransitionResult, error) {
			require.Equal(t, int64(1), transition.ExpectedVersion)
			require.Equal(t, model.AgentApprovalStatusPending, transition.ExpectedStatus)
			require.Equal(t, model.AgentApprovalStatusApproved, transition.NextStatus)
			require.Equal(t, "admin-1", transition.ActorID)
			require.Equal(t, argsHash, transition.ArgsHash)
			event := new(mqv1.AgentApprovalDecidedEvent)
			require.NoError(t, proto.Unmarshal(outbox.Payload, event))
			require.Equal(t, int64(2), event.GetApprovalVersion())
			require.Equal(t, argsHash, event.GetArgsHash())
			require.Equal(t, mqv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_APPROVE, event.GetDecision())
			updated := *current
			updated.Status = model.AgentApprovalStatusApproved
			updated.Decision = model.AgentApprovalDecisionApprove
			updated.DecisionBy = transition.ActorID
			updated.DecisionReason = transition.Reason
			updated.DecidedAt = &transition.OccurredAt
			updated.Version = 2
			outbox.ID = 21
			return &repo.AgentApprovalTransitionResult{Approval: &updated, Outbox: outbox, Changed: true}, nil
		},
	}
	svc := newTestAgentApprovalService(store, authorizer, now)
	response, err := svc.DecideApproval(newTestApprovalUserContext("admin-1", "tenant-a"), &logicv1.DecideApprovalRequest{
		TenantId:        "tenant-a",
		CallId:          "call-1",
		ArgsHash:        argsHash,
		ExpectedVersion: 1,
		Decision:        logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_APPROVE,
		Reason:          "reviewed",
	})
	require.NoError(t, err)
	require.True(t, response.GetChanged())
	require.Equal(t, logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_APPROVED, response.GetApproval().GetStatus())
	require.Equal(t, []scopeCheck{{tenantID: "tenant-a", actorID: "admin-1", scope: model.ScopeAgentApprovalDecide}}, authorizer.checks)
}

func TestAgentApprovalService_DecideFailsClosedAndForbidsSelfApproval(t *testing.T) {
	now := time.Date(2026, 8, 9, 3, 0, 0, 0, time.UTC)
	argsHash := strings.Repeat("c", 64)
	current := testApprovalFact(now, argsHash)
	getCalls := 0
	transitionCalls := 0
	store := &testAgentApprovalStore{
		getFn: func(context.Context, string, string) (*model.AgentApproval, error) {
			getCalls++
			copy := *current
			return &copy, nil
		},
		transitionFn: func(context.Context, repo.AgentApprovalTransition, *model.MessageOutbox) (*repo.AgentApprovalTransitionResult, error) {
			transitionCalls++
			return nil, nil
		},
	}
	request := &logicv1.DecideApprovalRequest{
		TenantId:        "tenant-a",
		CallId:          "call-1",
		ArgsHash:        argsHash,
		ExpectedVersion: 1,
		Decision:        logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_REJECT,
	}

	denied := newTestAgentApprovalService(store, allowAgentApprovalScopes(false), now)
	_, err := denied.DecideApproval(newTestApprovalUserContext("admin-1", "tenant-a"), request)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Equal(t, 0, getCalls, "scope 必须在读取并决定审批前重新校验")

	current.RequesterID = "admin-1"
	self := newTestAgentApprovalService(store, allowAgentApprovalScopes(true), now)
	_, err = self.DecideApproval(newTestApprovalUserContext("admin-1", "tenant-a"), request)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Equal(t, 1, getCalls)
	require.Equal(t, 0, transitionCalls)
}

func TestAgentApprovalService_GetAndListEnforceRequesterOrReadScope(t *testing.T) {
	now := time.Date(2026, 8, 9, 4, 0, 0, 0, time.UTC)
	argsHash := strings.Repeat("d", 64)
	current := testApprovalFact(now, argsHash)
	authorizer := allowAgentApprovalScopes(false)
	store := &testAgentApprovalStore{
		getFn: func(context.Context, string, string) (*model.AgentApproval, error) {
			copy := *current
			return &copy, nil
		},
		listFn: func(_ context.Context, filter repo.AgentApprovalListFilter) ([]*model.AgentApproval, error) {
			require.Equal(t, "tenant-a", filter.TenantID)
			require.Equal(t, model.AgentApprovalStatusPending, filter.Status)
			require.Equal(t, 2, filter.Limit)
			first := *current
			first.ID = 12
			second := *current
			second.ID = 11
			return []*model.AgentApproval{&first, &second}, nil
		},
	}
	svc := newTestAgentApprovalService(store, authorizer, now)

	response, err := svc.GetApproval(newTestApprovalUserContext("alice", "tenant-a"), &logicv1.GetApprovalRequest{TenantId: "tenant-a", CallId: "call-1"})
	require.NoError(t, err)
	require.Equal(t, "alice", response.GetApproval().GetRequesterId())
	require.Empty(t, authorizer.checks, "请求发起人读取自己的审批不需要管理员 scope")

	_, err = svc.GetApproval(newTestApprovalUserContext("alice", "tenant-b"), &logicv1.GetApprovalRequest{TenantId: "tenant-a", CallId: "call-1"})
	require.Equal(t, codes.PermissionDenied, status.Code(err), "即使 username 相同也不能跨越权威 tenant 边界")
	_, err = svc.GetApproval(WithUsername(context.Background(), "alice"), &logicv1.GetApprovalRequest{TenantId: "tenant-a", CallId: "call-1"})
	require.Equal(t, codes.Unauthenticated, status.Code(err), "审批读取不能接受缺少权威 tenant 的旧上下文")

	_, err = svc.GetApproval(newTestApprovalUserContext("mallory", "tenant-a"), &logicv1.GetApprovalRequest{TenantId: "tenant-a", CallId: "call-1"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))

	authorizer.allowed = true
	list, err := svc.ListApprovals(newTestApprovalUserContext("admin-1", "tenant-a"), &logicv1.ListApprovalsRequest{
		TenantId: "tenant-a",
		Status:   logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_PENDING,
		PageSize: 2,
	})
	require.NoError(t, err)
	require.Len(t, list.GetApprovals(), 2)
	require.Equal(t, int64(11), list.GetNextBeforeId())
}

type testAgentApprovalStore struct {
	createFn     func(context.Context, *model.AgentApproval, *model.MessageOutbox) (*repo.AgentApprovalCreateResult, error)
	getFn        func(context.Context, string, string) (*model.AgentApproval, error)
	listFn       func(context.Context, repo.AgentApprovalListFilter) ([]*model.AgentApproval, error)
	transitionFn func(context.Context, repo.AgentApprovalTransition, *model.MessageOutbox) (*repo.AgentApprovalTransitionResult, error)
}

func (s *testAgentApprovalStore) CreateAgentApprovalWithOutbox(ctx context.Context, approval *model.AgentApproval, outbox *model.MessageOutbox) (*repo.AgentApprovalCreateResult, error) {
	return s.createFn(ctx, approval, outbox)
}

func (s *testAgentApprovalStore) GetAgentApproval(ctx context.Context, tenantID, callID string) (*model.AgentApproval, error) {
	return s.getFn(ctx, tenantID, callID)
}

func (s *testAgentApprovalStore) ListAgentApprovals(ctx context.Context, filter repo.AgentApprovalListFilter) ([]*model.AgentApproval, error) {
	return s.listFn(ctx, filter)
}

func (s *testAgentApprovalStore) TransitionAgentApprovalWithOutbox(ctx context.Context, transition repo.AgentApprovalTransition, outbox *model.MessageOutbox) (*repo.AgentApprovalTransitionResult, error) {
	return s.transitionFn(ctx, transition, outbox)
}

type scopeCheck struct {
	tenantID string
	actorID  string
	scope    string
}

type testSystemScopeAuthorizer struct {
	allowed bool
	err     error
	checks  []scopeCheck
}

func (a *testSystemScopeAuthorizer) HasSystemScope(_ context.Context, tenantID, actorID, scope string) (bool, error) {
	a.checks = append(a.checks, scopeCheck{tenantID: tenantID, actorID: actorID, scope: scope})
	return a.allowed, a.err
}

func allowAgentApprovalScopes(allowed bool) *testSystemScopeAuthorizer {
	return &testSystemScopeAuthorizer{allowed: allowed}
}

func newTestAgentApprovalService(store AgentApprovalStore, authorizer SystemScopeAuthorizer, now time.Time) *AgentApprovalService {
	return NewAgentApprovalService(
		store,
		authorizer,
		&testGenerator{next: 9001},
		&testMQ{},
		testLogger(),
		AgentApprovalPolicy{PilotServiceID: "pilot-service"},
		WithAgentApprovalClock(func() time.Time { return now }),
	)
}

func testApprovalFact(now time.Time, argsHash string) *model.AgentApproval {
	return &model.AgentApproval{
		ID:          10,
		TenantID:    "tenant-a",
		RunID:       "run-1",
		CallID:      "call-1",
		ToolName:    "iam.disable_user",
		RequesterID: "alice",
		ArgsHash:    argsHash,
		ArgsSummary: "disable one user",
		Status:      model.AgentApprovalStatusPending,
		Decision:    model.AgentApprovalDecisionNone,
		ExpiresAt:   now.Add(time.Hour),
		Version:     1,
		CreatedAt:   now.Add(-time.Minute),
		UpdatedAt:   now.Add(-time.Minute),
	}
}

func newTestApprovalUserContext(username, tenantID string) context.Context {
	return WithUserPrincipal(context.Background(), &UserPrincipal{Username: username, TenantID: tenantID})
}
