package httpapi

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/gateway/middleware"
)

type fakeApprovalLogicClient struct {
	get    func(context.Context, *logicv1.GetApprovalRequest) (*logicv1.GetApprovalResponse, error)
	list   func(context.Context, *logicv1.ListApprovalsRequest) (*logicv1.ListApprovalsResponse, error)
	decide func(context.Context, *logicv1.DecideApprovalRequest) (*logicv1.DecideApprovalResponse, error)
}

func (f *fakeApprovalLogicClient) GetAgentApproval(ctx context.Context, req *logicv1.GetApprovalRequest) (*logicv1.GetApprovalResponse, error) {
	return f.get(ctx, req)
}

func (f *fakeApprovalLogicClient) ListAgentApprovals(ctx context.Context, req *logicv1.ListApprovalsRequest) (*logicv1.ListApprovalsResponse, error) {
	return f.list(ctx, req)
}

func (f *fakeApprovalLogicClient) DecideAgentApproval(ctx context.Context, req *logicv1.DecideApprovalRequest) (*logicv1.DecideApprovalResponse, error) {
	return f.decide(ctx, req)
}

func approvalRequestContext(tenantID string) context.Context {
	return middleware.WithPrincipal(context.Background(), &middleware.Principal{
		TenantID: tenantID, Username: "admin", MembershipVersion: 7,
	})
}

func testLogicApproval() *logicv1.AgentApproval {
	return &logicv1.AgentApproval{
		Id: 41, TenantId: "tenant-a", RunId: "run-1", CallId: "call-1", ToolName: "disable_tenant_user",
		RequesterId: "requester", ArgsHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ArgsSummary: "Disable user bob", Status: logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_PENDING,
		Version: 3, ExpiresAtMs: 1_900_000_000_000,
	}
}

func TestAgentApprovalPublicProtocolHasNoCreateTenantOrFrozenArgs(t *testing.T) {
	service := gatewayv1.File_gateway_v1_agent_approval_proto.Services().ByName("AgentApprovalService")
	require.NotNil(t, service)
	require.Equal(t, 3, service.Methods().Len())
	require.Nil(t, service.Methods().ByName("CreateApproval"))

	for _, messageName := range []string{"GetApprovalRequest", "ListApprovalsRequest", "DecideApprovalRequest"} {
		message := gatewayv1.File_gateway_v1_agent_approval_proto.Messages().ByName(protoreflect.Name(messageName))
		require.NotNil(t, message)
		require.Nil(t, message.Fields().ByName("tenant_id"))
	}
	approval := gatewayv1.File_gateway_v1_agent_approval_proto.Messages().ByName("AgentApproval")
	require.NotNil(t, approval)
	require.Nil(t, approval.Fields().ByName("tenant_id"))
	require.Nil(t, approval.Fields().ByName("frozen_args"))
	require.Nil(t, approval.Fields().ByName("raw_args"))
	require.NotNil(t, approval.Fields().ByName("args_summary"))
}

func TestGetApprovalInjectsVerifiedTenantAndRedactsTenantFromResponse(t *testing.T) {
	fake := &fakeApprovalLogicClient{}
	fake.get = func(_ context.Context, req *logicv1.GetApprovalRequest) (*logicv1.GetApprovalResponse, error) {
		require.Equal(t, "tenant-a", req.GetTenantId())
		// A call ID cannot change the trusted tenant used for the Logic lookup.
		require.Equal(t, "tenant-b-call", req.GetCallId())
		return &logicv1.GetApprovalResponse{Approval: testLogicApproval()}, nil
	}
	handler := &HTTPHandler{approvalClient: fake}
	response, err := handler.GetApproval(
		approvalRequestContext("tenant-a"),
		connect.NewRequest(&gatewayv1.GetApprovalRequest{CallId: "tenant-b-call"}),
	)
	require.NoError(t, err)
	require.Equal(t, "Disable user bob", response.Msg.GetApproval().GetArgsSummary())
	require.Equal(t, "call-1", response.Msg.GetApproval().GetCallId())
}

