// Package mutation 实现 Pilot 的审批后 IAM 写操作协调器。
package mutation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/pilot/runtime"
	"github.com/ceyewan/resonance/pilot/toolbroker"
	"github.com/ceyewan/resonance/pkg/agentmutation"
	"github.com/ceyewan/resonance/repo"
)

var (
	ErrFrozenBindingInvalid  = errors.New("frozen mutation binding is invalid")
	ErrRequesterUnauthorized = errors.New("mutation requester is not authorized")
)

type Config struct {
	TenantID         string
	AgentBotUsername string
	ApprovalTTL      time.Duration
	ReconcileEvery   time.Duration
	BatchSize        int
}

type ApprovalFact struct {
	TenantID    string
	RunID       string
	CallID      string
	ToolName    string
	RequesterID string
	ArgsHash    string
	ArgsSummary string
	Status      string
	Decision    string
	DecisionBy  string
	Version     int64
	ExpiresAt   time.Time
	DecidedAt   time.Time
	CreatedAt   time.Time
}

type CreateApprovalRequest struct {
	TenantID    string
	RunID       string
	CallID      string
	ToolName    string
	RequesterID string
	ArgsHash    string
	ArgsSummary string
	ExpiresAt   time.Time
}

type MutationReceipt struct {
	OperationID     string
	TargetUsername  string
	PreviousStatus  string
	ResultStatus    string
	PreviousVersion int64
	ResultVersion   int64
	ApprovalVersion int64
	CommittedAt     time.Time
	Repeated        bool
}

type MutationPreview struct {
	TargetUsername string
	CurrentStatus  string
	DesiredStatus  string
	CurrentVersion int64
	WouldChange    bool
	ArgsHash       string
}

type LogicClient interface {
	PreviewTenantMembershipStatus(context.Context, agentmutation.MembershipStatusArgs) (*MutationPreview, error)
	CreateApproval(context.Context, CreateApprovalRequest) (*ApprovalFact, bool, error)
	GetExecutionApproval(ctx context.Context, tenantID, callID, argsHash string) (*ApprovalFact, error)
	ExecuteTenantMembershipStatus(context.Context, agentmutation.MembershipStatusArgs, string, int64) (*MutationReceipt, error)
}

type PreparationStore interface {
	PrepareAgentMutation(context.Context, *model.AgentFrozenToolArgs, *model.AgentToolExecution) (*repo.AgentMutationPreparationResult, error)
	GetAgentFrozenToolArgs(ctx context.Context, tenantID, ref string) (*model.AgentFrozenToolArgs, error)
	ListAgentToolExecutions(ctx context.Context, filter repo.AgentToolExecutionListFilter) ([]*model.AgentToolExecution, error)
}

type ExecutionStore interface {
	GetAgentToolExecution(ctx context.Context, tenantID, callID string) (*model.AgentToolExecution, error)
	TransitionAgentToolExecution(context.Context, repo.AgentToolExecutionTransition) (*repo.AgentToolExecutionTransitionResult, error)
}

type AuditStore interface {
	AppendAgentAudit(context.Context, *model.AgentAuditLog) (*repo.AgentAuditAppendResult, error)
}

type PrincipalReader interface {
	ResolvePrincipal(ctx context.Context, tenantID, actorID string) (runtime.ActorPrincipal, error)
}

type Service struct {
	config       Config
	preparations PreparationStore
	executions   ExecutionStore
	audits       AuditStore
	principals   PrincipalReader
	logic        LogicClient
	now          func() time.Time

	locks sync.Map
}

func NewService(
	config Config,
	preparations PreparationStore,
	executions ExecutionStore,
	audits AuditStore,
	principals PrincipalReader,
	logic LogicClient,
) (*Service, error) {
	if config.TenantID == "" || config.AgentBotUsername == "" || config.ApprovalTTL <= 0 ||
		config.ReconcileEvery <= 0 || config.BatchSize < 1 || config.BatchSize > 100 ||
		preparations == nil || executions == nil || audits == nil || principals == nil || logic == nil {
		return nil, fmt.Errorf("agent mutation service configuration is incomplete")
	}
	return &Service{
		config: config, preparations: preparations, executions: executions, audits: audits,
		principals: principals, logic: logic, now: time.Now,
	}, nil
}

