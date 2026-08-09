package logicclient

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/pilot/mutation"
	"github.com/ceyewan/resonance/pkg/agentmutation"
	"github.com/ceyewan/resonance/pkg/serviceauth"
)

// AgentIAMMutationClient 每次业务重试都会重新调用 Authenticator，从而生成新 nonce。
// 它不依赖 gRPC transparent retry；业务幂等由 call_id/idempotency_key 保证。
type AgentIAMMutationClient struct {
	approvals     logicv1.AgentApprovalServiceClient
	mutations     logicv1.AgentIAMMutationServiceClient
	authenticator ContextAuthenticator
	botUsername   string
}

func NewAgentIAMMutationClient(
	approvals logicv1.AgentApprovalServiceClient,
	mutations logicv1.AgentIAMMutationServiceClient,
	authenticator ContextAuthenticator,
	botUsername string,
) (*AgentIAMMutationClient, error) {
	if approvals == nil || mutations == nil || authenticator == nil || botUsername == "" {
		return nil, fmt.Errorf("agent IAM mutation client configuration is incomplete")
	}
	return &AgentIAMMutationClient{
		approvals: approvals, mutations: mutations, authenticator: authenticator, botUsername: botUsername,
	}, nil
}

func (c *AgentIAMMutationClient) PreviewTenantMembershipStatus(
	ctx context.Context,
	args agentmutation.MembershipStatusArgs,
) (*mutation.MutationPreview, error) {
	if !args.DryRun {
		return nil, fmt.Errorf("preview requires dry_run=true")
	}
	req := &logicv1.PreviewTenantMembershipStatusRequest{
		TenantId: args.TenantID, RunId: args.RunID, CallId: args.CallID, RequesterId: args.RequesterID,
		TargetUsername: args.TargetUsername, DesiredStatus: args.DesiredStatus,
		ExpectedVersion: args.ExpectedVersion, DryRun: true,
	}
	authenticated, err := c.authenticate(ctx, args.TenantID, logicv1.AgentIAMMutationService_PreviewTenantMembershipStatus_FullMethodName, req)
	if err != nil {
		return nil, err
	}
	response, err := c.mutations.PreviewTenantMembershipStatus(authenticated, req)
	if err != nil {
		return nil, err
	}
	return &mutation.MutationPreview{
		TargetUsername: response.GetTargetUsername(), CurrentStatus: response.GetCurrentStatus(),
		DesiredStatus: response.GetDesiredStatus(), CurrentVersion: response.GetCurrentVersion(),
		WouldChange: response.GetWouldChange(), ArgsHash: response.GetArgsHash(),
	}, nil
}

func (c *AgentIAMMutationClient) CreateApproval(
	ctx context.Context,
	request mutation.CreateApprovalRequest,
) (*mutation.ApprovalFact, bool, error) {
	req := &logicv1.CreateApprovalRequest{
		TenantId: request.TenantID, RunId: request.RunID, CallId: request.CallID, ToolName: request.ToolName,
		RequesterId: request.RequesterID, ArgsHash: request.ArgsHash, ArgsSummary: request.ArgsSummary,
		ExpiresAtMs: request.ExpiresAt.UnixMilli(),
	}
	authenticated, err := c.authenticate(ctx, request.TenantID, logicv1.AgentApprovalService_CreateApproval_FullMethodName, req)
	if err != nil {
		return nil, false, err
	}
	response, err := c.approvals.CreateApproval(authenticated, req)
	if err != nil {
		return nil, false, err
	}
	fact, err := approvalFact(response.GetApproval())
	return fact, response.GetCreated(), err
}

func (c *AgentIAMMutationClient) GetExecutionApproval(
	ctx context.Context,
	tenantID, callID, argsHash string,
) (*mutation.ApprovalFact, error) {
	req := &logicv1.GetExecutionApprovalRequest{TenantId: tenantID, CallId: callID, ArgsHash: argsHash}
	authenticated, err := c.authenticate(ctx, tenantID, logicv1.AgentIAMMutationService_GetExecutionApproval_FullMethodName, req)
	if err != nil {
		return nil, err
	}
	response, err := c.mutations.GetExecutionApproval(authenticated, req)
	if err != nil {
		return nil, err
	}
	return approvalFact(response.GetApproval())
}