func TestGetApprovalFailsClosedOnCrossTenantLogicResponse(t *testing.T) {
	fake := &fakeApprovalLogicClient{}
	fake.get = func(_ context.Context, req *logicv1.GetApprovalRequest) (*logicv1.GetApprovalResponse, error) {
		require.Equal(t, "tenant-a", req.GetTenantId())
		foreign := testLogicApproval()
		foreign.TenantId = "tenant-b"
		return &logicv1.GetApprovalResponse{Approval: foreign}, nil
	}
	_, err := (&HTTPHandler{approvalClient: fake}).GetApproval(
		approvalRequestContext("tenant-a"),
		connect.NewRequest(&gatewayv1.GetApprovalRequest{CallId: "call-1"}),
	)
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestListApprovalsPropagatesCurrentScopeDenial(t *testing.T) {
	fake := &fakeApprovalLogicClient{}
	fake.list = func(_ context.Context, req *logicv1.ListApprovalsRequest) (*logicv1.ListApprovalsResponse, error) {
		require.Equal(t, "tenant-a", req.GetTenantId())
		require.Equal(t, int64(99), req.GetBeforeId())
		return nil, status.Error(codes.PermissionDenied, "required system scope is missing")
	}
	handler := &HTTPHandler{approvalClient: fake}
	_, err := handler.ListApprovals(
		approvalRequestContext("tenant-a"),
		connect.NewRequest(&gatewayv1.ListApprovalsRequest{BeforeId: 99, PageSize: 20}),
	)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestDecideApprovalBindsHashVersionReasonAndTrustedTenant(t *testing.T) {
	approval := testLogicApproval()
	approval.Status = logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_APPROVED
	approval.Version = 4
	fake := &fakeApprovalLogicClient{}
	fake.decide = func(_ context.Context, req *logicv1.DecideApprovalRequest) (*logicv1.DecideApprovalResponse, error) {
		require.Equal(t, "tenant-a", req.GetTenantId())
		require.Equal(t, "call-1", req.GetCallId())
		require.Equal(t, testLogicApproval().GetArgsHash(), req.GetArgsHash())
		require.Equal(t, int64(3), req.GetExpectedVersion())
		require.Equal(t, logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_APPROVE, req.GetDecision())
		require.Equal(t, "reviewed", req.GetReason())
		return &logicv1.DecideApprovalResponse{Approval: approval, Changed: true}, nil
	}
	handler := &HTTPHandler{approvalClient: fake}
	response, err := handler.DecideApproval(
		approvalRequestContext("tenant-a"),
		connect.NewRequest(&gatewayv1.DecideApprovalRequest{
			CallId: "call-1", ArgsHash: testLogicApproval().GetArgsHash(), ExpectedVersion: 3,
			Decision: gatewayv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_APPROVE, Reason: "reviewed",
		}),
	)
	require.NoError(t, err)
	require.True(t, response.Msg.GetChanged())
	require.Equal(t, int64(4), response.Msg.GetApproval().GetVersion())
}

func TestDecideApprovalMapsVersionConflictAndIdempotentReplay(t *testing.T) {
	request := connect.NewRequest(&gatewayv1.DecideApprovalRequest{
		CallId: "call-1", ArgsHash: testLogicApproval().GetArgsHash(), ExpectedVersion: 3,
		Decision: gatewayv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_REJECT,
	})

	t.Run("version conflict", func(t *testing.T) {
		fake := &fakeApprovalLogicClient{}
		fake.decide = func(context.Context, *logicv1.DecideApprovalRequest) (*logicv1.DecideApprovalResponse, error) {
			return nil, status.Error(codes.Aborted, "approval version or binding conflict")
		}
		_, err := (&HTTPHandler{approvalClient: fake}).DecideApproval(approvalRequestContext("tenant-a"), request)
		require.Equal(t, connect.CodeAborted, connect.CodeOf(err))
	})

	t.Run("idempotent replay", func(t *testing.T) {
		fake := &fakeApprovalLogicClient{}
		fake.decide = func(context.Context, *logicv1.DecideApprovalRequest) (*logicv1.DecideApprovalResponse, error) {
			approval := testLogicApproval()
			approval.Status = logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REJECTED
			approval.Version = 4
			return &logicv1.DecideApprovalResponse{Approval: approval, Changed: false}, nil
		}
		response, err := (&HTTPHandler{approvalClient: fake}).DecideApproval(approvalRequestContext("tenant-a"), request)
		require.NoError(t, err)
		require.False(t, response.Msg.GetChanged())
		require.Equal(t, gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REJECTED, response.Msg.GetApproval().GetStatus())
	})
}

func TestAgentApprovalRequiresVerifiedPrincipal(t *testing.T) {
	called := false
	fake := &fakeApprovalLogicClient{}
	fake.get = func(context.Context, *logicv1.GetApprovalRequest) (*logicv1.GetApprovalResponse, error) {
		called = true
		return nil, nil
	}
	_, err := (&HTTPHandler{approvalClient: fake}).GetApproval(
		context.Background(), connect.NewRequest(&gatewayv1.GetApprovalRequest{CallId: "call-1"}),
	)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	require.False(t, called)
}