func (s *Service) SetClockForTest(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// PrepareTenantMembershipStatus 冻结参数并创建审批；它绝不执行 IAM 写入。
func (s *Service) PrepareTenantMembershipStatus(
	ctx context.Context,
	request toolbroker.MembershipMutationPrepareRequest,
) (*toolbroker.MembershipMutationPrepareResult, error) {
	if request.TenantID != s.config.TenantID || request.RequesterID == "" || request.TargetUsername == request.RequesterID ||
		request.TargetUsername == s.config.AgentBotUsername {
		return nil, ErrRequesterUnauthorized
	}
	if err := s.requireCurrentWriteScope(ctx, request.TenantID, request.RequesterID); err != nil {
		return nil, err
	}
	args := agentmutation.NewMembershipStatusArgs(
		request.TenantID, request.RunID, request.CallID, request.RequesterID, request.TargetUsername,
		request.DesiredStatus, request.ExpectedVersion, request.DryRun,
	)
	payload, err := args.CanonicalPayload()
	if err != nil {
		return nil, err
	}
	argsHash, err := args.Hash()
	if err != nil {
		return nil, err
	}
	argsSummary := membershipArgsSummary(args)
	ref := agentmutation.FrozenArgsRef(args.TenantID, args.CallID, argsHash)
	if args.DryRun {
		preview, err := s.logic.PreviewTenantMembershipStatus(ctx, args)
		if err != nil {
			return nil, err
		}
		if preview == nil || preview.TargetUsername != args.TargetUsername || preview.DesiredStatus != args.DesiredStatus ||
			preview.CurrentVersion != args.ExpectedVersion || preview.ArgsHash != argsHash {
			return nil, ErrFrozenBindingInvalid
		}
		return &toolbroker.MembershipMutationPrepareResult{
			CallID: args.CallID, ArgsHash: argsHash, Status: "DRY_RUN", Created: false,
		}, nil
	}

	if existing, lookupErr := s.executions.GetAgentToolExecution(ctx, args.TenantID, args.CallID); lookupErr == nil {
		if existing.ArgsHash != argsHash || existing.FrozenArgsRef != ref || existing.RunID != args.RunID || existing.ToolName != args.ToolName {
			return nil, ErrFrozenBindingInvalid
		}
		frozen, err := s.preparations.GetAgentFrozenToolArgs(ctx, args.TenantID, existing.FrozenArgsRef)
		if err != nil || frozen.ArgsHash != argsHash || string(frozen.Payload) != string(payload) || frozen.RequesterID != args.RequesterID {
			return nil, ErrFrozenBindingInvalid
		}
		if existing.Status == model.AgentToolExecutionStatusSucceeded ||
			existing.Status == model.AgentToolExecutionStatusFailedFinal ||
			existing.Status == model.AgentToolExecutionStatusCancelled {
			return executionResult(existing), nil
		}
		approval, created, err := s.ensureApproval(ctx, existing, frozen)
		if err != nil {
			return nil, err
		}
		return preparedResult(existing, approval, created), nil
	} else if !errors.Is(lookupErr, repo.ErrAgentToolExecutionNotFound) {
		return nil, lookupErr
	}

	expiresAt := s.now().UTC().Add(s.config.ApprovalTTL)
	prepared, err := s.preparations.PrepareAgentMutation(ctx, &model.AgentFrozenToolArgs{
		TenantID: args.TenantID, Ref: ref, RunID: args.RunID, CallID: args.CallID, RequesterID: args.RequesterID,
		ArgsHash: argsHash, Payload: payload, ApprovalExpiresAt: expiresAt,
	}, &model.AgentToolExecution{
		TenantID: args.TenantID, RunID: args.RunID, CallID: args.CallID, RuntimeToolCallID: args.CallID,
		ToolName: args.ToolName, ToolVersion: agentmutation.MembershipStatusToolVersion,
		SchemaVersion: agentmutation.MembershipStatusSchemaVersion, ArgsHash: argsHash, FrozenArgsRef: ref,
		ArgsSummary: argsSummary, IdempotencyKey: agentmutation.IdempotencyKey(args.TenantID, args.CallID),
	})
	if err != nil {
		return nil, err
	}
	if prepared == nil || prepared.Execution == nil || prepared.Frozen == nil {
		return nil, fmt.Errorf("prepare repository returned no mutation fact")
	}
	if err := s.appendAudit(ctx, prepared.Execution, "prepared", "user", args.RequesterID,
		"Frozen tenant membership status arguments", prepared.Execution.CreatedAt, ref); err != nil {
		return nil, err
	}
	approval, created, err := s.ensureApproval(ctx, prepared.Execution, prepared.Frozen)
	if err != nil {
		return nil, err
	}
	return preparedResult(prepared.Execution, approval, created), nil
}

func (s *Service) ensureApproval(ctx context.Context, execution *model.AgentToolExecution, frozen *model.AgentFrozenToolArgs) (*ApprovalFact, bool, error) {
	approval, created, err := s.logic.CreateApproval(ctx, CreateApprovalRequest{
		TenantID: execution.TenantID, RunID: execution.RunID, CallID: execution.CallID,
		ToolName: execution.ToolName, RequesterID: frozen.RequesterID, ArgsHash: execution.ArgsHash,
		ArgsSummary: execution.ArgsSummary, ExpiresAt: frozen.ApprovalExpiresAt,
	})
	if err != nil {
		return nil, false, err
	}
	if approval == nil || approval.TenantID != execution.TenantID || approval.RunID != execution.RunID ||
		approval.CallID != execution.CallID || approval.RequesterID != frozen.RequesterID || approval.ArgsHash != execution.ArgsHash {
		return nil, false, ErrFrozenBindingInvalid
	}
	occurredAt := approval.CreatedAt
	if occurredAt.IsZero() {
		occurredAt = execution.CreatedAt
	}
	if err := s.appendAudit(ctx, execution, "approval-requested", "user", frozen.RequesterID,
		"Created durable IAM mutation approval", occurredAt, execution.FrozenArgsRef); err != nil {
		return nil, false, err
	}
	return approval, created, nil
}

func preparedResult(execution *model.AgentToolExecution, approval *ApprovalFact, created bool) *toolbroker.MembershipMutationPrepareResult {
	return &toolbroker.MembershipMutationPrepareResult{
		CallID: approval.CallID, ArgsHash: approval.ArgsHash, Status: approval.Status,
		ExecutionStatus: execution.Status, ExpiresAt: approval.ExpiresAt, Created: created,
	}
}

func executionResult(execution *model.AgentToolExecution) *toolbroker.MembershipMutationPrepareResult {
	return &toolbroker.MembershipMutationPrepareResult{
		CallID: execution.CallID, ArgsHash: execution.ArgsHash,
		ExecutionStatus: execution.Status, OperationID: execution.DownstreamOperationID,
		ExecutionSummary: execution.ResultSummary,
	}
}

// ProcessCall 把 MQ 事件当作唤醒信号，并重新读取审批和冻结参数后执行。
func (s *Service) ProcessCall(ctx context.Context, tenantID, callID string) error {
	lock := s.callLock(tenantID, callID)
	lock.Lock()
	defer lock.Unlock()

	execution, err := s.executions.GetAgentToolExecution(ctx, tenantID, callID)
	if err != nil {
		return err
	}
	if execution.Status == model.AgentToolExecutionStatusSucceeded || execution.Status == model.AgentToolExecutionStatusCancelled {
		return nil
	}
	if execution.Status == model.AgentToolExecutionStatusFailedFinal {
		occurredAt := execution.UpdatedAt
		if execution.FinishedAt != nil {
			occurredAt = *execution.FinishedAt
		}
		return s.appendAudit(ctx, execution, "failed-final", "service", s.config.AgentBotUsername,
			"IAM mutation execution did not complete: "+execution.ErrorCode, occurredAt, execution.FrozenArgsRef)
	}
	frozen, args, err := s.loadAndVerifyFrozen(ctx, execution)
	if err != nil {
		return s.failFinal(ctx, execution, "ARGS_SUBSTITUTION", err.Error())
	}
	if err := s.appendAudit(ctx, execution, "prepared", "user", frozen.RequesterID,
		"Frozen tenant membership status arguments", execution.CreatedAt, execution.FrozenArgsRef); err != nil {
		return err
	}
	if execution.Attempt > 0 && execution.StartedAt != nil {
		if err := s.appendAudit(ctx, execution, fmt.Sprintf("executing-%d", execution.Attempt), "service", s.config.AgentBotUsername,
			"Started idempotent IAM mutation attempt", *execution.StartedAt, execution.FrozenArgsRef); err != nil {
			return err
		}
	}
	if execution.Status == model.AgentToolExecutionStatusFailedRetryable && execution.LastFailedAt != nil {
		if err := s.appendAudit(ctx, execution, fmt.Sprintf("failed-retryable-%d", execution.Attempt), "service", s.config.AgentBotUsername,
			"IAM mutation execution did not complete: "+execution.ErrorCode, *execution.LastFailedAt, execution.FrozenArgsRef); err != nil {
			return err
		}
	}
	if execution.Status == model.AgentToolExecutionStatusReady || execution.Status == model.AgentToolExecutionStatusExecuting ||
		execution.Status == model.AgentToolExecutionStatusFailedRetryable {
		if execution.ApprovalVersion < 1 {
			return s.failFinal(ctx, execution, "APPROVAL_BINDING_MISSING", "prepared execution has no frozen approval version")
		}
		execution, err = s.advanceToExecuting(ctx, execution, nil)
		if err != nil {
			return err
		}
		// 这可能是“下游已提交但响应丢失”的恢复。Logic 先查不可变收据；
		// 只有没有收据的首次执行才重新校验当前审批与 requester/approver Scope。
		return s.executePreparedMutation(ctx, execution, args)
	}
	approval, err := s.logic.GetExecutionApproval(ctx, tenantID, callID, execution.ArgsHash)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			_, _, createErr := s.ensureApproval(ctx, execution, frozen)
			return createErr
		}
		return err
	}
	if err := s.appendAudit(ctx, execution, "approval-requested", "user", frozen.RequesterID,
		"Created durable IAM mutation approval", approval.CreatedAt, execution.FrozenArgsRef); err != nil {
		return err
	}
	if err := validateExecutionApproval(s.now().UTC(), execution, frozen, approval); err != nil {
		if approval.Status == model.AgentApprovalStatusPending && s.now().UTC().Before(approval.ExpiresAt) {
			return nil
		}
		return s.failFinal(ctx, execution, "APPROVAL_NOT_EXECUTABLE", err.Error())
	}
	if err := s.requireCurrentWriteScope(ctx, tenantID, frozen.RequesterID); err != nil {
		return s.failFinal(ctx, execution, "REQUESTER_DOWNGRADED", err.Error())
	}

	execution, err = s.advanceToExecuting(ctx, execution, approval)
	if err != nil {
		return err
	}
	return s.executePreparedMutation(ctx, execution, args)
}

