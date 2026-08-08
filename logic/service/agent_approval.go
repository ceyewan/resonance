package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/mq"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
	mqv1 "github.com/ceyewan/resonance/api/gen/go/mq/v1"
	"github.com/ceyewan/resonance/logic/internal/mqpublish"
	"github.com/ceyewan/resonance/logic/observability"
	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/repo"
)

const (
	defaultAgentApprovalPageSize = 50
	maxAgentApprovalPageSize     = 100
)

// AgentApprovalStore 是 Logic 使用的最窄审批存储接口。
// 原子方法保证事实变更与对应事件只能同时提交。
type AgentApprovalStore interface {
	CreateAgentApprovalWithOutbox(context.Context, *model.AgentApproval, *model.MessageOutbox) (*repo.AgentApprovalCreateResult, error)
	GetAgentApproval(context.Context, string, string) (*model.AgentApproval, error)
	ListAgentApprovals(context.Context, repo.AgentApprovalListFilter) ([]*model.AgentApproval, error)
	TransitionAgentApprovalWithOutbox(context.Context, repo.AgentApprovalTransition, *model.MessageOutbox) (*repo.AgentApprovalTransitionResult, error)
}

// SystemScopeAuthorizer 必须查询权威 IAM/System Scope 数据源。
// 实现不得根据请求字段或缓存的聊天角色推导权限。
type SystemScopeAuthorizer interface {
	HasSystemScope(ctx context.Context, tenantID, actorID, scope string) (bool, error)
}

// DenyAllSystemScopeAuthorizer 是 IAM Repo 接通前的 fail-closed 组装实现，
// 它不会授予任何管理 Scope。
type DenyAllSystemScopeAuthorizer struct{}

func (DenyAllSystemScopeAuthorizer) HasSystemScope(context.Context, string, string, string) (bool, error) {
	return false, nil
}

type AgentApprovalPolicy struct {
	PilotServiceID   string
	ReadScope        string
	DecideScope      string
	AllowSelfApprove bool
}

type AgentApprovalServiceOption func(*AgentApprovalService)

func WithAgentApprovalClock(now func() time.Time) AgentApprovalServiceOption {
	return func(service *AgentApprovalService) {
		if now != nil {
			service.now = now
		}
	}
}

// AgentApprovalService 只持有审批事实，绝不调用 Tool。
type AgentApprovalService struct {
	logicv1.UnimplementedAgentApprovalServiceServer
	store      AgentApprovalStore
	authorizer SystemScopeAuthorizer
	eventIDGen idgen.Generator
	mqClient   mq.MQ
	logger     clog.Logger
	policy     AgentApprovalPolicy
	now        func() time.Time
}

