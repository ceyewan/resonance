package repo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ceyewan/genesis/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ceyewan/resonance/model"
)

const zeroAgentAuditHash = "0000000000000000000000000000000000000000000000000000000000000000"

// AgentApprovalRepo 管理 Logic 权威持有的审批事实。
type AgentApprovalRepo interface {
	CreateAgentApproval(ctx context.Context, approval *model.AgentApproval) (*AgentApprovalCreateResult, error)
	CreateAgentApprovalWithOutbox(ctx context.Context, approval *model.AgentApproval, outbox *model.MessageOutbox) (*AgentApprovalCreateResult, error)
	GetAgentApproval(ctx context.Context, tenantID, callID string) (*model.AgentApproval, error)
	ListAgentApprovals(ctx context.Context, filter AgentApprovalListFilter) ([]*model.AgentApproval, error)
	TransitionAgentApproval(ctx context.Context, transition AgentApprovalTransition) (*AgentApprovalTransitionResult, error)
	TransitionAgentApprovalWithOutbox(ctx context.Context, transition AgentApprovalTransition, outbox *model.MessageOutbox) (*AgentApprovalTransitionResult, error)
	Close() error
}

type AgentApprovalCreateResult struct {
	Approval *model.AgentApproval
	Outbox   *model.MessageOutbox
	Created  bool
}

type AgentApprovalListFilter struct {
	TenantID string
	Status   string
	BeforeID int64
	Limit    int
}

type AgentApprovalTransition struct {
	TenantID        string
	CallID          string
	ArgsHash        string
	ExpectedStatus  string
	ExpectedVersion int64
	NextStatus      string
	ActorID         string
	Reason          string
	OccurredAt      time.Time
}

type AgentApprovalTransitionResult struct {
	Approval *model.AgentApproval
	Outbox   *model.MessageOutbox
	Changed  bool
}

// AgentToolExecutionRepo 管理 Pilot/Tool Broker 的冻结参数与执行事实。
type AgentToolExecutionRepo interface {
	CreateAgentToolExecution(ctx context.Context, execution *model.AgentToolExecution) (*AgentToolExecutionCreateResult, error)
	GetAgentToolExecution(ctx context.Context, tenantID, callID string) (*model.AgentToolExecution, error)
	TransitionAgentToolExecution(ctx context.Context, transition AgentToolExecutionTransition) (*AgentToolExecutionTransitionResult, error)
	Close() error
}

type AgentToolExecutionCreateResult struct {
	Execution *model.AgentToolExecution
	Created   bool
}

type AgentToolExecutionTransition struct {
	TenantID              string
	CallID                string
	ArgsHash              string
	ExpectedStatus        string
	NextStatus            string
	ApprovalVersion       int64
	OccurredAt            time.Time
	ResultRef             string
	ResultSummary         string
	ResultHash            string
	DownstreamOperationID string
	ErrorCode             string
	ErrorSummary          string
}

type AgentToolExecutionTransitionResult struct {
	Execution *model.AgentToolExecution
	Changed   bool
}

// AgentAuditRepo 管理 Pilot 的追加式审计哈希链。
type AgentAuditRepo interface {
	AppendAgentAudit(ctx context.Context, entry *model.AgentAuditLog) (*AgentAuditAppendResult, error)
	GetAgentAudit(ctx context.Context, tenantID, auditID string) (*model.AgentAuditLog, error)
	VerifyAgentAuditChain(ctx context.Context, tenantID, runID string) error
	Close() error
}

type AgentAuditAppendResult struct {
	Entry   *model.AgentAuditLog
	Created bool
}

type agentApprovalRepo struct{ db db.DB }
type agentToolExecutionRepo struct{ db db.DB }
type agentAuditRepo struct{ db db.DB }

func NewAgentApprovalRepo(database db.DB) (AgentApprovalRepo, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}
	return &agentApprovalRepo{db: database}, nil
}

func NewAgentToolExecutionRepo(database db.DB) (AgentToolExecutionRepo, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}
	return &agentToolExecutionRepo{db: database}, nil
}

func NewAgentAuditRepo(database db.DB) (AgentAuditRepo, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}
	return &agentAuditRepo{db: database}, nil
}

func (r *agentApprovalRepo) CreateAgentApproval(ctx context.Context, approval *model.AgentApproval) (*AgentApprovalCreateResult, error) {
	return r.createAgentApproval(ctx, approval, nil)
}

func (r *agentApprovalRepo) CreateAgentApprovalWithOutbox(
	ctx context.Context,
	approval *model.AgentApproval,
	outbox *model.MessageOutbox,
) (*AgentApprovalCreateResult, error) {
	if err := validateAgentApprovalOutbox(outbox); err != nil {
		return nil, err
	}
	return r.createAgentApproval(ctx, approval, outbox)
}