func (s *Service) executePreparedMutation(ctx context.Context, execution *model.AgentToolExecution, args agentmutation.MembershipStatusArgs) error {
	receipt, err := s.logic.ExecuteTenantMembershipStatus(ctx, args, execution.IdempotencyKey, execution.ApprovalVersion)
	if err != nil {
		if permanentExecutionError(err) {
			return s.transitionFailure(ctx, execution, model.AgentToolExecutionStatusFailedFinal, "IAM_MUTATION_REJECTED", err.Error())
		}
		transitionErr := s.transitionFailure(ctx, execution, model.AgentToolExecutionStatusFailedRetryable, "IAM_MUTATION_UNAVAILABLE", err.Error())
		if transitionErr != nil {
			return transitionErr
		}
		return err
	}
	if receipt == nil || receipt.OperationID == "" || receipt.TargetUsername != args.TargetUsername ||
		receipt.ResultStatus != args.DesiredStatus || receipt.ApprovalVersion != execution.ApprovalVersion {
		return s.transitionFailure(ctx, execution, model.AgentToolExecutionStatusFailedFinal, "INVALID_IAM_RECEIPT", "Logic returned an invalid IAM mutation receipt")
	}
	resultPayload, _ := json.Marshal(receipt)
	resultSum := sha256.Sum256(resultPayload)
	resultHash := hex.EncodeToString(resultSum[:])
	completedAt := receipt.CommittedAt
	if completedAt.IsZero() {
		completedAt = s.now().UTC()
	}
	resultRef := "logic://agent-iam-mutations/" + receipt.OperationID
	if err := s.appendAudit(ctx, execution, "succeeded", "service", s.config.AgentBotUsername,
		"Committed approved tenant membership status change", completedAt, resultRef); err != nil {
		return err
	}
	_, err = s.executions.TransitionAgentToolExecution(ctx, repo.AgentToolExecutionTransition{
		TenantID: execution.TenantID, CallID: execution.CallID, ArgsHash: execution.ArgsHash,
		ExpectedStatus: model.AgentToolExecutionStatusExecuting, NextStatus: model.AgentToolExecutionStatusSucceeded,
		OccurredAt: completedAt, ResultRef: resultRef,
		ResultSummary: fmt.Sprintf("Tenant member %s is now %s at version %d", args.TargetUsername, args.DesiredStatus, receipt.ResultVersion),
		ResultHash:    resultHash, DownstreamOperationID: receipt.OperationID,
	})
	if err != nil {
		return err
	}
	return nil
}