func NewAgentApprovalService(
	store AgentApprovalStore,
	authorizer SystemScopeAuthorizer,
	eventIDGen idgen.Generator,
	mqClient mq.MQ,
	logger clog.Logger,
	policy AgentApprovalPolicy,
	options ...AgentApprovalServiceOption,
) *AgentApprovalService {
	if authorizer == nil {
		authorizer = DenyAllSystemScopeAuthorizer{}
	}
	if policy.ReadScope == "" {
		policy.ReadScope = model.ScopeAgentApprovalRead
	}
	if policy.DecideScope == "" {
		policy.DecideScope = model.ScopeAgentApprovalDecide
	}
	service := &AgentApprovalService{
		store:      store,
		authorizer: authorizer,
		eventIDGen: eventIDGen,
		mqClient:   mqClient,
		logger:     logger,
		policy:     policy,
		now:        time.Now,
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *AgentApprovalService) CreateApproval(ctx context.Context, req *logicv1.CreateApprovalRequest) (*logicv1.CreateApprovalResponse, error) {
	serviceID, principalTenantID, ok := ServicePrincipalFromCtx(ctx)
	if !ok || serviceID == "" || serviceID != s.policy.PilotServiceID {
		return nil, status.Error(codes.PermissionDenied, "approval creation requires trusted pilot service identity")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if principalTenantID != req.GetTenantId() {
		return nil, status.Error(codes.PermissionDenied, "service tenant does not match approval tenant")
	}

	now := s.now().UTC()
	approval, err := approvalFromCreateRequest(req, now)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if !approval.ExpiresAt.After(now) {
		existing, lookupErr := s.store.GetAgentApproval(ctx, approval.TenantID, approval.CallID)
		switch {
		case lookupErr == nil && sameApprovalCreate(existing, approval):
			return &logicv1.CreateApprovalResponse{Approval: approvalToProto(existing), Created: false}, nil
		case lookupErr == nil:
			return nil, status.Error(codes.AlreadyExists, "call_id is already bound to another approval")
		case errors.Is(lookupErr, repo.ErrAgentApprovalNotFound):
			return nil, status.Error(codes.InvalidArgument, "expires_at_ms must be in the future")
		default:
			return nil, status.Error(codes.Internal, "failed to check approval idempotency")
		}
	}
	eventID, err := s.nextEventID()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate approval event id")
	}
	event := &mqv1.AgentApprovalRequestedEvent{
		EventId:         eventID,
		TenantId:        approval.TenantID,
		RunId:           approval.RunID,
		CallId:          approval.CallID,
		ToolName:        approval.ToolName,
		RequesterId:     approval.RequesterID,
		ArgsHash:        approval.ArgsHash,
		ArgsSummary:     approval.ArgsSummary,
		ExpiresAtMs:     approval.ExpiresAt.UnixMilli(),
		ApprovalVersion: 1,
		RequestedAtMs:   now.UnixMilli(),
		TraceHeaders:    make(map[string]string),
	}
	observability.InjectTraceContext(ctx, event.TraceHeaders)
	outbox, eventData, topic, err := approvalOutbox(eventID, event, now)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to encode approval requested event")
	}

	result, err := s.store.CreateAgentApprovalWithOutbox(ctx, approval, outbox)
	if err != nil {
		if errors.Is(err, repo.ErrAgentStateConflict) {
			return nil, status.Error(codes.AlreadyExists, "call_id is already bound to another approval")
		}
		s.logger.Error("failed to create approval", clog.Error(err))
		return nil, status.Error(codes.Internal, "failed to create approval")
	}
	if result == nil || result.Approval == nil {
		return nil, status.Error(codes.Internal, "approval repository returned no result")
	}
	if result.Created {
		if result.Outbox == nil {
			return nil, status.Error(codes.Internal, "created approval has no outbox")
		}
		s.publishAsync(result.Outbox.ID, topic, eventData)
	}
	return &logicv1.CreateApprovalResponse{Approval: approvalToProto(result.Approval), Created: result.Created}, nil
}

func (s *AgentApprovalService) DecideApproval(ctx context.Context, req *logicv1.DecideApprovalRequest) (*logicv1.DecideApprovalResponse, error) {
	actorID, principalTenantID, err := authenticatedUser(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateDecideApprovalRequest(req); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if principalTenantID != req.GetTenantId() {
		return nil, status.Error(codes.PermissionDenied, "authenticated tenant does not match approval tenant")
	}
	if err := s.requireScope(ctx, req.GetTenantId(), actorID, s.policy.DecideScope); err != nil {
		return nil, err
	}

	current, err := s.store.GetAgentApproval(ctx, req.GetTenantId(), req.GetCallId())
	if err != nil {
		return nil, mapApprovalReadError(err)
	}
	if current.ArgsHash != req.GetArgsHash() {
		return nil, status.Error(codes.FailedPrecondition, "approval binding does not match args_hash")
	}
	if !s.policy.AllowSelfApprove && current.RequesterID == actorID {
		return nil, status.Error(codes.PermissionDenied, "requester cannot decide their own approval")
	}

	nextStatus, mqDecision := decisionStatus(req.GetDecision())
	now := s.now().UTC()
	eventID, err := s.nextEventID()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate approval event id")
	}
	event := &mqv1.AgentApprovalDecidedEvent{
		EventId:         eventID,
		TenantId:        current.TenantID,
		RunId:           current.RunID,
		CallId:          current.CallID,
		ArgsHash:        current.ArgsHash,
		ApprovalVersion: req.GetExpectedVersion() + 1,
		Decision:        mqDecision,
		DecisionBy:      actorID,
		Reason:          req.GetReason(),
		DecidedAtMs:     now.UnixMilli(),
		TraceHeaders:    make(map[string]string),
	}
	observability.InjectTraceContext(ctx, event.TraceHeaders)
	outbox, eventData, topic, err := approvalOutbox(eventID, event, now)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to encode approval decided event")
	}
	result, err := s.store.TransitionAgentApprovalWithOutbox(ctx, repo.AgentApprovalTransition{
		TenantID:        current.TenantID,
		CallID:          current.CallID,
		ArgsHash:        current.ArgsHash,
		ExpectedStatus:  model.AgentApprovalStatusPending,
		ExpectedVersion: req.GetExpectedVersion(),
		NextStatus:      nextStatus,
		ActorID:         actorID,
		Reason:          req.GetReason(),
		OccurredAt:      now,
	}, outbox)
	if err != nil {
		return nil, mapApprovalTransitionError(err)
	}
	if result == nil || result.Approval == nil {
		return nil, status.Error(codes.Internal, "approval repository returned no result")
	}
	if result.Changed {
		if result.Outbox == nil {
			return nil, status.Error(codes.Internal, "changed approval has no outbox")
		}
		s.publishAsync(result.Outbox.ID, topic, eventData)
	}
	return &logicv1.DecideApprovalResponse{Approval: approvalToProto(result.Approval), Changed: result.Changed}, nil
}