func (r *agentApprovalRepo) createAgentApproval(
	ctx context.Context,
	approval *model.AgentApproval,
	outbox *model.MessageOutbox,
) (*AgentApprovalCreateResult, error) {
	if err := validateNewAgentApproval(approval); err != nil {
		return nil, err
	}

	candidate := *approval
	candidate.ID = 0
	candidate.Status = model.AgentApprovalStatusPending
	candidate.Decision = model.AgentApprovalDecisionNone
	candidate.DecisionBy = ""
	candidate.DecisionReason = ""
	candidate.DecidedAt = nil
	candidate.RevokedBy = ""
	candidate.RevokeReason = ""
	candidate.RevokedAt = nil
	candidate.ExpiredAt = nil
	candidate.Version = 1

	result := &AgentApprovalCreateResult{}
	err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		created := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "call_id"}},
			DoNothing: true,
		}).Create(&candidate)
		if created.Error != nil {
			return fmt.Errorf("create agent approval: %w", created.Error)
		}
		if created.RowsAffected == 1 {
			persistedOutbox, err := createAgentApprovalOutbox(tx, outbox)
			if err != nil {
				return err
			}
			result.Approval = &candidate
			result.Outbox = persistedOutbox
			result.Created = true
			return nil
		}

		var existing model.AgentApproval
		if err := tx.Where("tenant_id = ? AND call_id = ?", candidate.TenantID, candidate.CallID).Take(&existing).Error; err != nil {
			return fmt.Errorf("read conflicting agent approval: %w", err)
		}
		if !sameAgentApprovalCreate(&existing, &candidate) {
			return fmt.Errorf("%w: approval call_id=%s", ErrAgentStateConflict, candidate.CallID)
		}
		result.Approval = &existing
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *agentApprovalRepo) GetAgentApproval(ctx context.Context, tenantID, callID string) (*model.AgentApproval, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(callID) == "" {
		return nil, fmt.Errorf("tenant_id and call_id are required")
	}
	var approval model.AgentApproval
	if err := r.db.DB(ctx).Where("tenant_id = ? AND call_id = ?", tenantID, callID).Take(&approval).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAgentApprovalNotFound
		}
		return nil, fmt.Errorf("get agent approval: %w", err)
	}
	return &approval, nil
}

func (r *agentApprovalRepo) ListAgentApprovals(ctx context.Context, filter AgentApprovalListFilter) ([]*model.AgentApproval, error) {
	if strings.TrimSpace(filter.TenantID) == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	if filter.Status != "" && !validAgentApprovalStatus(filter.Status) {
		return nil, fmt.Errorf("invalid approval status %s", filter.Status)
	}
	if filter.BeforeID < 0 {
		return nil, fmt.Errorf("before_id cannot be negative")
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		return nil, fmt.Errorf("limit must be between 1 and 100")
	}

	query := r.db.DB(ctx).Where("tenant_id = ?", filter.TenantID)
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.BeforeID > 0 {
		query = query.Where("id < ?", filter.BeforeID)
	}
	var approvals []*model.AgentApproval
	if err := query.Order("id DESC").Limit(filter.Limit).Find(&approvals).Error; err != nil {
		return nil, fmt.Errorf("list agent approvals: %w", err)
	}
	return approvals, nil
}

func (r *agentApprovalRepo) TransitionAgentApproval(ctx context.Context, transition AgentApprovalTransition) (*AgentApprovalTransitionResult, error) {
	return r.transitionAgentApproval(ctx, transition, nil)
}

func (r *agentApprovalRepo) TransitionAgentApprovalWithOutbox(
	ctx context.Context,
	transition AgentApprovalTransition,
	outbox *model.MessageOutbox,
) (*AgentApprovalTransitionResult, error) {
	if err := validateAgentApprovalOutbox(outbox); err != nil {
		return nil, err
	}
	return r.transitionAgentApproval(ctx, transition, outbox)
}

