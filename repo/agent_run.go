package repo

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ceyewan/resonance/model"
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

var (
	agentRunPendingStatuses = []string{
		model.AgentRunStatusQueued,
		model.AgentRunStatusFailedRetryable,
	}
	agentRunExecutionStatuses = []string{
		model.AgentRunStatusClaimed,
		model.AgentRunStatusStartingRuntime,
		model.AgentRunStatusRunning,
	}
	agentRunPreparedStatuses = []string{
		model.AgentRunStatusReadyToCommit,
		model.AgentRunStatusCommitting,
	}
	agentRunCancellableStatuses = append(append([]string{}, agentRunExecutionStatuses...), agentRunPreparedStatuses...)
)

type AgentRunRepoOption func(*agentRunRepoOptions)

type agentRunRepoOptions struct {
	logger clog.Logger
}

func WithAgentRunRepoLogger(logger clog.Logger) AgentRunRepoOption {
	return func(options *agentRunRepoOptions) {
		options.logger = logger
	}
}

type agentRunRepo struct {
	db        db.DB
	logger    clog.Logger
	budgetNow func(*gorm.DB) (time.Time, error)
}

func NewAgentRunRepo(database db.DB, opts ...AgentRunRepoOption) (AgentRunRepo, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}

	options := &agentRunRepoOptions{}
	for _, option := range opts {
		option(options)
	}

	logger := options.logger
	if logger == nil {
		logger = clog.Discard()
	}

	return &agentRunRepo{
		db: database, logger: logger.WithNamespace("agent_run_repo"), budgetNow: databaseBudgetNow,
	}, nil
}

func (r *agentRunRepo) EnqueueAgentRun(ctx context.Context, run *model.AgentRun) (*AgentRunEnqueueResult, error) {
	if err := validateNewAgentRun(run); err != nil {
		return nil, err
	}

	candidate := *run
	now := time.Now().UTC()
	if candidate.Status == "" {
		candidate.Status = model.AgentRunStatusQueued
	}
	if candidate.Version == 0 {
		candidate.Version = 1
	}
	if candidate.QueuedAt.IsZero() {
		candidate.QueuedAt = now
	}
	if candidate.AvailableAt.IsZero() {
		candidate.AvailableAt = candidate.QueuedAt
	}

	var result *AgentRunEnqueueResult
	err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		created := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "source_event_id"}},
			DoNothing: true,
		}).Create(&candidate)
		if created.Error != nil {
			return fmt.Errorf("enqueue agent run: %w", created.Error)
		}
		if created.RowsAffected == 1 {
			result = &AgentRunEnqueueResult{Run: &candidate, Created: true}
			return nil
		}
		if created.RowsAffected != 0 {
			return fmt.Errorf("unexpected enqueued agent run count: %d", created.RowsAffected)
		}

		var existing model.AgentRun
		if err := tx.Where("tenant_id = ? AND source_event_id = ?", candidate.TenantID, candidate.SourceEventID).
			Take(&existing).Error; err != nil {
			return fmt.Errorf("load idempotent agent run: %w", err)
		}
		if !sameAgentRunSource(&existing, &candidate) {
			return ErrAgentRunSourceConflict
		}
		result = &AgentRunEnqueueResult{Run: &existing, Created: false}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result == nil || result.Run == nil {
		return nil, fmt.Errorf("enqueue agent run returned no result")
	}
	return result, nil
}

func (r *agentRunRepo) GetAgentRun(ctx context.Context, tenantID, runID string) (*model.AgentRun, error) {
	if err := validateTenantAndRunID(tenantID, runID); err != nil {
		return nil, err
	}
	return loadAgentRun(r.db.DB(ctx), tenantID, runID)
}

func (r *agentRunRepo) GetAgentSessionBinding(ctx context.Context, tenantID, conversationID string) (*model.AgentSessionBinding, error) {
	if !validBoundedString(tenantID, 64) || !validBoundedString(conversationID, 64) {
		return nil, fmt.Errorf("tenant_id and conversation_id must contain 1 to 64 bytes")
	}
	return loadAgentSessionBinding(r.db.DB(ctx), tenantID, conversationID)
}