func (s *Service) loadAndVerifyFrozen(ctx context.Context, execution *model.AgentToolExecution) (*model.AgentFrozenToolArgs, agentmutation.MembershipStatusArgs, error) {
	frozen, err := s.preparations.GetAgentFrozenToolArgs(ctx, execution.TenantID, execution.FrozenArgsRef)
	if err != nil {
		return nil, agentmutation.MembershipStatusArgs{}, err
	}
	args, err := agentmutation.ParseMembershipStatusArgs(frozen.Payload)
	if err != nil {
		return nil, agentmutation.MembershipStatusArgs{}, err
	}
	hash, err := args.Hash()
	if err != nil || frozen.Ref != execution.FrozenArgsRef || frozen.ArgsHash != execution.ArgsHash || hash != execution.ArgsHash ||
		frozen.TenantID != execution.TenantID || frozen.RunID != execution.RunID || frozen.CallID != execution.CallID ||
		frozen.RequesterID != args.RequesterID || args.ToolName != execution.ToolName {
		return nil, agentmutation.MembershipStatusArgs{}, ErrFrozenBindingInvalid
	}
	return frozen, args, nil
}

func validateExecutionApproval(now time.Time, execution *model.AgentToolExecution, frozen *model.AgentFrozenToolArgs, approval *ApprovalFact) error {
	if approval == nil || approval.TenantID != execution.TenantID || approval.RunID != execution.RunID ||
		approval.CallID != execution.CallID || approval.ToolName != execution.ToolName || approval.ArgsHash != execution.ArgsHash ||
		approval.RequesterID != frozen.RequesterID || approval.Status != model.AgentApprovalStatusApproved ||
		approval.Decision != model.AgentApprovalDecisionApprove || approval.Version < 2 || !now.Before(approval.ExpiresAt) {
		return fmt.Errorf("approval binding, decision, version or expiry is invalid")
	}
	return nil
}