func (r *agentApprovalRepo) transitionAgentApproval(
	ctx context.Context,
	transition AgentApprovalTransition,
	outbox *model.MessageOutbox,
) (*AgentApprovalTransitionResult, error) {
	if err := validateAgentApprovalTransitionInput(transition); err != nil {
		return nil, err
	}

	result := &AgentApprovalTransitionResult{}
	err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		var current model.AgentApproval
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND call_id = ?", transition.TenantID, transition.CallID).
			Take(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAgentApprovalNotFound
			}
			return fmt.Errorf("lock agent approval: %w", err)
		}
		if current.ArgsHash != transition.ArgsHash {
			return fmt.Errorf("%w: approval args_hash mismatch", ErrAgentStateConflict)
		}
		if current.Status == transition.NextStatus {
			if current.Version == transition.ExpectedVersion+1 && approvalTransitionAlreadyApplied(&current, transition) {
				result.Approval = &current
				return nil
			}
			return fmt.Errorf("%w: approval transition payload differs", ErrAgentStateConflict)
		}
		if current.Version != transition.ExpectedVersion {
			return fmt.Errorf("%w: approval expected version %d, current version %d", ErrAgentStateConflict, transition.ExpectedVersion, current.Version)
		}
		if current.Status != transition.ExpectedStatus || !allowedAgentApprovalTransition(current.Status, transition.NextStatus) {
			return fmt.Errorf("%w: approval %s -> %s", ErrAgentInvalidTransition, current.Status, transition.NextStatus)
		}

		updates, err := buildAgentApprovalTransition(&current, transition)
		if err != nil {
			return err
		}
		updates["version"] = gorm.Expr("version + 1")
		updated := tx.Model(&model.AgentApproval{}).
			Where("id = ? AND version = ?", current.ID, current.Version).
			Updates(updates)
		if updated.Error != nil {
			return fmt.Errorf("transition agent approval: %w", updated.Error)
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("%w: approval version changed", ErrAgentStateConflict)
		}
		if err := tx.Where("id = ?", current.ID).Take(&current).Error; err != nil {
			return fmt.Errorf("reload agent approval: %w", err)
		}
		persistedOutbox, err := createAgentApprovalOutbox(tx, outbox)
		if err != nil {
			return err
		}
		result.Approval = &current
		result.Outbox = persistedOutbox
		result.Changed = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func createAgentApprovalOutbox(tx *gorm.DB, outbox *model.MessageOutbox) (*model.MessageOutbox, error) {
	if outbox == nil {
		return nil, nil
	}
	candidate := *outbox
	candidate.ID = 0
	candidate.Status = model.OutboxStatusPending
	candidate.RetryCount = 0
	if err := tx.Create(&candidate).Error; err != nil {
		return nil, fmt.Errorf("create agent approval outbox: %w", err)
	}
	return &candidate, nil
}

func (r *agentToolExecutionRepo) CreateAgentToolExecution(ctx context.Context, execution *model.AgentToolExecution) (*AgentToolExecutionCreateResult, error) {
	if err := validateNewAgentToolExecution(execution); err != nil {
		return nil, err
	}
	candidate := *execution
	candidate.ID = 0
	candidate.Status = model.AgentToolExecutionStatusPrepared
	candidate.Version = 1
	candidate.ApprovalVersion = 0
	candidate.Attempt = 0
	candidate.ReadyAt = nil
	candidate.StartedAt = nil
	candidate.LastFailedAt = nil
	candidate.FinishedAt = nil
	candidate.ResultRef = ""
	candidate.ResultSummary = ""
	candidate.ResultHash = ""
	candidate.DownstreamOperationID = ""
	candidate.ErrorCode = ""
	candidate.ErrorSummary = ""

	result := &AgentToolExecutionCreateResult{}
	err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate)
		if created.Error != nil {
			return fmt.Errorf("create agent tool execution: %w", created.Error)
		}
		if created.RowsAffected == 1 {
			result.Execution = &candidate
			result.Created = true
			return nil
		}

		var existing model.AgentToolExecution
		err := tx.Where("tenant_id = ? AND call_id = ?", candidate.TenantID, candidate.CallID).Take(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = tx.Where("tenant_id = ? AND idempotency_key = ?", candidate.TenantID, candidate.IdempotencyKey).Take(&existing).Error
		}
		if err != nil {
			return fmt.Errorf("read conflicting agent tool execution: %w", err)
		}
		if !sameAgentToolExecutionCreate(&existing, &candidate) {
			return fmt.Errorf("%w: tool call_id=%s", ErrAgentStateConflict, candidate.CallID)
		}
		result.Execution = &existing
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *agentToolExecutionRepo) GetAgentToolExecution(ctx context.Context, tenantID, callID string) (*model.AgentToolExecution, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(callID) == "" {
		return nil, fmt.Errorf("tenant_id and call_id are required")
	}
	var execution model.AgentToolExecution
	if err := r.db.DB(ctx).Where("tenant_id = ? AND call_id = ?", tenantID, callID).Take(&execution).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAgentToolExecutionNotFound
		}
		return nil, fmt.Errorf("get agent tool execution: %w", err)
	}
	return &execution, nil
}

