package httpapi

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/gateway/middleware"
)

type approvalLogicClient interface {
	GetAgentApproval(context.Context, *logicv1.GetApprovalRequest) (*logicv1.GetApprovalResponse, error)
	ListAgentApprovals(context.Context, *logicv1.ListApprovalsRequest) (*logicv1.ListApprovalsResponse, error)
	DecideAgentApproval(context.Context, *logicv1.DecideApprovalRequest) (*logicv1.DecideApprovalResponse, error)
}

// GetApproval injects tenant from the verified request Principal. The public
// request deliberately has no tenant field.
func (h *HTTPHandler) GetApproval(
	ctx context.Context,
	req *connect.Request[gatewayv1.GetApprovalRequest],
) (*connect.Response[gatewayv1.GetApprovalResponse], error) {
	tenantID, err := trustedRequestTenant(ctx)
	if err != nil {
		return nil, err
	}
	response, err := h.approvalClient.GetAgentApproval(ctx, &logicv1.GetApprovalRequest{
		TenantId: tenantID,
		CallId:   req.Msg.GetCallId(),
	})
	if err != nil {
		return nil, toConnectError(err)
	}
	approval, err := publicApproval(response.GetApproval(), tenantID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&gatewayv1.GetApprovalResponse{Approval: approval}), nil
}

// ListApprovals is tenant-scoped by trusted context and supports stable
// before_id pagination.
func (h *HTTPHandler) ListApprovals(
	ctx context.Context,
	req *connect.Request[gatewayv1.ListApprovalsRequest],
) (*connect.Response[gatewayv1.ListApprovalsResponse], error) {
	tenantID, err := trustedRequestTenant(ctx)
	if err != nil {
		return nil, err
	}
	statusFilter, ok := logicApprovalStatus(req.Msg.GetStatus())
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid approval status"))
	}
	response, err := h.approvalClient.ListAgentApprovals(ctx, &logicv1.ListApprovalsRequest{
		TenantId: tenantID,
		Status:   statusFilter,
		BeforeId: req.Msg.GetBeforeId(),
		PageSize: req.Msg.GetPageSize(),
	})
	if err != nil {
		return nil, toConnectError(err)
	}
	approvals := make([]*gatewayv1.AgentApproval, 0, len(response.GetApprovals()))
	for _, approval := range response.GetApprovals() {
		redacted, mapErr := publicApproval(approval, tenantID)
		if mapErr != nil {
			return nil, mapErr
		}
		approvals = append(approvals, redacted)
	}
	return connect.NewResponse(&gatewayv1.ListApprovalsResponse{
		Approvals:    approvals,
		NextBeforeId: response.GetNextBeforeId(),
	}), nil
}

// DecideApproval keeps the decision bound to the exact args_hash and expected
// version returned by the read surface. Logic still performs authoritative
// scope, expiry, transition and self-approval checks.
func (h *HTTPHandler) DecideApproval(
	ctx context.Context,
	req *connect.Request[gatewayv1.DecideApprovalRequest],
) (*connect.Response[gatewayv1.DecideApprovalResponse], error) {
	tenantID, err := trustedRequestTenant(ctx)
	if err != nil {
		return nil, err
	}
	decision, ok := logicApprovalDecision(req.Msg.GetDecision())
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid approval decision"))
	}
	response, err := h.approvalClient.DecideAgentApproval(ctx, &logicv1.DecideApprovalRequest{
		TenantId:        tenantID,
		CallId:          req.Msg.GetCallId(),
		ArgsHash:        req.Msg.GetArgsHash(),
		ExpectedVersion: req.Msg.GetExpectedVersion(),
		Decision:        decision,
		Reason:          req.Msg.GetReason(),
	})
	if err != nil {
		return nil, toConnectError(err)
	}
	approval, err := publicApproval(response.GetApproval(), tenantID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&gatewayv1.DecideApprovalResponse{
		Approval: approval,
		Changed:  response.GetChanged(),
	}), nil
}