func (s *Service) advanceToExecuting(ctx context.Context, execution *model.AgentToolExecution, approval *ApprovalFact) (*model.AgentToolExecution, error) {
	current := execution
	if current.Status == model.AgentToolExecutionStatusPrepared {
		readyAt := approval.DecidedAt
		if readyAt.IsZero() {
			readyAt = s.now().UTC()
		}
		if err := s.appendAudit(ctx, current, "approved", "user", approval.DecisionBy,
			"Authoritative approval permits IAM mutation execution", readyAt, current.FrozenArgsRef); err != nil {
			return nil, err
		}
		result, err := s.executions.TransitionAgentToolExecution(ctx, repo.AgentToolExecutionTransition{
			TenantID: current.TenantID, CallID: current.CallID, ArgsHash: current.ArgsHash,
			ExpectedStatus: current.Status, NextStatus: model.AgentToolExecutionStatusReady, OccurredAt: readyAt,
			ApprovalVersion: approval.Version,
		})
		if err != nil {
			return nil, err
		}
		current = result.Execution
	}
	if current.Status == model.AgentToolExecutionStatusReady || current.Status == model.AgentToolExecutionStatusFailedRetryable {
		startedAt := s.now().UTC()
		result, err := s.executions.TransitionAgentToolExecution(ctx, repo.AgentToolExecutionTransition{
			TenantID: current.TenantID, CallID: current.CallID, ArgsHash: current.ArgsHash,
			ExpectedStatus: current.Status, NextStatus: model.AgentToolExecutionStatusExecuting, OccurredAt: startedAt,
		})
		if err != nil {
			return nil, err
		}
		current = result.Execution
		if err := s.appendAudit(ctx, current, fmt.Sprintf("executing-%d", current.Attempt), "service", s.config.AgentBotUsername,
			"Started idempotent IAM mutation attempt", startedAt, current.FrozenArgsRef); err != nil {
			return nil, err
		}
	}
	if current.Status != model.AgentToolExecutionStatusExecuting {
		return nil, fmt.Errorf("tool execution is not executable from status %s", current.Status)
	}
	return current, nil
}