func (r *agentToolExecutionRepo) TransitionAgentToolExecution(ctx context.Context, transition AgentToolExecutionTransition) (*AgentToolExecutionTransitionResult, error) {
	if err := validateAgentToolExecutionTransitionInput(transition); err != nil {
		return nil, err
	}
	result := &AgentToolExecutionTransitionResult{}
	err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		var current model.AgentToolExecution
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND call_id = ?", transition.TenantID, transition.CallID).
			Take(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAgentToolExecutionNotFound
			}
			return fmt.Errorf("lock agent tool execution: %w", err)
		}
		if current.ArgsHash != transition.ArgsHash {
			return fmt.Errorf("%w: tool args_hash mismatch", ErrAgentStateConflict)
		}
		if current.Status == transition.NextStatus {
			if toolExecutionTransitionAlreadyApplied(&current, transition) {
				result.Execution = &current
				return nil
			}
			return fmt.Errorf("%w: tool transition payload differs", ErrAgentStateConflict)
		}
		if current.Status != transition.ExpectedStatus || !allowedAgentToolExecutionTransition(current.Status, transition.NextStatus) {
			return fmt.Errorf("%w: tool execution %s -> %s", ErrAgentInvalidTransition, current.Status, transition.NextStatus)
		}
		updates, err := buildAgentToolExecutionTransition(transition)
		if err != nil {
			return err
		}
		updates["version"] = gorm.Expr("version + 1")
		updated := tx.Model(&model.AgentToolExecution{}).
			Where("id = ? AND version = ?", current.ID, current.Version).
			Updates(updates)
		if updated.Error != nil {
			return fmt.Errorf("transition agent tool execution: %w", updated.Error)
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("%w: tool execution version changed", ErrAgentStateConflict)
		}
		if err := tx.Where("id = ?", current.ID).Take(&current).Error; err != nil {
			return fmt.Errorf("reload agent tool execution: %w", err)
		}
		result.Execution = &current
		result.Changed = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *agentAuditRepo) AppendAgentAudit(ctx context.Context, entry *model.AgentAuditLog) (*AgentAuditAppendResult, error) {
	if err := validateNewAgentAudit(entry); err != nil {
		return nil, err
	}
	candidate := *entry
	candidate.ID = 0
	candidate.Sequence = 0
	candidate.PrevHash = ""
	candidate.EntryHash = ""

	result := &AgentAuditAppendResult{}
	err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		lockKey := fmt.Sprintf("%d:%s%s", len(candidate.TenantID), candidate.TenantID, candidate.RunID)
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", lockKey).Error; err != nil {
			return fmt.Errorf("lock agent audit chain: %w", err)
		}

		var existing model.AgentAuditLog
		err := tx.Where("tenant_id = ? AND audit_id = ?", candidate.TenantID, candidate.AuditID).Take(&existing).Error
		if err == nil {
			if !sameAgentAuditInput(&existing, &candidate) {
				return fmt.Errorf("%w: audit_id=%s", ErrAgentStateConflict, candidate.AuditID)
			}
			result.Entry = &existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("check agent audit idempotency: %w", err)
		}

		var previous model.AgentAuditLog
		err = tx.Where("tenant_id = ? AND run_id = ?", candidate.TenantID, candidate.RunID).
			Order("sequence DESC").Take(&previous).Error
		switch {
		case err == nil:
			candidate.Sequence = previous.Sequence + 1
			candidate.PrevHash = previous.EntryHash
		case errors.Is(err, gorm.ErrRecordNotFound):
			candidate.Sequence = 1
			candidate.PrevHash = zeroAgentAuditHash
		default:
			return fmt.Errorf("read agent audit head: %w", err)
		}
		candidate.EntryHash, err = hashAgentAuditEntry(&candidate)
		if err != nil {
			return err
		}
		if err := tx.Create(&candidate).Error; err != nil {
			return fmt.Errorf("append agent audit: %w", err)
		}
		result.Entry = &candidate
		result.Created = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *agentAuditRepo) GetAgentAudit(ctx context.Context, tenantID, auditID string) (*model.AgentAuditLog, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(auditID) == "" {
		return nil, fmt.Errorf("tenant_id and audit_id are required")
	}
	var entry model.AgentAuditLog
	if err := r.db.DB(ctx).Where("tenant_id = ? AND audit_id = ?", tenantID, auditID).Take(&entry).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAgentAuditNotFound
		}
		return nil, fmt.Errorf("get agent audit: %w", err)
	}
	return &entry, nil
}

