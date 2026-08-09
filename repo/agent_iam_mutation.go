package repo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ceyewan/genesis/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/pkg/agentmutation"
)

// AgentMutationPreparationRepo 原子保存不可变参数和 PREPARED 执行事实。
// 这样即使 Pilot 在随后创建审批前崩溃，重投也不会丢失或替换参数。
type AgentMutationPreparationRepo interface {
	PrepareAgentMutation(context.Context, *model.AgentFrozenToolArgs, *model.AgentToolExecution) (*AgentMutationPreparationResult, error)
	GetAgentFrozenToolArgs(ctx context.Context, tenantID, ref string) (*model.AgentFrozenToolArgs, error)
	ListAgentToolExecutions(ctx context.Context, filter AgentToolExecutionListFilter) ([]*model.AgentToolExecution, error)
	Close() error
}

type AgentMutationPreparationResult struct {
	Frozen    *model.AgentFrozenToolArgs
	Execution *model.AgentToolExecution
	Created   bool
}

type AgentToolExecutionListFilter struct {
	TenantID string
	Statuses []string
	Limit    int
}

type agentMutationPreparationRepo struct{ db db.DB }

func NewAgentMutationPreparationRepo(database db.DB) (AgentMutationPreparationRepo, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}
	return &agentMutationPreparationRepo{db: database}, nil
}

func (r *agentMutationPreparationRepo) PrepareAgentMutation(
	ctx context.Context,
	frozen *model.AgentFrozenToolArgs,
	execution *model.AgentToolExecution,
) (*AgentMutationPreparationResult, error) {
	if err := validateFrozenToolArgs(frozen); err != nil {
		return nil, err
	}
	if err := validateNewAgentToolExecution(execution); err != nil {
		return nil, err
	}
	if frozen.TenantID != execution.TenantID || frozen.RunID != execution.RunID || frozen.CallID != execution.CallID ||
		frozen.ArgsHash != execution.ArgsHash || frozen.Ref != execution.FrozenArgsRef {
		return nil, fmt.Errorf("%w: frozen args and execution binding differ", ErrAgentStateConflict)
	}

	frozenCandidate := *frozen
	frozenCandidate.ID = 0
	executionCandidate := *execution
	executionCandidate.ID = 0
	executionCandidate.Status = model.AgentToolExecutionStatusPrepared
	executionCandidate.Version = 1
	executionCandidate.ApprovalVersion = 0
	executionCandidate.Attempt = 0
	executionCandidate.ReadyAt = nil
	executionCandidate.StartedAt = nil
	executionCandidate.LastFailedAt = nil
	executionCandidate.FinishedAt = nil
	executionCandidate.ResultRef = ""
	executionCandidate.ResultSummary = ""
	executionCandidate.ResultHash = ""
	executionCandidate.DownstreamOperationID = ""
	executionCandidate.ErrorCode = ""
	executionCandidate.ErrorSummary = ""

	result := &AgentMutationPreparationResult{}
	err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		frozenCreated := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&frozenCandidate)
		if frozenCreated.Error != nil {
			return fmt.Errorf("create frozen tool args: %w", frozenCreated.Error)
		}
		if frozenCreated.RowsAffected == 0 {
			var existing model.AgentFrozenToolArgs
			if err := tx.Where("tenant_id = ? AND call_id = ?", frozenCandidate.TenantID, frozenCandidate.CallID).Take(&existing).Error; err != nil {
				return fmt.Errorf("read conflicting frozen tool args: %w", err)
			}
			if !sameFrozenToolArgs(&existing, &frozenCandidate) {
				return fmt.Errorf("%w: frozen args call_id=%s", ErrAgentStateConflict, frozenCandidate.CallID)
			}
			frozenCandidate = existing
		}

		executionCreated := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&executionCandidate)
		if executionCreated.Error != nil {
			return fmt.Errorf("create prepared tool execution: %w", executionCreated.Error)
		}
		if executionCreated.RowsAffected == 0 {
			var existing model.AgentToolExecution
			err := tx.Where("tenant_id = ? AND call_id = ?", executionCandidate.TenantID, executionCandidate.CallID).Take(&existing).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				err = tx.Where("tenant_id = ? AND idempotency_key = ?", executionCandidate.TenantID, executionCandidate.IdempotencyKey).Take(&existing).Error
			}
			if err != nil {
				return fmt.Errorf("read conflicting prepared tool execution: %w", err)
			}
			if !sameAgentToolExecutionCreate(&existing, &executionCandidate) {
				return fmt.Errorf("%w: tool call_id=%s", ErrAgentStateConflict, executionCandidate.CallID)
			}
			executionCandidate = existing
		}

		result.Frozen = &frozenCandidate
		result.Execution = &executionCandidate
		result.Created = frozenCreated.RowsAffected == 1 && executionCreated.RowsAffected == 1
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *agentMutationPreparationRepo) GetAgentFrozenToolArgs(ctx context.Context, tenantID, ref string) (*model.AgentFrozenToolArgs, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(ref) == "" {
		return nil, fmt.Errorf("tenant_id and ref are required")
	}
	var frozen model.AgentFrozenToolArgs
	if err := r.db.DB(ctx).Where("tenant_id = ? AND ref = ?", tenantID, ref).Take(&frozen).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAgentFrozenArgsNotFound
		}
		return nil, fmt.Errorf("get frozen tool args: %w", err)
	}
	return &frozen, nil
}