func (r *agentRunRepo) MarkAgentSessionBindingDirty(
	ctx context.Context,
	tenantID, conversationID string,
	expectedGeneration int64,
	now time.Time,
) (*model.AgentSessionBinding, error) {
	if !validBoundedString(tenantID, 64) || !validBoundedString(conversationID, 64) {
		return nil, fmt.Errorf("tenant_id and conversation_id must contain 1 to 64 bytes")
	}
	if expectedGeneration < 1 || now.IsZero() {
		return nil, fmt.Errorf("expected_generation and current time are required")
	}

	result := r.db.DB(ctx).Model(&model.AgentSessionBinding{}).
		Where("tenant_id = ? AND conversation_id = ? AND generation = ? AND status = ?",
			tenantID, conversationID, expectedGeneration, model.AgentSessionBindingStatusActive).
		Updates(map[string]any{
			"status":     model.AgentSessionBindingStatusDirty,
			"version":    gorm.Expr("version + 1"),
			"updated_at": now.UTC(),
		})
	if result.Error != nil {
		return nil, fmt.Errorf("mark agent session binding dirty: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		existing, err := loadAgentSessionBinding(r.db.DB(ctx), tenantID, conversationID)
		if err != nil {
			return nil, err
		}
		if existing.Generation == expectedGeneration && existing.Status == model.AgentSessionBindingStatusDirty {
			return existing, nil
		}
		return nil, ErrAgentSessionBindingConflict
	}
	return loadAgentSessionBinding(r.db.DB(ctx), tenantID, conversationID)
}

func (r *agentRunRepo) ClaimNextAgentRun(ctx context.Context, claim AgentRunClaim) (*model.AgentRun, error) {
	if err := validateClaim(claim); err != nil {
		return nil, err
	}

	var claimed *model.AgentRun
	err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		var candidate model.AgentRun
		query := tx.Table(model.AgentRun{}.TableName()+" AS candidate").
			Where("candidate.tenant_id = ?", claim.TenantID).
			Where("candidate.profile_id = ? AND candidate.profile_version = ?", claim.ProfileID, claim.ProfileVersion).
			Where("candidate.status IN ?", agentRunPendingStatuses).
			Where("candidate.available_at <= ?", claim.Now).
			Where("candidate.attempt < candidate.max_attempts").
			Where(`NOT EXISTS (
				SELECT 1 FROM t_agent_run active
				WHERE active.tenant_id = candidate.tenant_id
				  AND active.conversation_id = candidate.conversation_id
				  AND active.status IN ?
			)`, append(agentRunExecutionStatuses, agentRunPreparedStatuses...)).
			Where(`NOT EXISTS (
				SELECT 1 FROM t_agent_run earlier
				WHERE earlier.tenant_id = candidate.tenant_id
				  AND earlier.conversation_id = candidate.conversation_id
				  AND earlier.status IN ?
				  AND (
					earlier.source_seq_id < candidate.source_seq_id
					OR (earlier.source_seq_id = candidate.source_seq_id AND earlier.queued_at < candidate.queued_at)
					OR (earlier.source_seq_id = candidate.source_seq_id AND earlier.queued_at = candidate.queued_at AND earlier.run_id < candidate.run_id)
				  )
			)`, agentRunPendingStatuses).
			Order("candidate.available_at ASC, candidate.queued_at ASC, candidate.source_seq_id ASC, candidate.run_id ASC").
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Take(&candidate)
		if errors.Is(query.Error, gorm.ErrRecordNotFound) {
			return ErrNoAgentRunAvailable
		}
		if query.Error != nil {
			return fmt.Errorf("select next agent run: %w", query.Error)
		}

		expiresAt := claim.Now.Add(claim.LeaseDuration)
		updates := map[string]any{
			"status":           model.AgentRunStatusClaimed,
			"attempt":          gorm.Expr("attempt + 1"),
			"lease_owner":      claim.WorkerID,
			"lease_token":      claim.LeaseToken,
			"lease_expires_at": expiresAt,
			"claimed_at":       claim.Now,
			"version":          gorm.Expr("version + 1"),
			"updated_at":       claim.Now,
		}
		updated := tx.Model(&model.AgentRun{}).
			Where("tenant_id = ? AND run_id = ? AND version = ? AND status IN ?",
				candidate.TenantID, candidate.RunID, candidate.Version, agentRunPendingStatuses).
			Updates(updates)
		if updated.Error != nil {
			return fmt.Errorf("claim agent run: %w", updated.Error)
		}
		if updated.RowsAffected != 1 {
			return ErrAgentRunFenceLost
		}

		var err error
		claimed, err = loadAgentRun(tx, candidate.TenantID, candidate.RunID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (r *agentRunRepo) HeartbeatAgentRun(ctx context.Context, lease AgentRunLease) (*model.AgentRun, error) {
	if err := validateLease(lease, true); err != nil {
		return nil, err
	}
	return r.updateFencedRun(ctx, lease, append(agentRunExecutionStatuses, agentRunPreparedStatuses...), map[string]any{
		"lease_expires_at": lease.Now.Add(lease.LeaseDuration),
		"version":          gorm.Expr("version + 1"),
		"updated_at":       lease.Now,
	})
}

func (r *agentRunRepo) AdvanceAgentRun(ctx context.Context, transition AgentRunTransition) (*model.AgentRun, error) {
	if err := validateLease(transition.Lease, true); err != nil {
		return nil, err
	}
	if !validExecutionAdvance(transition.From, transition.To) {
		return nil, fmt.Errorf("%w: %s -> %s", ErrAgentRunInvalidTransition, transition.From, transition.To)
	}

	updates := map[string]any{
		"status":           transition.To,
		"lease_expires_at": transition.Lease.Now.Add(transition.Lease.LeaseDuration),
		"version":          gorm.Expr("version + 1"),
		"updated_at":       transition.Lease.Now,
	}
	if transition.To == model.AgentRunStatusStartingRuntime {
		updates["started_at"] = transition.Lease.Now
	}
	return r.updateFencedRun(ctx, transition.Lease, []string{transition.From}, updates)
}

func (r *agentRunRepo) FailAgentRun(ctx context.Context, failure AgentRunFailure) (*model.AgentRun, error) {
	if err := validateLease(failure.Lease, false); err != nil {
		return nil, err
	}
	if failure.ErrorCode == "" || len(failure.ErrorCode) > 64 {
		return nil, fmt.Errorf("error_code must contain 1 to 64 bytes")
	}
	if len(failure.ErrorSummary) > 512 {
		return nil, fmt.Errorf("error_summary exceeds 512 bytes")
	}
	if failure.Retryable && failure.RetryAt.Before(failure.Lease.Now) {
		return nil, fmt.Errorf("retry_at cannot be before now")
	}

	var failed *model.AgentRun
	err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		current, err := lockFencedAgentRun(tx, failure.Lease, agentRunExecutionStatuses)
		if err != nil {
			return err
		}

		status := model.AgentRunStatusFailedFinal
		completedAt := any(failure.Lease.Now)
		availableAt := current.AvailableAt
		if failure.Retryable && current.Attempt < current.MaxAttempts {
			status = model.AgentRunStatusFailedRetryable
			completedAt = nil
			availableAt = failure.RetryAt
		}

		updated := tx.Model(&model.AgentRun{}).
			Where("tenant_id = ? AND run_id = ? AND version = ?", current.TenantID, current.RunID, current.Version).
			Updates(map[string]any{
				"status":               status,
				"available_at":         availableAt,
				"lease_owner":          "",
				"lease_token":          "",
				"lease_expires_at":     nil,
				"last_error_code":      failure.ErrorCode,
				"last_error_summary":   failure.ErrorSummary,
				"last_error_retryable": status == model.AgentRunStatusFailedRetryable,
				"completed_at":         completedAt,
				"version":              gorm.Expr("version + 1"),
				"updated_at":           failure.Lease.Now,
			})
		if updated.Error != nil {
			return fmt.Errorf("fail agent run: %w", updated.Error)
		}
		if updated.RowsAffected != 1 {
			return ErrAgentRunFenceLost
		}
		failed, err = loadAgentRun(tx, current.TenantID, current.RunID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return failed, nil
}

func (r *agentRunRepo) CancelAgentRun(ctx context.Context, cancellation AgentRunCancellation) (*model.AgentRun, error) {
	if err := validateLease(cancellation.Lease, false); err != nil {
		return nil, err
	}
	if err := validateCancellationReason(cancellation.ErrorCode, cancellation.ErrorSummary); err != nil {
		return nil, err
	}
	return r.updateFencedRun(ctx, cancellation.Lease, agentRunCancellableStatuses, map[string]any{
		"status":               model.AgentRunStatusCancelled,
		"lease_owner":          "",
		"lease_token":          "",
		"lease_expires_at":     nil,
		"last_error_code":      cancellation.ErrorCode,
		"last_error_summary":   cancellation.ErrorSummary,
		"last_error_retryable": false,
		"completed_at":         cancellation.Lease.Now,
		"version":              gorm.Expr("version + 1"),
		"updated_at":           cancellation.Lease.Now,
	})
}

func (r *agentRunRepo) CancelPendingAgentRuns(
	ctx context.Context,
	cancellation AgentPendingRunCancellation,
) (int64, error) {
	if !validBoundedString(cancellation.TenantID, 64) || !validBoundedString(cancellation.ActorID, 64) ||
		!validBoundedString(cancellation.ProfileID, 64) || cancellation.ProfileVersion < 1 || cancellation.Now.IsZero() {
		return 0, fmt.Errorf("tenant, actor, profile snapshot and current time are required")
	}
	if err := validateCancellationReason(cancellation.ErrorCode, cancellation.ErrorSummary); err != nil {
		return 0, err
	}
	result := r.db.DB(ctx).Model(&model.AgentRun{}).
		Where("tenant_id = ? AND actor_id = ? AND profile_id = ? AND profile_version = ? AND status IN ?",
			cancellation.TenantID, cancellation.ActorID, cancellation.ProfileID, cancellation.ProfileVersion, agentRunPendingStatuses).
		Updates(map[string]any{
			"status":               model.AgentRunStatusCancelled,
			"last_error_code":      cancellation.ErrorCode,
			"last_error_summary":   cancellation.ErrorSummary,
			"last_error_retryable": false,
			"completed_at":         cancellation.Now,
			"version":              gorm.Expr("version + 1"),
			"updated_at":           cancellation.Now,
		})
	if result.Error != nil {
		return 0, fmt.Errorf("cancel pending agent runs: %w", result.Error)
	}
	return result.RowsAffected, nil
}

func validateCancellationReason(code, summary string) error {
	if !validBoundedString(code, 64) {
		return fmt.Errorf("cancellation error_code must contain 1 to 64 bytes")
	}
	if len(summary) > 512 {
		return fmt.Errorf("cancellation error_summary exceeds 512 bytes")
	}
	return nil
}

func (r *agentRunRepo) PrepareAgentRun(ctx context.Context, prepared AgentRunPreparedResult) (*model.AgentRun, error) {
	if err := validatePreparedResult(prepared); err != nil {
		return nil, err
	}
	updates := map[string]any{
		"status":                  model.AgentRunStatusReadyToCommit,
		"base_session_generation": prepared.BaseSessionGeneration,
		"candidate_session_id":    prepared.CandidateSessionID,
		"candidate_session_ref":   prepared.CandidateSessionRef,
		"candidate_checksum":      prepared.CandidateChecksum,
		"candidate_leaf_entry_id": prepared.CandidateLeafEntryID,
		"candidate_session_bytes": prepared.CandidateSessionBytes,
		"candidate_entry_count":   prepared.CandidateEntryCount,
		"frozen_final_text":       prepared.FrozenFinalText,
		"final_client_msg_id":     prepared.FinalClientMsgID,
		"input_tokens":            prepared.InputTokens,
		"output_tokens":           prepared.OutputTokens,
		"cache_read_tokens":       prepared.CacheReadTokens,
		"cache_write_tokens":      prepared.CacheWriteTokens,
		"total_tokens":            prepared.TotalTokens,
		"usage_state":             prepared.UsageState,
		"cost_micros":             prepared.CostMicros,
		"cost":                    prepared.Cost,
		"prepared_at":             prepared.Lease.Now,
		"lease_expires_at":        prepared.Lease.Now.Add(prepared.Lease.LeaseDuration),
		"version":                 gorm.Expr("version + 1"),
		"updated_at":              prepared.Lease.Now,
	}
	return r.updateFencedRun(ctx, prepared.Lease, []string{model.AgentRunStatusRunning}, updates)
}

func (r *agentRunRepo) BeginAgentRunCommit(ctx context.Context, lease AgentRunLease) (*model.AgentRun, error) {
	if err := validateLease(lease, true); err != nil {
		return nil, err
	}
	return r.updateFencedRun(ctx, lease, []string{model.AgentRunStatusReadyToCommit}, map[string]any{
		"status":           model.AgentRunStatusCommitting,
		"lease_expires_at": lease.Now.Add(lease.LeaseDuration),
		"version":          gorm.Expr("version + 1"),
		"updated_at":       lease.Now,
	})
}

func (r *agentRunRepo) ClaimPreparedAgentRun(ctx context.Context, claim AgentRunClaim) (*model.AgentRun, error) {
	if err := validateClaim(claim); err != nil {
		return nil, err
	}

	var claimed *model.AgentRun
	err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		var candidate model.AgentRun
		selected := tx.Table(model.AgentRun{}.TableName()+" AS candidate").
			Where("candidate.tenant_id = ?", claim.TenantID).
			Where("candidate.profile_id = ? AND candidate.profile_version = ?", claim.ProfileID, claim.ProfileVersion).
			Where("candidate.status IN ?", agentRunPreparedStatuses).
			Where("candidate.lease_expires_at IS NULL OR candidate.lease_expires_at <= ?", claim.Now).
			Order("candidate.prepared_at ASC, candidate.run_id ASC").
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Take(&candidate)
		if errors.Is(selected.Error, gorm.ErrRecordNotFound) {
			return ErrNoAgentRunAvailable
		}
		if selected.Error != nil {
			return fmt.Errorf("select prepared agent run: %w", selected.Error)
		}

		updated := tx.Model(&model.AgentRun{}).
			Where("tenant_id = ? AND run_id = ? AND version = ? AND status IN ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)",
				candidate.TenantID, candidate.RunID, candidate.Version, agentRunPreparedStatuses, claim.Now).
			Updates(map[string]any{
				"status":           model.AgentRunStatusCommitting,
				"lease_owner":      claim.WorkerID,
				"lease_token":      claim.LeaseToken,
				"lease_expires_at": claim.Now.Add(claim.LeaseDuration),
				"version":          gorm.Expr("version + 1"),
				"updated_at":       claim.Now,
			})
		if updated.Error != nil {
			return fmt.Errorf("claim prepared agent run: %w", updated.Error)
		}
		if updated.RowsAffected != 1 {
			return ErrAgentRunFenceLost
		}
		var err error
		claimed, err = loadAgentRun(tx, candidate.TenantID, candidate.RunID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (r *agentRunRepo) RecordAgentRunFinalMessage(ctx context.Context, result AgentRunFinalMessage) (*model.AgentRun, error) {
	if err := validateLease(result.Lease, false); err != nil {
		return nil, err
	}
	if result.EventID <= 0 || result.SeqID <= 0 || result.TimestampMs <= 0 {
		return nil, fmt.Errorf("final message event_id, seq_id and timestamp_ms must be positive")
	}

	var recorded *model.AgentRun
	err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		current, err := lockAgentRun(tx, result.Lease.TenantID, result.Lease.RunID)
		if err != nil {
			return err
		}
		if err := verifyCurrentLease(current, result.Lease, []string{model.AgentRunStatusCommitting}); err != nil {
			return err
		}
		if current.FinalEventID != 0 || current.FinalSeqID != 0 || current.FinalTimestampMs != 0 {
			if current.FinalEventID == result.EventID && current.FinalSeqID == result.SeqID && current.FinalTimestampMs == result.TimestampMs {
				recorded = current
				return nil
			}
			return ErrAgentRunCommitConflict
		}
		if current.Version != result.Lease.ExpectedVersion {
			return ErrAgentRunFenceLost
		}

		updated := tx.Model(&model.AgentRun{}).
			Where("tenant_id = ? AND run_id = ? AND version = ?", current.TenantID, current.RunID, current.Version).
			Updates(map[string]any{
				"final_event_id":     result.EventID,
				"final_seq_id":       result.SeqID,
				"final_timestamp_ms": result.TimestampMs,
				"version":            gorm.Expr("version + 1"),
				"updated_at":         result.Lease.Now,
			})
		if updated.Error != nil {
			return fmt.Errorf("record final agent message: %w", updated.Error)
		}
		if updated.RowsAffected != 1 {
			return ErrAgentRunFenceLost
		}
		recorded, err = loadAgentRun(tx, current.TenantID, current.RunID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return recorded, nil
}

func (r *agentRunRepo) CompleteAgentRun(ctx context.Context, completion AgentRunCompletion) (*model.AgentRun, error) {
	if err := validateLease(completion.Lease, false); err != nil {
		return nil, err
	}
	if completion.CommittedGeneration < 1 {
		return nil, fmt.Errorf("committed_generation must be positive")
	}

	var completed *model.AgentRun
	err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		current, err := lockAgentRun(tx, completion.Lease.TenantID, completion.Lease.RunID)
		if err != nil {
			return err
		}
		if current.Status == model.AgentRunStatusSucceeded &&
			(current.CommittedGeneration == completion.CommittedGeneration ||
				(current.SessionInvalidatedAt != nil && current.CommittedGeneration == 0)) {
			completed = current
			return nil
		}
		if err := verifyCurrentLease(current, completion.Lease, []string{model.AgentRunStatusCommitting}); err != nil {
			return err
		}
		if current.Version != completion.Lease.ExpectedVersion {
			return ErrAgentRunFenceLost
		}
		if current.FinalEventID <= 0 || current.FinalSeqID <= 0 || current.FinalTimestampMs <= 0 {
			return fmt.Errorf("%w: final message is not recorded", ErrAgentRunInvalidTransition)
		}
		if completion.CommittedGeneration != current.BaseSessionGeneration+1 {
			return fmt.Errorf("%w: committed generation must advance the prepared base by one", ErrAgentRunCommitConflict)
		}
		committedGeneration := completion.CommittedGeneration
		if current.SessionInvalidatedAt == nil {
			if err := commitAgentSessionBinding(tx, current, completion.CommittedGeneration, completion.Lease.Now); err != nil {
				return err
			}
		} else {
			// The final ChatEvent is already a durable Logic fact and cannot be
			// rolled back. Keep the Binding dirty/absent and let GC reclaim the
			// stale Candidate instead of promoting invalidated history.
			committedGeneration = 0
		}

		updated := tx.Model(&model.AgentRun{}).
			Where("tenant_id = ? AND run_id = ? AND version = ?", current.TenantID, current.RunID, current.Version).
			Updates(map[string]any{
				"status":               model.AgentRunStatusSucceeded,
				"committed_generation": committedGeneration,
				"lease_owner":          "",
				"lease_token":          "",
				"lease_expires_at":     nil,
				"completed_at":         completion.Lease.Now,
				"version":              gorm.Expr("version + 1"),
				"updated_at":           completion.Lease.Now,
			})
		if updated.Error != nil {
			return fmt.Errorf("complete agent run: %w", updated.Error)
		}
		if updated.RowsAffected != 1 {
			return ErrAgentRunFenceLost
		}
		completed, err = loadAgentRun(tx, current.TenantID, current.RunID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return completed, nil
}

func (r *agentRunRepo) RecoverExpiredAgentRuns(ctx context.Context, tenantID string, now time.Time) (AgentRunRecoveryResult, error) {
	if !validBoundedString(tenantID, 64) {
		return AgentRunRecoveryResult{}, fmt.Errorf("tenant_id must contain 1 to 64 bytes")
	}
	if now.IsZero() {
		return AgentRunRecoveryResult{}, fmt.Errorf("now cannot be zero")
	}
	now = now.UTC()
	var recovered AgentRunRecoveryResult
	err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		retryable := tx.Model(&model.AgentRun{}).
			Where("tenant_id = ? AND status IN ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ? AND attempt < max_attempts", tenantID, agentRunExecutionStatuses, now).
			Updates(map[string]any{
				"status":               model.AgentRunStatusFailedRetryable,
				"available_at":         now,
				"lease_owner":          "",
				"lease_token":          "",
				"lease_expires_at":     nil,
				"last_error_code":      "lease_expired",
				"last_error_summary":   "worker lease expired before result preparation",
				"last_error_retryable": true,
				"version":              gorm.Expr("version + 1"),
				"updated_at":           now,
			})
		if retryable.Error != nil {
			return fmt.Errorf("recover retryable agent runs: %w", retryable.Error)
		}
		recovered.RetryableExecutions = retryable.RowsAffected

		finalFailures := tx.Model(&model.AgentRun{}).
			Where("tenant_id = ? AND status IN ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ? AND attempt >= max_attempts", tenantID, agentRunExecutionStatuses, now).
			Updates(map[string]any{
				"status":               model.AgentRunStatusFailedFinal,
				"lease_owner":          "",
				"lease_token":          "",
				"lease_expires_at":     nil,
				"last_error_code":      "lease_expired",
				"last_error_summary":   "worker lease expired after the final allowed attempt",
				"last_error_retryable": false,
				"completed_at":         now,
				"version":              gorm.Expr("version + 1"),
				"updated_at":           now,
			})
		if finalFailures.Error != nil {
			return fmt.Errorf("finalize expired agent runs: %w", finalFailures.Error)
		}
		recovered.FinalFailures = finalFailures.RowsAffected

		prepared := tx.Model(&model.AgentRun{}).
			Where("tenant_id = ? AND status IN ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?", tenantID, agentRunPreparedStatuses, now).
			Updates(map[string]any{
				"status":           model.AgentRunStatusReadyToCommit,
				"lease_owner":      "",
				"lease_token":      "",
				"lease_expires_at": nil,
				"version":          gorm.Expr("version + 1"),
				"updated_at":       now,
			})
		if prepared.Error != nil {
			return fmt.Errorf("recover prepared agent runs: %w", prepared.Error)
		}
		recovered.PreparedRuns = prepared.RowsAffected
		return nil
	})
	return recovered, err
}

// WithAgentSessionGCLock keeps the advisory lock and live-reference snapshot
// in one PostgreSQL transaction while the caller prunes the shared object
// store. References are intentionally not tenant-filtered: one Session root
// may serve multiple tenant-scoped Pilot pools.
func (r *agentRunRepo) WithAgentSessionGCLock(
	ctx context.Context,
	lockID string,
	prune func(context.Context, []string) error,
) (bool, error) {
	if strings.TrimSpace(lockID) == "" || len(lockID) > 128 || prune == nil {
		return false, fmt.Errorf("agent session GC lock id and callback are required")
	}
	acquired := false
	err := r.db.DB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Raw("SELECT pg_try_advisory_xact_lock(hashtextextended(?, 0))", lockID).Scan(&acquired).Error; err != nil {
			return fmt.Errorf("acquire agent session GC lock: %w", err)
		}
		if !acquired {
			return nil
		}
		var references []string
		if err := tx.Raw(`
			SELECT session_ref AS reference
			FROM t_agent_session_binding
			WHERE session_ref <> ''
			UNION
			SELECT candidate_session_ref AS reference
			FROM t_agent_run
			WHERE candidate_session_ref <> ''
			  AND status IN ?
			ORDER BY reference ASC`, []string{
			model.AgentRunStatusQueued,
			model.AgentRunStatusClaimed,
			model.AgentRunStatusStartingRuntime,
			model.AgentRunStatusRunning,
			model.AgentRunStatusReadyToCommit,
			model.AgentRunStatusCommitting,
			model.AgentRunStatusFailedRetryable,
		}).Scan(&references).Error; err != nil {
			return fmt.Errorf("snapshot live agent session references: %w", err)
		}
		if err := prune(ctx, references); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return acquired, err
	}
	return acquired, nil
}

func (r *agentRunRepo) updateFencedRun(
	ctx context.Context,
	lease AgentRunLease,
	statuses []string,
	updates map[string]any,
) (*model.AgentRun, error) {
	result := r.db.DB(ctx).Model(&model.AgentRun{}).
		Where("tenant_id = ? AND run_id = ? AND status IN ? AND lease_owner = ? AND lease_token = ? AND version = ? AND lease_expires_at > ?",
			lease.TenantID, lease.RunID, statuses, lease.WorkerID, lease.LeaseToken, lease.ExpectedVersion, lease.Now).
		Updates(updates)
	if result.Error != nil {
		return nil, fmt.Errorf("update fenced agent run: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return nil, ErrAgentRunFenceLost
	}
	return loadAgentRun(r.db.DB(ctx), lease.TenantID, lease.RunID)
}

func lockFencedAgentRun(tx *gorm.DB, lease AgentRunLease, statuses []string) (*model.AgentRun, error) {
	current, err := lockAgentRun(tx, lease.TenantID, lease.RunID)
	if err != nil {
		return nil, err
	}
	if err := verifyCurrentLease(current, lease, statuses); err != nil {
		return nil, err
	}
	if current.Version != lease.ExpectedVersion {
		return nil, ErrAgentRunFenceLost
	}
	return current, nil
}

func lockAgentRun(tx *gorm.DB, tenantID, runID string) (*model.AgentRun, error) {
	var run model.AgentRun
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND run_id = ?", tenantID, runID).
		Take(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAgentRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock agent run: %w", err)
	}
	return &run, nil
}

func verifyCurrentLease(current *model.AgentRun, lease AgentRunLease, statuses []string) error {
	if !containsStatus(statuses, current.Status) || current.LeaseOwner != lease.WorkerID || current.LeaseToken != lease.LeaseToken ||
		current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.After(lease.Now) {
		return ErrAgentRunFenceLost
	}
	return nil
}

func loadAgentRun(database *gorm.DB, tenantID, runID string) (*model.AgentRun, error) {
	var run model.AgentRun
	err := database.Where("tenant_id = ? AND run_id = ?", tenantID, runID).Take(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAgentRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load agent run: %w", err)
	}
	return &run, nil
}

func loadAgentSessionBinding(database *gorm.DB, tenantID, conversationID string) (*model.AgentSessionBinding, error) {
	var binding model.AgentSessionBinding
	err := database.Where("tenant_id = ? AND conversation_id = ?", tenantID, conversationID).Take(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAgentSessionBindingNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load agent session binding: %w", err)
	}
	return &binding, nil
}

func commitAgentSessionBinding(tx *gorm.DB, run *model.AgentRun, generation int64, now time.Time) error {
	var current model.AgentSessionBinding
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND conversation_id = ?", run.TenantID, run.ConversationID).
		Take(&current).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if run.BaseSessionGeneration != 0 || generation != 1 {
			return ErrAgentSessionBindingConflict
		}
		binding := model.AgentSessionBinding{
			TenantID:             run.TenantID,
			ConversationID:       run.ConversationID,
			RuntimeKind:          run.RuntimeKind,
			RuntimeVersion:       run.RuntimeVersion,
			BridgeVersion:        run.BridgeVersion,
			RuntimeSessionID:     run.CandidateSessionID,
			SessionRef:           run.CandidateSessionRef,
			Checksum:             run.CandidateChecksum,
			ProfileID:            run.ProfileID,
			ProfileVersion:       run.ProfileVersion,
			Generation:           generation,
			LastCommittedEntryID: run.CandidateLeafEntryID,
			SessionBytes:         run.CandidateSessionBytes,
			EntryCount:           run.CandidateEntryCount,
			Status:               model.AgentSessionBindingStatusActive,
			Version:              1,
			CreatedAt:            now,
			UpdatedAt:            now,
		}
		if createErr := tx.Create(&binding).Error; createErr != nil {
			return fmt.Errorf("create agent session binding: %w", createErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock agent session binding: %w", err)
	}

	if current.Generation == generation {
		if sameCommittedBinding(&current, run) {
			return nil
		}
		return ErrAgentSessionBindingConflict
	}
	if current.Generation != run.BaseSessionGeneration ||
		(current.Status != model.AgentSessionBindingStatusActive && current.Status != model.AgentSessionBindingStatusDirty) {
		return ErrAgentSessionBindingConflict
	}

	updated := tx.Model(&model.AgentSessionBinding{}).
		Where("tenant_id = ? AND conversation_id = ? AND generation = ? AND version = ? AND status IN ?",
			current.TenantID, current.ConversationID, current.Generation, current.Version,
			[]string{model.AgentSessionBindingStatusActive, model.AgentSessionBindingStatusDirty}).
		Updates(map[string]any{
			"runtime_kind":            run.RuntimeKind,
			"runtime_version":         run.RuntimeVersion,
			"bridge_version":          run.BridgeVersion,
			"runtime_session_id":      run.CandidateSessionID,
			"session_ref":             run.CandidateSessionRef,
			"checksum":                run.CandidateChecksum,
			"profile_id":              run.ProfileID,
			"profile_version":         run.ProfileVersion,
			"generation":              generation,
			"last_committed_entry_id": run.CandidateLeafEntryID,
			"session_bytes":           run.CandidateSessionBytes,
			"entry_count":             run.CandidateEntryCount,
			"status":                  model.AgentSessionBindingStatusActive,
			"version":                 gorm.Expr("version + 1"),
			"updated_at":              now,
		})
	if updated.Error != nil {
		return fmt.Errorf("commit agent session binding: %w", updated.Error)
	}
	if updated.RowsAffected != 1 {
		return ErrAgentSessionBindingConflict
	}
	return nil
}

func sameCommittedBinding(binding *model.AgentSessionBinding, run *model.AgentRun) bool {
	return binding.RuntimeKind == run.RuntimeKind &&
		binding.RuntimeVersion == run.RuntimeVersion &&
		binding.BridgeVersion == run.BridgeVersion &&
		binding.RuntimeSessionID == run.CandidateSessionID &&
		binding.SessionRef == run.CandidateSessionRef &&
		binding.Checksum == run.CandidateChecksum &&
		binding.ProfileID == run.ProfileID &&
		binding.ProfileVersion == run.ProfileVersion &&
		binding.LastCommittedEntryID == run.CandidateLeafEntryID &&
		binding.SessionBytes == run.CandidateSessionBytes &&
		binding.EntryCount == run.CandidateEntryCount &&
		binding.Status == model.AgentSessionBindingStatusActive
}

func validateNewAgentRun(run *model.AgentRun) error {
	if run == nil {
		return fmt.Errorf("agent run cannot be nil")
	}
	if err := validateTenantAndRunID(run.TenantID, run.RunID); err != nil {
		return err
	}
	if !validBoundedString(run.ConversationID, 64) || !validBoundedString(run.ActorID, 64) ||
		!validBoundedString(run.ActorUsername, 64) {
		return fmt.Errorf("conversation_id, actor_id and actor_username must contain 1 to 64 bytes")
	}
	if len(run.RunID) > 52 {
		return fmt.Errorf("run_id exceeds 52 bytes and cannot form the final message idempotency key")
	}
	if run.SourceEventID <= 0 || run.SourceSeqID <= 0 || run.SourceTimestampMs <= 0 {
		return fmt.Errorf("source event_id, seq_id and timestamp_ms must be positive")
	}
	if !sha256Pattern.MatchString(run.SourceHash) {
		return fmt.Errorf("source_hash must be a lowercase SHA-256 hex digest")
	}
	if run.Prompt == "" {
		return fmt.Errorf("prompt cannot be empty")
	}
	if len(run.TraceContext) > 16*1024 {
		return fmt.Errorf("trace_context exceeds 16 KiB")
	}
	if !validBoundedString(run.ProfileID, 64) || run.ProfileVersion <= 0 ||
		!validBoundedString(run.RuntimeKind, 32) || !validBoundedString(run.RuntimeVersion, 64) ||
		!validBoundedString(run.BridgeVersion, 64) ||
		!validBoundedString(run.ModelProvider, 64) || !validBoundedString(run.ModelID, 128) {
		return fmt.Errorf("profile, runtime and model snapshot is incomplete")
	}
	if run.MaxAttempts < 1 || run.MaxAttempts > 100 {
		return fmt.Errorf("max_attempts must be between 1 and 100")
	}
	if run.Status != "" && run.Status != model.AgentRunStatusQueued {
		return fmt.Errorf("new agent run status must be QUEUED")
	}
	if run.Attempt != 0 || (run.Version != 0 && run.Version != 1) || run.LeaseOwner != "" || run.LeaseToken != "" || run.LeaseExpiresAt != nil {
		return fmt.Errorf("new agent run contains execution state")
	}
	return nil
}

func validateTenantAndRunID(tenantID, runID string) error {
	if !validBoundedString(tenantID, 64) || !validBoundedString(runID, 64) {
		return fmt.Errorf("tenant_id and run_id must contain 1 to 64 bytes")
	}
	return nil
}

func validateClaim(claim AgentRunClaim) error {
	if !validBoundedString(claim.TenantID, 64) || !validBoundedString(claim.ProfileID, 64) || claim.ProfileVersion < 1 ||
		!validBoundedString(claim.WorkerID, 128) || !validBoundedString(claim.LeaseToken, 128) {
		return fmt.Errorf("tenant_id, profile snapshot, worker_id and lease_token are required")
	}
	if claim.Now.IsZero() {
		return fmt.Errorf("claim time cannot be zero")
	}
	if claim.LeaseDuration <= 0 {
		return fmt.Errorf("lease duration must be positive")
	}
	return nil
}

func validateLease(lease AgentRunLease, requireDuration bool) error {
	if err := validateTenantAndRunID(lease.TenantID, lease.RunID); err != nil {
		return err
	}
	if !validBoundedString(lease.WorkerID, 128) || !validBoundedString(lease.LeaseToken, 128) {
		return fmt.Errorf("worker_id and lease_token are required")
	}
	if lease.ExpectedVersion < 1 || lease.Now.IsZero() {
		return fmt.Errorf("expected_version and current time are required")
	}
	if requireDuration && lease.LeaseDuration <= 0 {
		return fmt.Errorf("lease duration must be positive")
	}
	return nil
}

func validatePreparedResult(prepared AgentRunPreparedResult) error {
	if err := validateLease(prepared.Lease, true); err != nil {
		return err
	}
	if prepared.BaseSessionGeneration < 0 || !validBoundedString(prepared.CandidateSessionID, 128) ||
		prepared.CandidateSessionRef == "" || !sha256Pattern.MatchString(prepared.CandidateChecksum) ||
		!validBoundedString(prepared.CandidateLeafEntryID, 128) || prepared.CandidateSessionBytes < 1 ||
		prepared.CandidateEntryCount < 1 || prepared.FrozenFinalText == "" {
		return fmt.Errorf("prepared session and frozen result are incomplete")
	}
	expectedClientID := "agent:" + prepared.Lease.RunID + ":final"
	if prepared.FinalClientMsgID != expectedClientID || len(prepared.FinalClientMsgID) > 64 {
		return fmt.Errorf("final_client_msg_id must equal agent:<run_id>:final and fit 64 bytes")
	}
	if prepared.InputTokens < 0 || prepared.OutputTokens < 0 || prepared.CacheReadTokens < 0 ||
		prepared.CacheWriteTokens < 0 || prepared.TotalTokens < 0 || prepared.CostMicros < 0 || prepared.Cost < 0 {
		return fmt.Errorf("usage values cannot be negative")
	}
	if prepared.UsageState != model.AgentUsageStateExact && prepared.UsageState != model.AgentUsageStateUnknown {
		return fmt.Errorf("prepared usage state must be EXACT or UNKNOWN")
	}
	return nil
}

func validExecutionAdvance(from, to string) bool {
	return (from == model.AgentRunStatusClaimed && to == model.AgentRunStatusStartingRuntime) ||
		(from == model.AgentRunStatusStartingRuntime && to == model.AgentRunStatusRunning)
}

func containsStatus(statuses []string, status string) bool {
	return slices.Contains(statuses, status)
}

func validBoundedString(value string, max int) bool {
	return value != "" && len(value) <= max && strings.TrimSpace(value) == value
}

func sameAgentRunSource(existing, candidate *model.AgentRun) bool {
	return existing.SourceHash == candidate.SourceHash &&
		existing.ConversationID == candidate.ConversationID &&
		existing.SourceSeqID == candidate.SourceSeqID &&
		existing.SourceTimestampMs == candidate.SourceTimestampMs &&
		existing.Prompt == candidate.Prompt &&
		existing.ActorID == candidate.ActorID &&
		existing.ActorUsername == candidate.ActorUsername
}

func (r *agentRunRepo) Close() error {
	r.logger.Info("关闭 AgentRunRepo")
	return nil
}