func (r *agentAuditRepo) VerifyAgentAuditChain(ctx context.Context, tenantID, runID string) error {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(runID) == "" {
		return fmt.Errorf("tenant_id and run_id are required")
	}
	var entries []*model.AgentAuditLog
	if err := r.db.DB(ctx).Where("tenant_id = ? AND run_id = ?", tenantID, runID).
		Order("sequence ASC").Find(&entries).Error; err != nil {
		return fmt.Errorf("list agent audit chain: %w", err)
	}
	previousHash := zeroAgentAuditHash
	for i, entry := range entries {
		expectedSequence := int64(i + 1)
		if entry.Sequence != expectedSequence || entry.PrevHash != previousHash {
			return fmt.Errorf("%w: sequence=%d", ErrAgentAuditChainBroken, entry.Sequence)
		}
		expectedHash, err := hashAgentAuditEntry(entry)
		if err != nil {
			return err
		}
		if entry.EntryHash != expectedHash {
			return fmt.Errorf("%w: entry=%s", ErrAgentAuditChainBroken, entry.AuditID)
		}
		previousHash = entry.EntryHash
	}
	return nil
}

func (r *agentApprovalRepo) Close() error      { return nil }
func (r *agentToolExecutionRepo) Close() error { return nil }
func (r *agentAuditRepo) Close() error         { return nil }

func validateNewAgentApproval(approval *model.AgentApproval) error {
	if approval == nil {
		return fmt.Errorf("approval cannot be nil")
	}
	if strings.TrimSpace(approval.TenantID) == "" || strings.TrimSpace(approval.RunID) == "" || strings.TrimSpace(approval.CallID) == "" {
		return fmt.Errorf("tenant_id, run_id and call_id are required")
	}
	if strings.TrimSpace(approval.ToolName) == "" || strings.TrimSpace(approval.RequesterID) == "" || strings.TrimSpace(approval.ArgsSummary) == "" {
		return fmt.Errorf("tool_name, requester_id and args_summary are required")
	}
	if !validSHA256(approval.ArgsHash) {
		return fmt.Errorf("args_hash must be a lowercase SHA-256 hex digest")
	}
	if approval.ExpiresAt.IsZero() {
		return fmt.Errorf("expires_at is required")
	}
	if approval.Status != "" && approval.Status != model.AgentApprovalStatusPending {
		return fmt.Errorf("new approval status must be PENDING")
	}
	return nil
}

func sameAgentApprovalCreate(a, b *model.AgentApproval) bool {
	return a.TenantID == b.TenantID && a.RunID == b.RunID && a.CallID == b.CallID &&
		a.ToolName == b.ToolName && a.RequesterID == b.RequesterID && a.ArgsHash == b.ArgsHash &&
		a.ArgsSummary == b.ArgsSummary && a.ExpiresAt.Equal(b.ExpiresAt)
}

func validateAgentApprovalTransitionInput(t AgentApprovalTransition) error {
	if strings.TrimSpace(t.TenantID) == "" || strings.TrimSpace(t.CallID) == "" {
		return fmt.Errorf("tenant_id and call_id are required")
	}
	if !validSHA256(t.ArgsHash) {
		return fmt.Errorf("args_hash must be a lowercase SHA-256 hex digest")
	}
	if t.ExpectedStatus == "" || t.NextStatus == "" || t.OccurredAt.IsZero() {
		return fmt.Errorf("expected_status, next_status and occurred_at are required")
	}
	if t.ExpectedVersion <= 0 {
		return fmt.Errorf("expected_version must be positive")
	}
	if !allowedAgentApprovalTransition(t.ExpectedStatus, t.NextStatus) {
		return fmt.Errorf("%w: approval %s -> %s", ErrAgentInvalidTransition, t.ExpectedStatus, t.NextStatus)
	}
	return nil
}

func validAgentApprovalStatus(value string) bool {
	switch value {
	case model.AgentApprovalStatusPending,
		model.AgentApprovalStatusApproved,
		model.AgentApprovalStatusRejected,
		model.AgentApprovalStatusRevoked,
		model.AgentApprovalStatusExpired:
		return true
	default:
		return false
	}
}

func validateAgentApprovalOutbox(outbox *model.MessageOutbox) error {
	if outbox == nil {
		return fmt.Errorf("approval outbox cannot be nil")
	}
	if outbox.EventID <= 0 || strings.TrimSpace(outbox.Topic) == "" || len(outbox.Payload) == 0 {
		return fmt.Errorf("approval outbox requires event_id, topic and payload")
	}
	if len(outbox.Topic) > 64 {
		return fmt.Errorf("approval outbox topic exceeds 64 bytes")
	}
	if outbox.NextRetryTime.IsZero() {
		return fmt.Errorf("approval outbox next_retry_time is required")
	}
	return nil
}