func (r *agentMutationPreparationRepo) ListAgentToolExecutions(ctx context.Context, filter AgentToolExecutionListFilter) ([]*model.AgentToolExecution, error) {
	if strings.TrimSpace(filter.TenantID) == "" || filter.Limit < 1 || filter.Limit > 100 || len(filter.Statuses) == 0 {
		return nil, fmt.Errorf("tenant_id, statuses and limit are required")
	}
	for _, status := range filter.Statuses {
		if !validAgentToolExecutionStatus(status) {
			return nil, fmt.Errorf("invalid tool execution status %s", status)
		}
	}
	var executions []*model.AgentToolExecution
	if err := r.db.DB(ctx).Where("tenant_id = ? AND status IN ?", filter.TenantID, filter.Statuses).
		Order("updated_at ASC, id ASC").Limit(filter.Limit).Find(&executions).Error; err != nil {
		return nil, fmt.Errorf("list tool executions: %w", err)
	}
	return executions, nil
}

func (r *agentMutationPreparationRepo) Close() error { return nil }

func validateFrozenToolArgs(frozen *model.AgentFrozenToolArgs) error {
	if frozen == nil || strings.TrimSpace(frozen.TenantID) == "" || strings.TrimSpace(frozen.Ref) == "" ||
		strings.TrimSpace(frozen.RunID) == "" || strings.TrimSpace(frozen.CallID) == "" || strings.TrimSpace(frozen.RequesterID) == "" ||
		len(frozen.Payload) == 0 || frozen.ApprovalExpiresAt.IsZero() {
		return fmt.Errorf("frozen tool args are incomplete")
	}
	if !validSHA256(frozen.ArgsHash) {
		return fmt.Errorf("frozen args_hash must be a lowercase SHA-256 hex digest")
	}
	args, err := agentmutation.ParseMembershipStatusArgs(frozen.Payload)
	if err != nil {
		return err
	}
	hash, err := args.Hash()
	if err != nil {
		return err
	}
	if args.TenantID != frozen.TenantID || args.RunID != frozen.RunID || args.CallID != frozen.CallID || args.RequesterID != frozen.RequesterID ||
		hash != frozen.ArgsHash || agentmutation.FrozenArgsRef(frozen.TenantID, frozen.CallID, hash) != frozen.Ref {
		return fmt.Errorf("%w: frozen args binding is invalid", ErrAgentStateConflict)
	}
	return nil
}

func sameFrozenToolArgs(a, b *model.AgentFrozenToolArgs) bool {
	return a.TenantID == b.TenantID && a.Ref == b.Ref && a.RunID == b.RunID && a.CallID == b.CallID &&
		a.RequesterID == b.RequesterID && a.ArgsHash == b.ArgsHash && a.ApprovalExpiresAt.Equal(b.ApprovalExpiresAt) && bytes.Equal(a.Payload, b.Payload)
}