func (s *AgentApprovalService) GetApproval(ctx context.Context, req *logicv1.GetApprovalRequest) (*logicv1.GetApprovalResponse, error) {
	actorID, principalTenantID, err := authenticatedUser(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil || !validIdentifier(req.GetTenantId(), 64) || !validIdentifier(req.GetCallId(), 128) {
		return nil, status.Error(codes.InvalidArgument, "tenant_id and call_id are required")
	}
	if principalTenantID != req.GetTenantId() {
		return nil, status.Error(codes.PermissionDenied, "authenticated tenant does not match approval tenant")
	}
	approval, err := s.store.GetAgentApproval(ctx, req.GetTenantId(), req.GetCallId())
	if err != nil {
		return nil, mapApprovalReadError(err)
	}
	if approval.RequesterID != actorID {
		if err := s.requireScope(ctx, req.GetTenantId(), actorID, s.policy.ReadScope); err != nil {
			return nil, err
		}
	}
	return &logicv1.GetApprovalResponse{Approval: approvalToProto(approval)}, nil
}

func (s *AgentApprovalService) ListApprovals(ctx context.Context, req *logicv1.ListApprovalsRequest) (*logicv1.ListApprovalsResponse, error) {
	actorID, principalTenantID, err := authenticatedUser(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil || !validIdentifier(req.GetTenantId(), 64) {
		return nil, status.Error(codes.InvalidArgument, "tenant_id is required")
	}
	if principalTenantID != req.GetTenantId() {
		return nil, status.Error(codes.PermissionDenied, "authenticated tenant does not match approval tenant")
	}
	if req.GetBeforeId() < 0 || req.GetPageSize() < 0 || req.GetPageSize() > maxAgentApprovalPageSize {
		return nil, status.Error(codes.InvalidArgument, "invalid approval page")
	}
	statusFilter, err := approvalStatusFromProto(req.GetStatus())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.requireScope(ctx, req.GetTenantId(), actorID, s.policy.ReadScope); err != nil {
		return nil, err
	}
	pageSize := int(req.GetPageSize())
	if pageSize == 0 {
		pageSize = defaultAgentApprovalPageSize
	}
	approvals, err := s.store.ListAgentApprovals(ctx, repo.AgentApprovalListFilter{
		TenantID: req.GetTenantId(),
		Status:   statusFilter,
		BeforeID: req.GetBeforeId(),
		Limit:    pageSize,
	})
	if err != nil {
		s.logger.Error("failed to list approvals", clog.Error(err))
		return nil, status.Error(codes.Internal, "failed to list approvals")
	}
	response := &logicv1.ListApprovalsResponse{Approvals: make([]*logicv1.AgentApproval, 0, len(approvals))}
	for _, approval := range approvals {
		response.Approvals = append(response.Approvals, approvalToProto(approval))
	}
	if len(approvals) == pageSize {
		response.NextBeforeId = approvals[len(approvals)-1].ID
	}
	return response, nil
}

func authenticatedUser(ctx context.Context) (string, string, error) {
	if _, _, ok := ServicePrincipalFromCtx(ctx); ok {
		return "", "", status.Error(codes.PermissionDenied, "operation requires an authenticated user")
	}
	principal, ok := UserPrincipalFromCtx(ctx)
	if !ok {
		return "", "", status.Error(codes.Unauthenticated, "missing authenticated user principal")
	}
	return principal.Username, principal.TenantID, nil
}

func (s *AgentApprovalService) requireScope(ctx context.Context, tenantID, actorID, scope string) error {
	allowed, err := s.authorizer.HasSystemScope(ctx, tenantID, actorID, scope)
	if err != nil {
		s.logger.Error("failed to query authoritative system scope", clog.Error(err))
		return status.Error(codes.Unavailable, "authorization source unavailable")
	}
	if !allowed {
		return status.Error(codes.PermissionDenied, "required system scope is missing")
	}
	return nil
}

func (s *AgentApprovalService) nextEventID() (int64, error) {
	if s.eventIDGen == nil {
		return 0, fmt.Errorf("event id generator is nil")
	}
	return s.eventIDGen.Next()
}

func (s *AgentApprovalService) publishAsync(outboxID int64, topic string, data []byte) {
	if s.mqClient == nil {
		return
	}
	mqpublish.PublishMessageToMQAsync(s.mqClient, outboxID, topic, data, s.logger)
}

func approvalFromCreateRequest(req *logicv1.CreateApprovalRequest, now time.Time) (*model.AgentApproval, error) {
	if !validIdentifier(req.GetTenantId(), 64) || !validIdentifier(req.GetRunId(), 64) || !validIdentifier(req.GetCallId(), 128) {
		return nil, fmt.Errorf("tenant_id, run_id and call_id are required and must be bounded")
	}
	if !validIdentifier(req.GetToolName(), 128) || !validIdentifier(req.GetRequesterId(), 64) {
		return nil, fmt.Errorf("tool_name and requester_id are required and must be bounded")
	}
	if !validApprovalHash(req.GetArgsHash()) {
		return nil, fmt.Errorf("args_hash must be a lowercase SHA-256 hex digest")
	}
	if strings.TrimSpace(req.GetArgsSummary()) == "" || len(req.GetArgsSummary()) > 4096 {
		return nil, fmt.Errorf("args_summary is required and must not exceed 4096 bytes")
	}
	expiresAt := time.UnixMilli(req.GetExpiresAtMs()).UTC()
	if req.GetExpiresAtMs() <= 0 {
		return nil, fmt.Errorf("expires_at_ms must be positive")
	}
	return &model.AgentApproval{
		TenantID:    req.GetTenantId(),
		RunID:       req.GetRunId(),
		CallID:      req.GetCallId(),
		ToolName:    req.GetToolName(),
		RequesterID: req.GetRequesterId(),
		ArgsHash:    req.GetArgsHash(),
		ArgsSummary: req.GetArgsSummary(),
		ExpiresAt:   expiresAt,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func sameApprovalCreate(existing, candidate *model.AgentApproval) bool {
	return existing != nil && candidate != nil &&
		existing.TenantID == candidate.TenantID && existing.RunID == candidate.RunID &&
		existing.CallID == candidate.CallID && existing.ToolName == candidate.ToolName &&
		existing.RequesterID == candidate.RequesterID && existing.ArgsHash == candidate.ArgsHash &&
		existing.ArgsSummary == candidate.ArgsSummary && existing.ExpiresAt.Equal(candidate.ExpiresAt)
}

func validateDecideApprovalRequest(req *logicv1.DecideApprovalRequest) error {
	if req == nil {
		return fmt.Errorf("request is required")
	}
	if !validIdentifier(req.GetTenantId(), 64) || !validIdentifier(req.GetCallId(), 128) {
		return fmt.Errorf("tenant_id and call_id are required and must be bounded")
	}
	if !validApprovalHash(req.GetArgsHash()) {
		return fmt.Errorf("args_hash must be a lowercase SHA-256 hex digest")
	}
	if req.GetExpectedVersion() <= 0 {
		return fmt.Errorf("expected_version must be positive")
	}
	if req.GetDecision() != logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_APPROVE &&
		req.GetDecision() != logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_REJECT {
		return fmt.Errorf("decision must be APPROVE or REJECT")
	}
	if len(req.GetReason()) > 512 {
		return fmt.Errorf("reason must not exceed 512 bytes")
	}
	return nil
}

func decisionStatus(decision logicv1.AgentApprovalDecision) (string, mqv1.AgentApprovalDecision) {
	if decision == logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_REJECT {
		return model.AgentApprovalStatusRejected, mqv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_REJECT
	}
	return model.AgentApprovalStatusApproved, mqv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_APPROVE
}

func approvalOutbox(eventID int64, event proto.Message, now time.Time) (*model.MessageOutbox, []byte, string, error) {
	payload, err := proto.Marshal(event)
	if err != nil {
		return nil, nil, "", err
	}
	value := proto.GetExtension(event.ProtoReflect().Descriptor().Options(), commonv1.E_DefaultTopic)
	topic, ok := value.(string)
	if !ok || topic == "" {
		return nil, nil, "", fmt.Errorf("approval event has no default topic")
	}
	return &model.MessageOutbox{
		EventID:       eventID,
		Topic:         topic,
		Payload:       payload,
		Status:        model.OutboxStatusPending,
		NextRetryTime: now,
	}, payload, topic, nil
}

func mapApprovalReadError(err error) error {
	if errors.Is(err, repo.ErrAgentApprovalNotFound) {
		return status.Error(codes.NotFound, "approval not found")
	}
	return status.Error(codes.Internal, "failed to read approval")
}

func mapApprovalTransitionError(err error) error {
	switch {
	case errors.Is(err, repo.ErrAgentApprovalNotFound):
		return status.Error(codes.NotFound, "approval not found")
	case errors.Is(err, repo.ErrAgentApprovalExpired):
		return status.Error(codes.FailedPrecondition, "approval has expired")
	case errors.Is(err, repo.ErrAgentInvalidTransition):
		return status.Error(codes.FailedPrecondition, "approval is no longer pending")
	case errors.Is(err, repo.ErrAgentStateConflict):
		return status.Error(codes.Aborted, "approval version or binding conflict")
	default:
		return status.Error(codes.Internal, "failed to decide approval")
	}
}

func approvalToProto(approval *model.AgentApproval) *logicv1.AgentApproval {
	if approval == nil {
		return nil
	}
	result := &logicv1.AgentApproval{
		Id:             approval.ID,
		TenantId:       approval.TenantID,
		RunId:          approval.RunID,
		CallId:         approval.CallID,
		ToolName:       approval.ToolName,
		RequesterId:    approval.RequesterID,
		ArgsHash:       approval.ArgsHash,
		ArgsSummary:    approval.ArgsSummary,
		Status:         approvalStatusToProto(approval.Status),
		Decision:       approvalDecisionToProto(approval.Decision),
		DecisionBy:     approval.DecisionBy,
		DecisionReason: approval.DecisionReason,
		ExpiresAtMs:    approval.ExpiresAt.UnixMilli(),
		Version:        approval.Version,
		CreatedAtMs:    approval.CreatedAt.UnixMilli(),
		UpdatedAtMs:    approval.UpdatedAt.UnixMilli(),
	}
	if approval.DecidedAt != nil {
		result.DecidedAtMs = approval.DecidedAt.UnixMilli()
	}
	return result
}

func approvalStatusToProto(value string) logicv1.AgentApprovalStatus {
	switch value {
	case model.AgentApprovalStatusPending:
		return logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_PENDING
	case model.AgentApprovalStatusApproved:
		return logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_APPROVED
	case model.AgentApprovalStatusRejected:
		return logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REJECTED
	case model.AgentApprovalStatusRevoked:
		return logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REVOKED
	case model.AgentApprovalStatusExpired:
		return logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_EXPIRED
	default:
		return logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_UNSPECIFIED
	}
}

func approvalStatusFromProto(value logicv1.AgentApprovalStatus) (string, error) {
	switch value {
	case logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_UNSPECIFIED:
		return "", nil
	case logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_PENDING:
		return model.AgentApprovalStatusPending, nil
	case logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_APPROVED:
		return model.AgentApprovalStatusApproved, nil
	case logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REJECTED:
		return model.AgentApprovalStatusRejected, nil
	case logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_REVOKED:
		return model.AgentApprovalStatusRevoked, nil
	case logicv1.AgentApprovalStatus_AGENT_APPROVAL_STATUS_EXPIRED:
		return model.AgentApprovalStatusExpired, nil
	default:
		return "", fmt.Errorf("invalid approval status")
	}
}

func approvalDecisionToProto(value string) logicv1.AgentApprovalDecision {
	switch value {
	case model.AgentApprovalDecisionApprove:
		return logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_APPROVE
	case model.AgentApprovalDecisionReject:
		return logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_REJECT
	default:
		return logicv1.AgentApprovalDecision_AGENT_APPROVAL_DECISION_UNSPECIFIED
	}
}

func validApprovalHash(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func validIdentifier(value string, maxBytes int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maxBytes
}