func allowedAgentApprovalTransition(from, to string) bool {
	switch from {
	case model.AgentApprovalStatusPending:
		return to == model.AgentApprovalStatusApproved || to == model.AgentApprovalStatusRejected ||
			to == model.AgentApprovalStatusRevoked || to == model.AgentApprovalStatusExpired
	case model.AgentApprovalStatusApproved:
		return to == model.AgentApprovalStatusRevoked || to == model.AgentApprovalStatusExpired
	default:
		return false
	}
}

func buildAgentApprovalTransition(current *model.AgentApproval, t AgentApprovalTransition) (map[string]any, error) {
	updates := map[string]any{"status": t.NextStatus}
	switch t.NextStatus {
	case model.AgentApprovalStatusApproved, model.AgentApprovalStatusRejected:
		if strings.TrimSpace(t.ActorID) == "" {
			return nil, fmt.Errorf("decision actor_id is required")
		}
		if !t.OccurredAt.Before(current.ExpiresAt) {
			return nil, ErrAgentApprovalExpired
		}
		decision := model.AgentApprovalDecisionApprove
		if t.NextStatus == model.AgentApprovalStatusRejected {
			decision = model.AgentApprovalDecisionReject
		}
		updates["decision"] = decision
		updates["decision_by"] = t.ActorID
		updates["decision_reason"] = t.Reason
		updates["decided_at"] = t.OccurredAt
	case model.AgentApprovalStatusRevoked:
		if strings.TrimSpace(t.ActorID) == "" {
			return nil, fmt.Errorf("revoke actor_id is required")
		}
		updates["revoked_by"] = t.ActorID
		updates["revoke_reason"] = t.Reason
		updates["revoked_at"] = t.OccurredAt
	case model.AgentApprovalStatusExpired:
		if t.OccurredAt.Before(current.ExpiresAt) {
			return nil, fmt.Errorf("%w: expiry transition before expires_at", ErrAgentInvalidTransition)
		}
		updates["expired_at"] = t.OccurredAt
	default:
		return nil, fmt.Errorf("%w: unknown approval status %s", ErrAgentInvalidTransition, t.NextStatus)
	}
	return updates, nil
}

func approvalTransitionAlreadyApplied(current *model.AgentApproval, t AgentApprovalTransition) bool {
	switch t.NextStatus {
	case model.AgentApprovalStatusApproved, model.AgentApprovalStatusRejected:
		decision := model.AgentApprovalDecisionApprove
		if t.NextStatus == model.AgentApprovalStatusRejected {
			decision = model.AgentApprovalDecisionReject
		}
		// 决定时间由 Logic 分配，而非用户提供。重投按原 expected_version 和
		// 决定载荷绑定，不要求本次重新采样的服务端时间与首次相同。
		return current.Decision == decision && current.DecisionBy == t.ActorID && current.DecisionReason == t.Reason
	case model.AgentApprovalStatusRevoked:
		return current.RevokedBy == t.ActorID && current.RevokeReason == t.Reason && sameTime(current.RevokedAt, t.OccurredAt)
	case model.AgentApprovalStatusExpired:
		return sameTime(current.ExpiredAt, t.OccurredAt)
	default:
		return false
	}
}

func validateNewAgentToolExecution(execution *model.AgentToolExecution) error {
	if execution == nil {
		return fmt.Errorf("execution cannot be nil")
	}
	if strings.TrimSpace(execution.TenantID) == "" || strings.TrimSpace(execution.RunID) == "" || strings.TrimSpace(execution.CallID) == "" {
		return fmt.Errorf("tenant_id, run_id and call_id are required")
	}
	if strings.TrimSpace(execution.RuntimeToolCallID) == "" || strings.TrimSpace(execution.ToolName) == "" ||
		strings.TrimSpace(execution.ToolVersion) == "" || strings.TrimSpace(execution.SchemaVersion) == "" {
		return fmt.Errorf("runtime tool call and tool/schema versions are required")
	}
	if !validSHA256(execution.ArgsHash) {
		return fmt.Errorf("args_hash must be a lowercase SHA-256 hex digest")
	}
	if strings.TrimSpace(execution.FrozenArgsRef) == "" || strings.TrimSpace(execution.ArgsSummary) == "" || strings.TrimSpace(execution.IdempotencyKey) == "" {
		return fmt.Errorf("frozen_args_ref, args_summary and idempotency_key are required")
	}
	if execution.Status != "" && execution.Status != model.AgentToolExecutionStatusPrepared {
		return fmt.Errorf("new tool execution status must be PREPARED")
	}
	return nil
}