func (s *Service) failFinal(ctx context.Context, execution *model.AgentToolExecution, code, summary string) error {
	if execution.Status == model.AgentToolExecutionStatusFailedFinal || execution.Status == model.AgentToolExecutionStatusCancelled {
		return nil
	}
	return s.transitionFailure(ctx, execution, model.AgentToolExecutionStatusFailedFinal, code, summary)
}

func (s *Service) transitionFailure(ctx context.Context, execution *model.AgentToolExecution, nextStatus, code, summary string) error {
	result, err := s.executions.TransitionAgentToolExecution(ctx, repo.AgentToolExecutionTransition{
		TenantID: execution.TenantID, CallID: execution.CallID, ArgsHash: execution.ArgsHash,
		ExpectedStatus: execution.Status, NextStatus: nextStatus, OccurredAt: s.now().UTC(),
		ErrorCode: code, ErrorSummary: truncate(summary, 512),
	})
	if err != nil {
		return err
	}
	var event string
	occurredAt := s.now().UTC()
	if result.Execution.LastFailedAt != nil {
		occurredAt = *result.Execution.LastFailedAt
	}
	if nextStatus == model.AgentToolExecutionStatusFailedFinal {
		event = "failed-final"
	} else {
		event = fmt.Sprintf("failed-retryable-%d", result.Execution.Attempt)
	}
	return s.appendAudit(ctx, result.Execution, event, "service", s.config.AgentBotUsername,
		"IAM mutation execution did not complete: "+code, occurredAt, result.Execution.FrozenArgsRef)
}

func (s *Service) appendAudit(ctx context.Context, execution *model.AgentToolExecution, suffix, actorType, actorID, summary string, occurredAt time.Time, detailRef string) error {
	if occurredAt.IsZero() {
		occurredAt = s.now().UTC()
	}
	_, err := s.audits.AppendAgentAudit(ctx, &model.AgentAuditLog{
		TenantID: execution.TenantID, AuditID: execution.CallID + ":" + suffix,
		RunID: execution.RunID, CallID: execution.CallID, EventType: "agent.iam.mutation." + suffix,
		ActorType: actorType, ActorID: actorID, Summary: summary, DetailRef: detailRef, OccurredAt: occurredAt,
	})
	return err
}

func (s *Service) requireCurrentWriteScope(ctx context.Context, tenantID, requesterID string) error {
	principal, err := s.principals.ResolvePrincipal(ctx, tenantID, requesterID)
	if err != nil || principal.TenantID != tenantID || principal.ActorID != requesterID || principal.Username != requesterID ||
		!contains(principal.Roles, model.SystemRoleIAMAdmin) || !contains(principal.Scopes, model.ScopeIAMUsersWrite) {
		return ErrRequesterUnauthorized
	}
	return nil
}

func (s *Service) callLock(tenantID, callID string) *sync.Mutex {
	value, _ := s.locks.LoadOrStore(tenantID+"\x00"+callID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (s *Service) Reconcile(ctx context.Context) error {
	executions, err := s.preparations.ListAgentToolExecutions(ctx, repo.AgentToolExecutionListFilter{
		TenantID: s.config.TenantID,
		Statuses: []string{
			model.AgentToolExecutionStatusPrepared, model.AgentToolExecutionStatusReady,
			model.AgentToolExecutionStatusExecuting, model.AgentToolExecutionStatusFailedRetryable,
		},
		Limit: s.config.BatchSize,
	})
	if err != nil {
		return err
	}
	var joined error
	for _, execution := range executions {
		if err := s.ProcessCall(ctx, execution.TenantID, execution.CallID); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func membershipArgsSummary(args agentmutation.MembershipStatusArgs) string {
	return fmt.Sprintf("Set tenant member %s status to %s at expected version %d", args.TargetUsername, args.DesiredStatus, args.ExpectedVersion)
}

func permanentExecutionError(err error) bool {
	switch status.Code(err) {
	case codes.InvalidArgument, codes.PermissionDenied, codes.FailedPrecondition, codes.NotFound, codes.AlreadyExists:
		return true
	default:
		return false
	}
}

func contains(values []string, expected string) bool {
	return slices.Contains(values, expected)
}

func truncate(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max]
}