func trustedRequestTenant(ctx context.Context) (string, error) {
	principal, ok := middleware.PrincipalFromRequestContext(ctx)
	if !ok || principal.TenantID == "" || principal.Username == "" || principal.MembershipVersion < 1 {
		return "", connect.NewError(connect.CodeUnauthenticated, middleware.ErrMissingToken)
	}
	return principal.TenantID, nil
}

func logicApprovalStatus(status gatewayv1.AgentApprovalStatus) (logicv1.AgentApprovalStatus, bool) {
	switch status {
	case gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_UNSPECIFIED:
		return logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_UNSPECIFIED, true
	case gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_PENDING:
		return logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_PENDING, true
	case gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_APPROVED:
		return logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_APPROVED, true
	case gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REJECTED:
		return logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REJECTED, true
	case gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REVOKED:
		return logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REVOKED, true
	case gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_EXPIRED:
		return logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_EXPIRED, true
	default:
		return logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_UNSPECIFIED, false
	}
}

func logicApprovalDecision(decision gatewayv1.AgentApprovalDecision) (logicv1.AgentApprovalDecision, bool) {
	switch decision {
	case gatewayv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_APPROVE:
		return logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_APPROVE, true
	case gatewayv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_REJECT:
		return logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_REJECT, true
	default:
		return logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_UNSPECIFIED, false
	}
}

func publicApproval(approval *logicv1.AgentApproval, trustedTenantID string) (*gatewayv1.AgentApproval, error) {
	if approval == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("approval response is missing"))
	}
	if trustedTenantID == "" || approval.GetTenantId() != trustedTenantID {
		return nil, connect.NewError(connect.CodeInternal, errors.New("approval response tenant mismatch"))
	}
	status, ok := publicApprovalStatus(approval.GetStatus())
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("approval response has invalid status"))
	}
	decision, ok := publicApprovalDecision(approval.GetDecision())
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("approval response has invalid decision"))
	}
	return &gatewayv1.AgentApproval{
		Id:             approval.GetId(),
		RunId:          approval.GetRunId(),
		CallId:         approval.GetCallId(),
		ToolName:       approval.GetToolName(),
		RequesterId:    approval.GetRequesterId(),
		ArgsHash:       approval.GetArgsHash(),
		ArgsSummary:    approval.GetArgsSummary(),
		Status:         status,
		Decision:       decision,
		DecisionBy:     approval.GetDecisionBy(),
		DecisionReason: approval.GetDecisionReason(),
		DecidedAtMs:    approval.GetDecidedAtMs(),
		ExpiresAtMs:    approval.GetExpiresAtMs(),
		Version:        approval.GetVersion(),
		CreatedAtMs:    approval.GetCreatedAtMs(),
		UpdatedAtMs:    approval.GetUpdatedAtMs(),
	}, nil
}

func publicApprovalStatus(status logicv1.AgentApprovalStatus) (gatewayv1.AgentApprovalStatus, bool) {
	switch status {
	case logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_UNSPECIFIED:
		return gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_UNSPECIFIED, true
	case logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_PENDING:
		return gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_PENDING, true
	case logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_APPROVED:
		return gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_APPROVED, true
	case logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REJECTED:
		return gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REJECTED, true
	case logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REVOKED:
		return gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REVOKED, true
	case logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_EXPIRED:
		return gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_EXPIRED, true
	default:
		return gatewayv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_UNSPECIFIED, false
	}
}

func publicApprovalDecision(decision logicv1.AgentApprovalDecision) (gatewayv1.AgentApprovalDecision, bool) {
	switch decision {
	case logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_UNSPECIFIED:
		return gatewayv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_UNSPECIFIED, true
	case logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_APPROVE:
		return gatewayv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_APPROVE, true
	case logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_REJECT:
		return gatewayv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_REJECT, true
	default:
		return gatewayv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_UNSPECIFIED, false
	}
}