func sameAgentToolExecutionCreate(a, b *model.AgentToolExecution) bool {
	return a.TenantID == b.TenantID && a.RunID == b.RunID && a.CallID == b.CallID &&
		a.RuntimeToolCallID == b.RuntimeToolCallID && a.ToolName == b.ToolName &&
		a.ToolVersion == b.ToolVersion && a.SchemaVersion == b.SchemaVersion &&
		a.ArgsHash == b.ArgsHash && a.FrozenArgsRef == b.FrozenArgsRef &&
		a.ArgsSummary == b.ArgsSummary && a.IdempotencyKey == b.IdempotencyKey
}

func validateAgentToolExecutionTransitionInput(t AgentToolExecutionTransition) error {
	if strings.TrimSpace(t.TenantID) == "" || strings.TrimSpace(t.CallID) == "" {
		return fmt.Errorf("tenant_id and call_id are required")
	}
	if !validSHA256(t.ArgsHash) {
		return fmt.Errorf("args_hash must be a lowercase SHA-256 hex digest")
	}
	if t.ExpectedStatus == "" || t.NextStatus == "" || t.OccurredAt.IsZero() {
		return fmt.Errorf("expected_status, next_status and occurred_at are required")
	}
	if !allowedAgentToolExecutionTransition(t.ExpectedStatus, t.NextStatus) {
		return fmt.Errorf("%w: tool execution %s -> %s", ErrAgentInvalidTransition, t.ExpectedStatus, t.NextStatus)
	}
	return nil
}

func allowedAgentToolExecutionTransition(from, to string) bool {
	switch from {
	case model.AgentToolExecutionStatusPrepared:
		return to == model.AgentToolExecutionStatusReady || to == model.AgentToolExecutionStatusFailedFinal || to == model.AgentToolExecutionStatusCancelled
	case model.AgentToolExecutionStatusReady:
		return to == model.AgentToolExecutionStatusExecuting || to == model.AgentToolExecutionStatusFailedFinal || to == model.AgentToolExecutionStatusCancelled
	case model.AgentToolExecutionStatusExecuting:
		return to == model.AgentToolExecutionStatusSucceeded || to == model.AgentToolExecutionStatusFailedRetryable ||
			to == model.AgentToolExecutionStatusFailedFinal || to == model.AgentToolExecutionStatusCancelled
	case model.AgentToolExecutionStatusFailedRetryable:
		return to == model.AgentToolExecutionStatusExecuting || to == model.AgentToolExecutionStatusFailedFinal || to == model.AgentToolExecutionStatusCancelled
	default:
		return false
	}
}

func buildAgentToolExecutionTransition(t AgentToolExecutionTransition) (map[string]any, error) {
	updates := map[string]any{"status": t.NextStatus}
	switch t.NextStatus {
	case model.AgentToolExecutionStatusReady:
		if t.ApprovalVersion < 1 {
			return nil, fmt.Errorf("ready execution requires approval_version")
		}
		updates["ready_at"] = t.OccurredAt
		updates["approval_version"] = t.ApprovalVersion
	case model.AgentToolExecutionStatusExecuting:
		updates["started_at"] = t.OccurredAt
		updates["attempt"] = gorm.Expr("attempt + 1")
	case model.AgentToolExecutionStatusSucceeded:
		if strings.TrimSpace(t.ResultRef) == "" || strings.TrimSpace(t.ResultSummary) == "" || !validSHA256(t.ResultHash) {
			return nil, fmt.Errorf("successful execution requires result_ref, result_summary and result_hash")
		}
		updates["finished_at"] = t.OccurredAt
		updates["result_ref"] = t.ResultRef
		updates["result_summary"] = t.ResultSummary
		updates["result_hash"] = t.ResultHash
		updates["downstream_operation_id"] = t.DownstreamOperationID
	case model.AgentToolExecutionStatusFailedRetryable:
		if strings.TrimSpace(t.ErrorCode) == "" {
			return nil, fmt.Errorf("failed execution requires error_code")
		}
		updates["last_failed_at"] = t.OccurredAt
		updates["error_code"] = t.ErrorCode
		updates["error_summary"] = t.ErrorSummary
	case model.AgentToolExecutionStatusFailedFinal:
		if strings.TrimSpace(t.ErrorCode) == "" {
			return nil, fmt.Errorf("failed execution requires error_code")
		}
		updates["last_failed_at"] = t.OccurredAt
		updates["finished_at"] = t.OccurredAt
		updates["error_code"] = t.ErrorCode
		updates["error_summary"] = t.ErrorSummary
	case model.AgentToolExecutionStatusCancelled:
		updates["finished_at"] = t.OccurredAt
		updates["error_code"] = t.ErrorCode
		updates["error_summary"] = t.ErrorSummary
	default:
		return nil, fmt.Errorf("%w: unknown tool execution status %s", ErrAgentInvalidTransition, t.NextStatus)
	}
	return updates, nil
}