func validAgentToolExecutionStatus(status string) bool {
	switch status {
	case model.AgentToolExecutionStatusPrepared, model.AgentToolExecutionStatusReady,
		model.AgentToolExecutionStatusExecuting, model.AgentToolExecutionStatusSucceeded,
		model.AgentToolExecutionStatusFailedRetryable, model.AgentToolExecutionStatusFailedFinal,
		model.AgentToolExecutionStatusCancelled:
		return true
	default:
		return false
	}
}

// AgentIAMMutationRepo 只在 Logic 内部使用。Pilot 只能调用 Logic RPC，不能持有该接口。
type AgentIAMMutationRepo interface {
	PreviewTenantMembershipStatus(ctx context.Context, request AgentIAMMembershipPreview) (*AgentIAMMembershipPreviewResult, error)
	GetTenantMembershipStatusResult(ctx context.Context, request AgentIAMMembershipMutation) (*AgentIAMMutationResult, error)
	ExecuteTenantMembershipStatus(ctx context.Context, request AgentIAMMembershipMutation) (*AgentIAMMutationResult, error)
	Close() error
}

type AgentIAMMembershipPreview struct {
	TenantID        string
	RunID           string
	CallID          string
	RequesterID     string
	TargetUsername  string
	DesiredStatus   string
	ExpectedVersion int64
}

type AgentIAMMembershipPreviewResult struct {
	TargetUsername string
	CurrentStatus  string
	DesiredStatus  string
	CurrentVersion int64
	WouldChange    bool
	ArgsHash       string
}

type AgentIAMMembershipMutation struct {
	TenantID        string
	RunID           string
	CallID          string
	ArgsHash        string
	ToolName        string
	IdempotencyKey  string
	TargetUsername  string
	RequesterID     string
	DesiredStatus   string
	ExpectedVersion int64
	ApprovalVersion int64
	OccurredAt      time.Time
}

type AgentIAMMutationResult struct {
	Receipt  *model.AgentIAMMutationReceipt
	Repeated bool
}

type agentIAMMutationRepo struct {
	db               db.DB
	agentBotUsername string
}

func NewAgentIAMMutationRepo(database db.DB, agentBotUsername string) (AgentIAMMutationRepo, error) {
	if database == nil || strings.TrimSpace(agentBotUsername) == "" {
		return nil, fmt.Errorf("database and agent bot username are required")
	}
	return &agentIAMMutationRepo{db: database, agentBotUsername: agentBotUsername}, nil
}

func (r *agentIAMMutationRepo) PreviewTenantMembershipStatus(ctx context.Context, request AgentIAMMembershipPreview) (*AgentIAMMembershipPreviewResult, error) {
	args := agentmutation.NewMembershipStatusArgs(
		request.TenantID, request.RunID, request.CallID, request.RequesterID, request.TargetUsername,
		request.DesiredStatus, request.ExpectedVersion, true,
	)
	hash, err := args.Hash()
	if err != nil {
		return nil, err
	}
	var result *AgentIAMMembershipPreviewResult
	err = r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		lockKey := fmt.Sprintf("agent-iam-membership:%d:%s", len(request.TenantID), request.TenantID)
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", lockKey).Error; err != nil {
			return fmt.Errorf("lock tenant IAM preview: %w", err)
		}
		if err := r.validateCurrentIAMActor(tx, request.TenantID, request.RequesterID); err != nil {
			return err
		}
		if request.TargetUsername == request.RequesterID {
			return fmt.Errorf("%w: requester targets self", ErrAgentIAMMutationNotAllowed)
		}
		var targetUser model.User
		if err := tx.Where("username = ?", request.TargetUsername).Take(&targetUser).Error; err != nil {
			return fmt.Errorf("%w: target user unavailable", ErrAgentIAMMutationNotAllowed)
		}
		if request.TargetUsername == r.agentBotUsername || targetUser.Kind == model.UserKindAgentBot {
			return fmt.Errorf("%w: agent bot identity is protected", ErrAgentIAMMutationNotAllowed)
		}
		var target model.TenantMembership
		if err := tx.Where("tenant_id = ? AND username = ?", request.TenantID, request.TargetUsername).Take(&target).Error; err != nil {
			return fmt.Errorf("%w: target membership unavailable", ErrAgentIAMMutationNotAllowed)
		}
		if target.Version != request.ExpectedVersion {
			return fmt.Errorf("%w: expected target version %d, current %d", ErrAgentIAMMutationConflict, request.ExpectedVersion, target.Version)
		}
		if err := validateLastActiveAdmin(tx, request.TenantID, request.TargetUsername, request.DesiredStatus, target.Status); err != nil {
			return err
		}
		result = &AgentIAMMembershipPreviewResult{
			TargetUsername: request.TargetUsername, CurrentStatus: target.Status, DesiredStatus: request.DesiredStatus,
			CurrentVersion: target.Version, WouldChange: target.Status != request.DesiredStatus, ArgsHash: hash,
		}
		return nil
	})
	return result, err
}

