package service

import (
	"context"
	"errors"
	"time"

	"github.com/ceyewan/genesis/clog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/pkg/agentmutation"
	"github.com/ceyewan/resonance/repo"
)

type AgentIAMMutationPolicy struct {
	PilotServiceID string
	WriteScope     string
	DecideScope    string
}

type AgentIAMMutationServiceOption func(*AgentIAMMutationService)

func WithAgentIAMMutationClock(now func() time.Time) AgentIAMMutationServiceOption {
	return func(service *AgentIAMMutationService) {
		if now != nil {
			service.now = now
		}
	}
}

// AgentIAMMutationService 是审批后唯一的 IAM 写入边界。
// 事件仅作为唤醒信号；本服务总是回查审批和执行发起人的当前权威 Scope。
type AgentIAMMutationService struct {
	logicv1.UnimplementedAgentIAMMutationServiceServer
	approvals interface {
		GetAgentApproval(context.Context, string, string) (*model.AgentApproval, error)
	}
	mutations  repo.AgentIAMMutationRepo
	authorizer SystemScopeAuthorizer
	logger     clog.Logger
	policy     AgentIAMMutationPolicy
	now        func() time.Time
}

func NewAgentIAMMutationService(
	approvals interface {
		GetAgentApproval(context.Context, string, string) (*model.AgentApproval, error)
	},
	mutations repo.AgentIAMMutationRepo,
	authorizer SystemScopeAuthorizer,
	logger clog.Logger,
	policy AgentIAMMutationPolicy,
	options ...AgentIAMMutationServiceOption,
) *AgentIAMMutationService {
	if authorizer == nil {
		authorizer = DenyAllSystemScopeAuthorizer{}
	}
	if policy.WriteScope == "" {
		policy.WriteScope = model.ScopeIAMUsersWrite
	}
	if policy.DecideScope == "" {
		policy.DecideScope = model.ScopeAgentApprovalDecide
	}
	service := &AgentIAMMutationService{
		approvals: approvals, mutations: mutations, authorizer: authorizer, logger: logger,
		policy: policy, now: time.Now,
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *AgentIAMMutationService) PreviewTenantMembershipStatus(
	ctx context.Context,
	req *logicv1.PreviewTenantMembershipStatusRequest,
) (*logicv1.PreviewTenantMembershipStatusResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if err := s.requirePilot(ctx, req.GetTenantId()); err != nil {
		return nil, err
	}
	args := agentmutation.NewMembershipStatusArgs(
		req.GetTenantId(), req.GetRunId(), req.GetCallId(), req.GetRequesterId(), req.GetTargetUsername(),
		req.GetDesiredStatus(), req.GetExpectedVersion(), req.GetDryRun(),
	)
	hash, err := args.Hash()
	if err != nil || !req.GetDryRun() {
		return nil, status.Error(codes.InvalidArgument, "dry_run preview binding is invalid")
	}
	allowed, err := s.authorizer.HasSystemScope(ctx, args.TenantID, args.RequesterID, s.policy.WriteScope)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "authorization source unavailable")
	}
	if !allowed {
		return nil, status.Error(codes.PermissionDenied, "requester lacks IAM write scope")
	}
	preview, err := s.mutations.PreviewTenantMembershipStatus(ctx, repo.AgentIAMMembershipPreview{
		TenantID: args.TenantID, RunID: args.RunID, CallID: args.CallID, RequesterID: args.RequesterID,
		TargetUsername: args.TargetUsername, DesiredStatus: args.DesiredStatus, ExpectedVersion: args.ExpectedVersion,
	})
	if err != nil {
		return nil, mapAgentIAMMutationError(err)
	}
	return &logicv1.PreviewTenantMembershipStatusResponse{
		TargetUsername: preview.TargetUsername, CurrentStatus: preview.CurrentStatus,
		DesiredStatus: preview.DesiredStatus, CurrentVersion: preview.CurrentVersion,
		WouldChange: preview.WouldChange, ArgsHash: hash,
	}, nil
}

func (s *AgentIAMMutationService) GetExecutionApproval(ctx context.Context, req *logicv1.GetExecutionApprovalRequest) (*logicv1.GetExecutionApprovalResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if err := s.requirePilot(ctx, req.GetTenantId()); err != nil {
		return nil, err
	}
	if !validIdentifier(req.GetTenantId(), 64) || !validIdentifier(req.GetCallId(), 128) || !validSHA256Hex(req.GetArgsHash()) {
		return nil, status.Error(codes.InvalidArgument, "tenant_id, call_id and args_hash are required")
	}
	approval, err := s.approvals.GetAgentApproval(ctx, req.GetTenantId(), req.GetCallId())
	if err != nil {
		return nil, mapApprovalReadError(err)
	}
	if approval.ArgsHash != req.GetArgsHash() {
		return nil, status.Error(codes.FailedPrecondition, "approval args_hash binding differs")
	}
	return &logicv1.GetExecutionApprovalResponse{Approval: approvalToProto(approval)}, nil
}