func toolExecutionTransitionAlreadyApplied(current *model.AgentToolExecution, t AgentToolExecutionTransition) bool {
	switch t.NextStatus {
	case model.AgentToolExecutionStatusReady:
		return sameTime(current.ReadyAt, t.OccurredAt) && current.ApprovalVersion == t.ApprovalVersion
	case model.AgentToolExecutionStatusExecuting:
		return sameTime(current.StartedAt, t.OccurredAt)
	case model.AgentToolExecutionStatusSucceeded:
		return sameTime(current.FinishedAt, t.OccurredAt) && current.ResultRef == t.ResultRef &&
			current.ResultSummary == t.ResultSummary && current.ResultHash == t.ResultHash &&
			current.DownstreamOperationID == t.DownstreamOperationID
	case model.AgentToolExecutionStatusFailedRetryable:
		return sameTime(current.LastFailedAt, t.OccurredAt) && current.ErrorCode == t.ErrorCode && current.ErrorSummary == t.ErrorSummary
	case model.AgentToolExecutionStatusFailedFinal:
		return sameTime(current.FinishedAt, t.OccurredAt) && current.ErrorCode == t.ErrorCode && current.ErrorSummary == t.ErrorSummary
	case model.AgentToolExecutionStatusCancelled:
		return sameTime(current.FinishedAt, t.OccurredAt) && current.ErrorCode == t.ErrorCode && current.ErrorSummary == t.ErrorSummary
	default:
		return false
	}
}

func validateNewAgentAudit(entry *model.AgentAuditLog) error {
	if entry == nil {
		return fmt.Errorf("audit entry cannot be nil")
	}
	if strings.TrimSpace(entry.TenantID) == "" || strings.TrimSpace(entry.AuditID) == "" || strings.TrimSpace(entry.RunID) == "" {
		return fmt.Errorf("tenant_id, audit_id and run_id are required")
	}
	if strings.TrimSpace(entry.EventType) == "" || strings.TrimSpace(entry.ActorType) == "" || strings.TrimSpace(entry.ActorID) == "" {
		return fmt.Errorf("event_type, actor_type and actor_id are required")
	}
	if strings.TrimSpace(entry.Summary) == "" || entry.OccurredAt.IsZero() {
		return fmt.Errorf("summary and occurred_at are required")
	}
	return nil
}

func sameAgentAuditInput(a, b *model.AgentAuditLog) bool {
	return a.TenantID == b.TenantID && a.AuditID == b.AuditID && a.RunID == b.RunID &&
		a.CallID == b.CallID && a.EventType == b.EventType && a.ActorType == b.ActorType &&
		a.ActorID == b.ActorID && a.Summary == b.Summary && a.DetailRef == b.DetailRef &&
		a.OccurredAt.Equal(b.OccurredAt)
}

func hashAgentAuditEntry(entry *model.AgentAuditLog) (string, error) {
	payload := struct {
		Domain     string `json:"domain"`
		TenantID   string `json:"tenant_id"`
		AuditID    string `json:"audit_id"`
		RunID      string `json:"run_id"`
		Sequence   int64  `json:"sequence"`
		CallID     string `json:"call_id"`
		EventType  string `json:"event_type"`
		ActorType  string `json:"actor_type"`
		ActorID    string `json:"actor_id"`
		Summary    string `json:"summary"`
		DetailRef  string `json:"detail_ref"`
		OccurredNS int64  `json:"occurred_ns"`
		PrevHash   string `json:"prev_hash"`
	}{
		Domain:     "resonance.agent.audit.v1",
		TenantID:   entry.TenantID,
		AuditID:    entry.AuditID,
		RunID:      entry.RunID,
		Sequence:   entry.Sequence,
		CallID:     entry.CallID,
		EventType:  entry.EventType,
		ActorType:  entry.ActorType,
		ActorID:    entry.ActorID,
		Summary:    entry.Summary,
		DetailRef:  entry.DetailRef,
		OccurredNS: entry.OccurredAt.UTC().UnixNano(),
		PrevHash:   entry.PrevHash,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal agent audit hash payload: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func sameTime(value *time.Time, expected time.Time) bool {
	return value != nil && value.Equal(expected)
}