func (r *agentIAMMutationRepo) GetTenantMembershipStatusResult(ctx context.Context, request AgentIAMMembershipMutation) (*AgentIAMMutationResult, error) {
	if err := validateIAMMembershipMutation(request); err != nil {
		return nil, err
	}
	var receipt model.AgentIAMMutationReceipt
	if err := r.db.DB(ctx).Where("tenant_id = ? AND idempotency_key = ?", request.TenantID, request.IdempotencyKey).Take(&receipt).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAgentIAMMutationReceiptNotFound
		}
		return nil, fmt.Errorf("get IAM mutation receipt: %w", err)
	}
	if !sameIAMMutationReceipt(&receipt, request) {
		return nil, fmt.Errorf("%w: receipt binding differs", ErrAgentIAMMutationConflict)
	}
	return &AgentIAMMutationResult{Receipt: &receipt, Repeated: true}, nil
}

func (r *agentIAMMutationRepo) ExecuteTenantMembershipStatus(ctx context.Context, request AgentIAMMembershipMutation) (*AgentIAMMutationResult, error) {
	if err := validateIAMMembershipMutation(request); err != nil {
		return nil, err
	}
	result := &AgentIAMMutationResult{}
	err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		lockKey := fmt.Sprintf("agent-iam-membership:%d:%s", len(request.TenantID), request.TenantID)
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", lockKey).Error; err != nil {
			return fmt.Errorf("lock tenant IAM mutation: %w", err)
		}

		var existing model.AgentIAMMutationReceipt
		err := tx.Where("tenant_id = ? AND idempotency_key = ?", request.TenantID, request.IdempotencyKey).Take(&existing).Error
		if err == nil {
			if !sameIAMMutationReceipt(&existing, request) {
				return fmt.Errorf("%w: idempotency key reused", ErrAgentIAMMutationConflict)
			}
			result.Receipt = &existing
			result.Repeated = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("read IAM mutation receipt: %w", err)
		}

		var approval model.AgentApproval
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND call_id = ?", request.TenantID, request.CallID).Take(&approval).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAgentApprovalNotFound
			}
			return fmt.Errorf("lock authoritative approval: %w", err)
		}
		if approval.RunID != request.RunID || approval.ToolName != request.ToolName || approval.RequesterID != request.RequesterID || approval.ArgsHash != request.ArgsHash ||
			approval.Status != model.AgentApprovalStatusApproved || approval.Decision != model.AgentApprovalDecisionApprove ||
			approval.Version != request.ApprovalVersion || approval.RevokedAt != nil || !request.OccurredAt.Before(approval.ExpiresAt) {
			return fmt.Errorf("%w: approval is not executable", ErrAgentIAMMutationNotAllowed)
		}

		var requester model.TenantMembership
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND username = ?", request.TenantID, approval.RequesterID).Take(&requester).Error; err != nil {
			return fmt.Errorf("%w: requester membership unavailable", ErrAgentIAMMutationNotAllowed)
		}
		if requester.Status != model.TenantMembershipStatusActive || request.TargetUsername == approval.RequesterID {
			return fmt.Errorf("%w: requester is inactive or targets self", ErrAgentIAMMutationNotAllowed)
		}
		var requesterAdminCount int64
		if err := tx.Model(&model.SystemRoleBinding{}).Where(
			"tenant_id = ? AND username = ? AND role = ?", request.TenantID, approval.RequesterID, model.SystemRoleIAMAdmin,
		).Count(&requesterAdminCount).Error; err != nil {
			return fmt.Errorf("check requester admin role: %w", err)
		}
		if requesterAdminCount != 1 {
			return fmt.Errorf("%w: requester lost IAM write scope", ErrAgentIAMMutationNotAllowed)
		}
		if approval.DecisionBy == "" {
			return fmt.Errorf("%w: approval decision actor is missing", ErrAgentIAMMutationNotAllowed)
		}
		if err := r.validateCurrentIAMActor(tx, request.TenantID, approval.DecisionBy); err != nil {
			return fmt.Errorf("%w: approver lost approval decide scope", ErrAgentIAMMutationNotAllowed)
		}

		var targetUser model.User
		if err := tx.Where("username = ?", request.TargetUsername).Take(&targetUser).Error; err != nil {
			return fmt.Errorf("%w: target user unavailable", ErrAgentIAMMutationNotAllowed)
		}
		if request.TargetUsername == r.agentBotUsername || targetUser.Kind == model.UserKindAgentBot {
			return fmt.Errorf("%w: agent bot identity is protected", ErrAgentIAMMutationNotAllowed)
		}

		var target model.TenantMembership
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND username = ?", request.TenantID, request.TargetUsername).Take(&target).Error; err != nil {
			return fmt.Errorf("%w: target membership unavailable", ErrAgentIAMMutationNotAllowed)
		}
		if target.Version != request.ExpectedVersion {
			return fmt.Errorf("%w: expected target version %d, current %d", ErrAgentIAMMutationConflict, request.ExpectedVersion, target.Version)
		}

		if request.DesiredStatus == model.TenantMembershipStatusDisabled && target.Status != model.TenantMembershipStatusDisabled {
			var targetAdminCount int64
			if err := tx.Model(&model.SystemRoleBinding{}).Where(
				"tenant_id = ? AND username = ? AND role = ?", request.TenantID, request.TargetUsername, model.SystemRoleIAMAdmin,
			).Count(&targetAdminCount).Error; err != nil {
				return fmt.Errorf("check target admin role: %w", err)
			}
			if targetAdminCount > 0 {
				var activeAdminCount int64
				if err := tx.Table("t_system_role_binding AS roles").
					Joins("JOIN t_tenant_membership AS members ON members.tenant_id = roles.tenant_id AND members.username = roles.username").
					Where("roles.tenant_id = ? AND roles.role = ? AND members.status = ?", request.TenantID, model.SystemRoleIAMAdmin, model.TenantMembershipStatusActive).
					Count(&activeAdminCount).Error; err != nil {
					return fmt.Errorf("count active tenant administrators: %w", err)
				}
				if activeAdminCount <= 1 {
					return fmt.Errorf("%w: cannot disable the last active administrator", ErrAgentIAMMutationNotAllowed)
				}
			}
		}

		previousStatus, previousVersion := target.Status, target.Version
		if target.Status != request.DesiredStatus {
			updated := tx.Model(&model.TenantMembership{}).
				Where("tenant_id = ? AND username = ? AND version = ?", request.TenantID, request.TargetUsername, target.Version).
				Updates(map[string]any{"status": request.DesiredStatus, "version": gorm.Expr("version + 1")})
			if updated.Error != nil {
				return fmt.Errorf("update tenant membership: %w", updated.Error)
			}
			if updated.RowsAffected != 1 {
				return fmt.Errorf("%w: target version changed", ErrAgentIAMMutationConflict)
			}
			target.Status = request.DesiredStatus
			target.Version++
		}

		receipt := model.AgentIAMMutationReceipt{
			TenantID: request.TenantID, OperationID: request.IdempotencyKey, IdempotencyKey: request.IdempotencyKey,
			RunID: request.RunID, CallID: request.CallID, ArgsHash: request.ArgsHash, ToolName: request.ToolName,
			RequesterID: approval.RequesterID, TargetUsername: request.TargetUsername,
			PreviousStatus: previousStatus, ResultStatus: target.Status, PreviousVersion: previousVersion,
			ResultVersion: target.Version, ApprovalVersion: approval.Version, DownstreamCommittedAt: request.OccurredAt,
		}
		if err := tx.Create(&receipt).Error; err != nil {
			return fmt.Errorf("create IAM mutation receipt: %w", err)
		}
		result.Receipt = &receipt
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *agentIAMMutationRepo) Close() error { return nil }