func (s *AgentIAMMutationService) ExecuteTenantMembershipStatus(
	ctx context.Context,
	req *logicv1.ExecuteTenantMembershipStatusRequest,
) (*logicv1.ExecuteTenantMembershipStatusResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if err := s.requirePilot(ctx, req.GetTenantId()); err != nil {
		return nil, err
	}
	args := agentmutation.NewMembershipStatusArgs(
		req.GetTenantId(), req.GetRunId(), req.GetCallId(), req.GetRequesterId(), req.GetTargetUsername(),
		req.GetDesiredStatus(), req.GetExpectedVersion(), req.GetDryRun(),
	)
	hash, err := args.Hash()
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if req.GetToolName() != agentmutation.MembershipStatusTool || req.GetArgsHash() != hash ||
		req.GetIdempotencyKey() != agentmutation.IdempotencyKey(req.GetTenantId(), req.GetCallId()) || req.GetApprovalVersion() < 1 || req.GetDryRun() {
		return nil, status.Error(codes.FailedPrecondition, "mutation binding differs from frozen arguments")
	}
	mutationRequest := repo.AgentIAMMembershipMutation{
		TenantID: req.GetTenantId(), RunID: req.GetRunId(), CallID: req.GetCallId(), ArgsHash: req.GetArgsHash(),
		ToolName: req.GetToolName(), IdempotencyKey: req.GetIdempotencyKey(), RequesterID: req.GetRequesterId(), TargetUsername: req.GetTargetUsername(),
		DesiredStatus: args.DesiredStatus, ExpectedVersion: req.GetExpectedVersion(), ApprovalVersion: req.GetApprovalVersion(),
		OccurredAt: s.now().UTC(),
	}
	// 已提交收据优先于当前审批/权限状态，用于恢复“提交成功但响应丢失”。
	if replay, replayErr := s.mutations.GetTenantMembershipStatusResult(ctx, mutationRequest); replayErr == nil {
		if replay == nil || replay.Receipt == nil {
			return nil, status.Error(codes.Internal, "IAM mutation repository returned no receipt")
		}
		return agentIAMMutationResponse(replay), nil
	} else if !errors.Is(replayErr, repo.ErrAgentIAMMutationReceiptNotFound) {
		return nil, mapAgentIAMMutationError(replayErr)
	}

	approval, err := s.approvals.GetAgentApproval(ctx, req.GetTenantId(), req.GetCallId())
	if err != nil {
		return nil, mapApprovalReadError(err)
	}
	if approval.RunID != req.GetRunId() || approval.ToolName != req.GetToolName() || approval.RequesterID != req.GetRequesterId() || approval.ArgsHash != req.GetArgsHash() ||
		approval.Status != model.AgentApprovalStatusApproved || approval.Decision != model.AgentApprovalDecisionApprove ||
		approval.Version != req.GetApprovalVersion() || approval.RevokedAt != nil || !s.now().UTC().Before(approval.ExpiresAt) {
		return nil, status.Error(codes.FailedPrecondition, "approval is not currently executable")
	}
	allowed, err := s.authorizer.HasSystemScope(ctx, approval.TenantID, approval.RequesterID, s.policy.WriteScope)
	if err != nil {
		s.logger.Error("failed to recheck IAM mutation requester scope", clog.Error(err))
		return nil, status.Error(codes.Unavailable, "authorization source unavailable")
	}
	if !allowed {
		return nil, status.Error(codes.PermissionDenied, "requester no longer has IAM write scope")
	}
	allowed, err = s.authorizer.HasSystemScope(ctx, approval.TenantID, approval.DecisionBy, s.policy.DecideScope)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "authorization source unavailable")
	}
	if !allowed {
		return nil, status.Error(codes.PermissionDenied, "approver no longer has approval decide scope")
	}

	result, err := s.mutations.ExecuteTenantMembershipStatus(ctx, mutationRequest)
	if err != nil {
		return nil, mapAgentIAMMutationError(err)
	}
	if result == nil || result.Receipt == nil {
		return nil, status.Error(codes.Internal, "IAM mutation repository returned no receipt")
	}
	return agentIAMMutationResponse(result), nil
}

func agentIAMMutationResponse(result *repo.AgentIAMMutationResult) *logicv1.ExecuteTenantMembershipStatusResponse {
	receipt := result.Receipt
	return &logicv1.ExecuteTenantMembershipStatusResponse{
		OperationId: receipt.OperationID, TargetUsername: receipt.TargetUsername,
		PreviousStatus: receipt.PreviousStatus, ResultStatus: receipt.ResultStatus,
		PreviousVersion: receipt.PreviousVersion, ResultVersion: receipt.ResultVersion,
		ApprovalVersion: receipt.ApprovalVersion, CommittedAtMs: receipt.DownstreamCommittedAt.UnixMilli(),
		Repeated: result.Repeated,
	}
}

func (s *AgentIAMMutationService) requirePilot(ctx context.Context, requestTenantID string) error {
	serviceID, principalTenantID, ok := ServicePrincipalFromCtx(ctx)
	if !ok || serviceID == "" || serviceID != s.policy.PilotServiceID {
		return status.Error(codes.PermissionDenied, "IAM mutation requires trusted pilot service identity")
	}
	if requestTenantID == "" || principalTenantID != requestTenantID {
		return status.Error(codes.PermissionDenied, "service tenant does not match mutation tenant")
	}
	return nil
}

func mapAgentIAMMutationError(err error) error {
	switch {
	case errors.Is(err, repo.ErrAgentApprovalNotFound):
		return status.Error(codes.NotFound, "approval not found")
	case errors.Is(err, repo.ErrAgentIAMMutationNotAllowed), errors.Is(err, repo.ErrAgentApprovalExpired):
		return status.Error(codes.PermissionDenied, "IAM mutation is not allowed")
	case errors.Is(err, repo.ErrAgentIAMMutationConflict), errors.Is(err, repo.ErrAgentStateConflict), errors.Is(err, repo.ErrIdentityVersionConflict):
		return status.Error(codes.FailedPrecondition, "IAM mutation conflicts with current state")
	default:
		return status.Error(codes.Internal, "failed to execute IAM mutation")
	}
}

func validSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