func (c *AgentIAMMutationClient) ExecuteTenantMembershipStatus(
	ctx context.Context,
	args agentmutation.MembershipStatusArgs,
	idempotencyKey string,
	approvalVersion int64,
) (*mutation.MutationReceipt, error) {
	hash, err := args.Hash()
	if err != nil {
		return nil, err
	}
	req := &logicv1.ExecuteTenantMembershipStatusRequest{
		TenantId: args.TenantID, RunId: args.RunID, CallId: args.CallID, RequesterId: args.RequesterID,
		ArgsHash: hash, ToolName: args.ToolName, IdempotencyKey: idempotencyKey,
		TargetUsername: args.TargetUsername, DesiredStatus: args.DesiredStatus,
		ExpectedVersion: args.ExpectedVersion, ApprovalVersion: approvalVersion, DryRun: args.DryRun,
	}
	authenticated, err := c.authenticate(ctx, args.TenantID, logicv1.AgentIAMMutationService_ExecuteTenantMembershipStatus_FullMethodName, req)
	if err != nil {
		return nil, err
	}
	response, err := c.mutations.ExecuteTenantMembershipStatus(authenticated, req)
	if err != nil {
		return nil, err
	}
	if response.GetOperationId() == "" || response.GetCommittedAtMs() <= 0 {
		return nil, fmt.Errorf("logic returned incomplete IAM mutation receipt")
	}
	return &mutation.MutationReceipt{
		OperationID: response.GetOperationId(), TargetUsername: response.GetTargetUsername(),
		PreviousStatus: response.GetPreviousStatus(), ResultStatus: response.GetResultStatus(),
		PreviousVersion: response.GetPreviousVersion(), ResultVersion: response.GetResultVersion(),
		ApprovalVersion: response.GetApprovalVersion(), CommittedAt: time.UnixMilli(response.GetCommittedAtMs()).UTC(),
		Repeated: response.GetRepeated(),
	}, nil
}

func (c *AgentIAMMutationClient) authenticate(ctx context.Context, tenantID, fullMethod string, payload proto.Message) (context.Context, error) {
	payloadHash, err := serviceauth.PayloadHash(payload)
	if err != nil {
		return nil, fmt.Errorf("hash service request: %w", err)
	}
	authenticated, err := c.authenticator.AuthenticateServiceCall(ctx, tenantID, c.botUsername, fullMethod, payloadHash)
	if err != nil {
		return nil, fmt.Errorf("authenticate service request: %w", err)
	}
	return authenticated, nil
}

func approvalFact(approval *logicv1.AgentApproval) (*mutation.ApprovalFact, error) {
	if approval == nil || approval.GetTenantId() == "" || approval.GetCallId() == "" || approval.GetArgsHash() == "" {
		return nil, fmt.Errorf("logic returned incomplete approval fact")
	}
	statusValue, decisionValue := approvalStatus(approval.GetStatus()), approvalDecision(approval.GetDecision())
	return &mutation.ApprovalFact{
		TenantID: approval.GetTenantId(), RunID: approval.GetRunId(), CallID: approval.GetCallId(),
		ToolName: approval.GetToolName(), RequesterID: approval.GetRequesterId(), ArgsHash: approval.GetArgsHash(),
		ArgsSummary: approval.GetArgsSummary(), Status: statusValue, Decision: decisionValue,
		DecisionBy: approval.GetDecisionBy(), Version: approval.GetVersion(),
		ExpiresAt: time.UnixMilli(approval.GetExpiresAtMs()).UTC(),
		DecidedAt: unixMilliOrZero(approval.GetDecidedAtMs()), CreatedAt: unixMilliOrZero(approval.GetCreatedAtMs()),
	}, nil
}

func approvalStatus(value logicv1.AgentApprovalStatus) string {
	switch value {
	case logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_PENDING:
		return model.AgentApprovalStatusPending
	case logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_APPROVED:
		return model.AgentApprovalStatusApproved
	case logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REJECTED:
		return model.AgentApprovalStatusRejected
	case logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REVOKED:
		return model.AgentApprovalStatusRevoked
	case logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_EXPIRED:
		return model.AgentApprovalStatusExpired
	default:
		return ""
	}
}

func approvalDecision(value logicv1.AgentApprovalDecision) string {
	switch value {
	case logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_APPROVE:
		return model.AgentApprovalDecisionApprove
	case logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_REJECT:
		return model.AgentApprovalDecisionReject
	default:
		return model.AgentApprovalDecisionNone
	}
}

func unixMilliOrZero(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}