func validateIAMMembershipMutation(request AgentIAMMembershipMutation) error {
	args := agentmutation.NewMembershipStatusArgs(
		request.TenantID, request.RunID, request.CallID, request.RequesterID, request.TargetUsername,
		request.DesiredStatus, request.ExpectedVersion, false,
	)
	hash, err := args.Hash()
	if err != nil {
		return err
	}
	if request.ToolName != agentmutation.MembershipStatusTool || request.ArgsHash != hash ||
		request.IdempotencyKey != agentmutation.IdempotencyKey(request.TenantID, request.CallID) ||
		request.ApprovalVersion < 1 || request.OccurredAt.IsZero() {
		return fmt.Errorf("%w: invalid IAM mutation binding", ErrAgentIAMMutationConflict)
	}
	return nil
}

func sameIAMMutationReceipt(receipt *model.AgentIAMMutationReceipt, request AgentIAMMembershipMutation) bool {
	return receipt.TenantID == request.TenantID && receipt.IdempotencyKey == request.IdempotencyKey &&
		receipt.RunID == request.RunID && receipt.CallID == request.CallID && receipt.ArgsHash == request.ArgsHash &&
		receipt.ToolName == request.ToolName && receipt.RequesterID == request.RequesterID && receipt.TargetUsername == request.TargetUsername &&
		receipt.ResultStatus == request.DesiredStatus && receipt.PreviousVersion == request.ExpectedVersion &&
		receipt.ApprovalVersion == request.ApprovalVersion
}

func (r *agentIAMMutationRepo) validateCurrentIAMActor(tx *gorm.DB, tenantID, actorID string) error {
	var membership model.TenantMembership
	if err := tx.Where("tenant_id = ? AND username = ?", tenantID, actorID).Take(&membership).Error; err != nil ||
		membership.Status != model.TenantMembershipStatusActive || membership.Version < 1 {
		return fmt.Errorf("%w: IAM actor membership is inactive", ErrAgentIAMMutationNotAllowed)
	}
	var adminCount int64
	if err := tx.Model(&model.SystemRoleBinding{}).Where(
		"tenant_id = ? AND username = ? AND role = ?", tenantID, actorID, model.SystemRoleIAMAdmin,
	).Count(&adminCount).Error; err != nil {
		return fmt.Errorf("check IAM actor role: %w", err)
	}
	if adminCount != 1 {
		return fmt.Errorf("%w: IAM actor lacks required system scope", ErrAgentIAMMutationNotAllowed)
	}
	return nil
}

func validateLastActiveAdmin(tx *gorm.DB, tenantID, targetUsername, desiredStatus, currentStatus string) error {
	if desiredStatus != model.TenantMembershipStatusDisabled || currentStatus == model.TenantMembershipStatusDisabled {
		return nil
	}
	var targetAdminCount int64
	if err := tx.Model(&model.SystemRoleBinding{}).Where(
		"tenant_id = ? AND username = ? AND role = ?", tenantID, targetUsername, model.SystemRoleIAMAdmin,
	).Count(&targetAdminCount).Error; err != nil {
		return fmt.Errorf("check target admin role: %w", err)
	}
	if targetAdminCount == 0 {
		return nil
	}
	var activeAdminCount int64
	if err := tx.Table("t_system_role_binding AS roles").
		Joins("JOIN t_tenant_membership AS members ON members.tenant_id = roles.tenant_id AND members.username = roles.username").
		Where("roles.tenant_id = ? AND roles.role = ? AND members.status = ?", tenantID, model.SystemRoleIAMAdmin, model.TenantMembershipStatusActive).
		Count(&activeAdminCount).Error; err != nil {
		return fmt.Errorf("count active tenant administrators: %w", err)
	}
	if activeAdminCount <= 1 {
		return fmt.Errorf("%w: cannot disable the last active administrator", ErrAgentIAMMutationNotAllowed)
	}
	return nil
}
