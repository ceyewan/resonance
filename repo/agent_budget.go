package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ceyewan/resonance/model"
)

var (
	ErrAgentBudgetPolicyNotFound  = errors.New("agent budget policy not found")
	ErrAgentBudgetPolicyDisabled  = errors.New("agent budget policy is disabled")
	ErrAgentBudgetPolicyConflict  = errors.New("agent budget policy version conflict")
	ErrAgentBudgetExceeded        = errors.New("agent budget exhausted")
	ErrAgentBudgetAttemptNotFound = errors.New("agent budget attempt not found")
	ErrAgentBudgetAttemptConflict = errors.New("agent budget attempt conflicts with persisted ledger")
)

type AgentBudgetReservation struct {
	Lease          AgentRunLease
	Attempt        int
	ProfileID      string
	ProfileVersion int64
}

type AgentBudgetUsage struct {
	State            string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	TotalTokens      int64
	CostMicros       int64
}

type AgentBudgetSettlement struct {
	Lease   AgentRunLease
	Attempt int
	Usage   AgentBudgetUsage
}

// PutAgentBudgetPolicy 使用 expectedVersion 做 CAS。expectedVersion=0 只允许首次创建。
func (r *agentRunRepo) PutAgentBudgetPolicy(
	ctx context.Context,
	policy *model.AgentBudgetPolicy,
	expectedVersion int64,
) (*model.AgentBudgetPolicy, error) {
	if err := validateAgentBudgetPolicy(policy); err != nil {
		return nil, err
	}
	if expectedVersion < 0 {
		return nil, fmt.Errorf("expected budget policy version cannot be negative")
	}
	candidate := *policy
	var persisted model.AgentBudgetPolicy
	err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		if expectedVersion == 0 {
			candidate.Version = 1
			created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate)
			if created.Error != nil {
				return fmt.Errorf("create agent budget policy: %w", created.Error)
			}
			if created.RowsAffected != 1 {
				return ErrAgentBudgetPolicyConflict
			}
		} else {
			updated := tx.Model(&model.AgentBudgetPolicy{}).
				Where("tenant_id = ? AND version = ?", candidate.TenantID, expectedVersion).
				Updates(map[string]any{
					"enabled":                   candidate.Enabled,
					"daily_token_limit":         candidate.DailyTokenLimit,
					"monthly_token_limit":       candidate.MonthlyTokenLimit,
					"daily_cost_limit_micros":   candidate.DailyCostLimitMicros,
					"monthly_cost_limit_micros": candidate.MonthlyCostLimitMicros,
					"max_attempt_tokens":        candidate.MaxAttemptTokens,
					"max_attempt_cost_micros":   candidate.MaxAttemptCostMicros,
					"version":                   gorm.Expr("version + 1"),
					"updated_at":                time.Now().UTC(),
				})
			if updated.Error != nil {
				return fmt.Errorf("update agent budget policy: %w", updated.Error)
			}
			if updated.RowsAffected != 1 {
				return ErrAgentBudgetPolicyConflict
			}
		}
		if err := tx.Where("tenant_id = ?", candidate.TenantID).Take(&persisted).Error; err != nil {
			return fmt.Errorf("load agent budget policy: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &persisted, nil
}

func (r *agentRunRepo) GetAgentBudgetPolicy(ctx context.Context, tenantID string) (*model.AgentBudgetPolicy, error) {
	if !validBoundedString(tenantID, 64) {
		return nil, fmt.Errorf("tenant_id must contain 1 to 64 bytes")
	}
	var policy model.AgentBudgetPolicy
	if err := r.db.DB(ctx).Where("tenant_id = ?", tenantID).Take(&policy).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAgentBudgetPolicyNotFound
		}
		return nil, fmt.Errorf("get agent budget policy: %w", err)
	}
	return &policy, nil
}

// ReserveAgentBudget 在同一 PostgreSQL 事务中校验 Run fence、锁 Policy、
// 锁 UTC 日/月 Bucket，并创建唯一 Attempt ledger。
func (r *agentRunRepo) ReserveAgentBudget(
	ctx context.Context,
	reservation AgentBudgetReservation,
) (*model.AgentBudgetAttempt, error) {
	if err := validateBudgetReservation(reservation); err != nil {
		return nil, err
	}
	var reserved *model.AgentBudgetAttempt
	err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		current, err := lockFencedAgentRun(tx, reservation.Lease, []string{model.AgentRunStatusStartingRuntime})
		if err != nil {
			return err
		}
		if current.Attempt != reservation.Attempt || current.ProfileID != reservation.ProfileID ||
			current.ProfileVersion != reservation.ProfileVersion {
			return ErrAgentBudgetAttemptConflict
		}

		policy, err := lockAgentBudgetPolicy(tx, current.TenantID)
		if err != nil {
			return err
		}
		var existing model.AgentBudgetAttempt
		existingErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND run_id = ? AND attempt = ?", current.TenantID, current.RunID, current.Attempt).
			Take(&existing).Error
		if existingErr == nil {
			if !sameBudgetReservation(&existing, current, reservation.Lease) {
				return ErrAgentBudgetAttemptConflict
			}
			reserved = &existing
			return nil
		}
		if !errors.Is(existingErr, gorm.ErrRecordNotFound) {
			return fmt.Errorf("load idempotent agent budget attempt: %w", existingErr)
		}
		if !policy.Enabled {
			return ErrAgentBudgetPolicyDisabled
		}

		periodNow, err := r.budgetNow(tx)
		if err != nil {
			return fmt.Errorf("load authoritative budget time: %w", err)
		}
		dayStart, monthStart := budgetPeriodStarts(periodNow)
		day, err := lockOrCreateBudgetBucket(tx, current.TenantID, model.AgentBudgetPeriodDay, dayStart, policy.Version)
		if err != nil {
			return err
		}
		month, err := lockOrCreateBudgetBucket(tx, current.TenantID, model.AgentBudgetPeriodMonth, monthStart, policy.Version)
		if err != nil {
			return err
		}
		if !bucketCanReserve(day, policy.DailyTokenLimit, policy.DailyCostLimitMicros, policy.MaxAttemptTokens, policy.MaxAttemptCostMicros) ||
			!bucketCanReserve(month, policy.MonthlyTokenLimit, policy.MonthlyCostLimitMicros, policy.MaxAttemptTokens, policy.MaxAttemptCostMicros) {
			return ErrAgentBudgetExceeded
		}
		for _, bucket := range []*model.AgentBudgetBucket{day, month} {
			if err := addBucketReservation(tx, bucket, policy); err != nil {
				return err
			}
		}

		attempt := &model.AgentBudgetAttempt{
			TenantID: current.TenantID, RunID: current.RunID, Attempt: current.Attempt,
			ProfileID: current.ProfileID, ProfileVersion: current.ProfileVersion, PolicyVersion: policy.Version,
			LeaseOwner: current.LeaseOwner, LeaseToken: current.LeaseToken, RunVersion: current.Version,
			DayPeriodStart: dayStart, MonthPeriodStart: monthStart,
			ReservedTokens: policy.MaxAttemptTokens, ReservedCostMicros: policy.MaxAttemptCostMicros,
			Status: model.AgentBudgetAttemptStatusReserved, Version: 1,
		}
		if err := tx.Create(attempt).Error; err != nil {
			return fmt.Errorf("create agent budget attempt: %w", err)
		}
		reserved = attempt
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reserved, nil
}

// SettleAgentBudget 将 Runtime-neutral usage 状态原子映射到账本：
// EXACT 结算真实用量，NOT_STARTED 释放，UNKNOWN 保留原 reservation。
func (r *agentRunRepo) SettleAgentBudget(
	ctx context.Context,
	settlement AgentBudgetSettlement,
) (*model.AgentBudgetAttempt, error) {
	if err := validateBudgetSettlement(settlement); err != nil {
		return nil, err
	}
	var settled *model.AgentBudgetAttempt
	err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
		attempt, err := loadAgentBudgetAttempt(tx, settlement.Lease.TenantID, settlement.Lease.RunID, settlement.Attempt)
		if err != nil {
			return err
		}
		if attempt.Status != model.AgentBudgetAttemptStatusReserved {
			if sameBudgetSettlement(attempt, settlement) {
				settled = attempt
				return nil
			}
			return ErrAgentBudgetAttemptConflict
		}

		current, err := lockFencedAgentRun(tx, settlement.Lease, []string{
			model.AgentRunStatusStartingRuntime,
			model.AgentRunStatusRunning,
		})
		if err != nil {
			return err
		}
		attempt, err = lockAgentBudgetAttempt(tx, settlement.Lease.TenantID, settlement.Lease.RunID, settlement.Attempt)
		if err != nil {
			return err
		}
		if attempt.Status != model.AgentBudgetAttemptStatusReserved {
			if sameBudgetSettlement(attempt, settlement) {
				settled = attempt
				return nil
			}
			return ErrAgentBudgetAttemptConflict
		}
		if !sameBudgetReservation(attempt, current, settlement.Lease) || current.Attempt != settlement.Attempt {
			return ErrAgentBudgetAttemptConflict
		}

		day, err := lockBudgetBucket(tx, attempt.TenantID, model.AgentBudgetPeriodDay, attempt.DayPeriodStart)
		if err != nil {
			return err
		}
		month, err := lockBudgetBucket(tx, attempt.TenantID, model.AgentBudgetPeriodMonth, attempt.MonthPeriodStart)
		if err != nil {
			return err
		}
		now := settlement.Lease.Now.UTC()
		status := ""
		switch settlement.Usage.State {
		case model.AgentUsageStateExact:
			for _, bucket := range []*model.AgentBudgetBucket{day, month} {
				if err := settleBudgetBucket(tx, bucket, attempt, settlement.Usage); err != nil {
					return err
				}
			}
			status = model.AgentBudgetAttemptStatusSettled
			if settlement.Usage.TotalTokens > attempt.ReservedTokens || settlement.Usage.CostMicros > attempt.ReservedCostMicros {
				status = model.AgentBudgetAttemptStatusOverdrawn
			}
		case model.AgentUsageStateNotStarted:
			for _, bucket := range []*model.AgentBudgetBucket{day, month} {
				if err := releaseBudgetBucket(tx, bucket, attempt); err != nil {
					return err
				}
			}
			status = model.AgentBudgetAttemptStatusReleased
		case model.AgentUsageStateUnknown:
			for _, bucket := range []*model.AgentBudgetBucket{day, month} {
				if err := markBudgetBucketUnknown(tx, bucket, attempt); err != nil {
					return err
				}
			}
			status = model.AgentBudgetAttemptStatusUnknown
		default:
			return fmt.Errorf("unsupported usage state")
		}

		updates := map[string]any{
			"status":                    status,
			"usage_state":               settlement.Usage.State,
			"actual_input_tokens":       settlement.Usage.InputTokens,
			"actual_output_tokens":      settlement.Usage.OutputTokens,
			"actual_cache_read_tokens":  settlement.Usage.CacheReadTokens,
			"actual_cache_write_tokens": settlement.Usage.CacheWriteTokens,
			"actual_total_tokens":       settlement.Usage.TotalTokens,
			"actual_cost_micros":        settlement.Usage.CostMicros,
			"settled_at":                now,
			"version":                   gorm.Expr("version + 1"),
			"updated_at":                now,
		}
		updated := tx.Model(&model.AgentBudgetAttempt{}).
			Where("tenant_id = ? AND run_id = ? AND attempt = ? AND version = ? AND status = ?",
				attempt.TenantID, attempt.RunID, attempt.Attempt, attempt.Version, model.AgentBudgetAttemptStatusReserved).
			Updates(updates)
		if updated.Error != nil {
			return fmt.Errorf("settle agent budget attempt: %w", updated.Error)
		}
		if updated.RowsAffected != 1 {
			return ErrAgentBudgetAttemptConflict
		}
		settled, err = loadAgentBudgetAttempt(tx, attempt.TenantID, attempt.RunID, attempt.Attempt)
		return err
	})
	if err != nil {
		return nil, err
	}
	return settled, nil
}

// RecoverExpiredAgentBudgetAttempts 将无法证明仍由有效 Run lease 持有的 RESERVED Attempt
// 转为 UNKNOWN；reservation 仍留在原日/月 Bucket 中。
func (r *agentRunRepo) RecoverExpiredAgentBudgetAttempts(ctx context.Context, tenantID string, now time.Time) (int64, error) {
	if !validBoundedString(tenantID, 64) || now.IsZero() {
		return 0, fmt.Errorf("tenant_id and now are required")
	}
	now = now.UTC()
	var keys []struct {
		RunID   string
		Attempt int
	}
	if err := r.db.DB(ctx).Model(&model.AgentBudgetAttempt{}).
		Select("run_id", "attempt").
		Where("tenant_id = ? AND status = ?", tenantID, model.AgentBudgetAttemptStatusReserved).
		Order("day_period_start ASC, month_period_start ASC, run_id ASC, attempt ASC").
		Scan(&keys).Error; err != nil {
		return 0, fmt.Errorf("list recoverable agent budget attempts: %w", err)
	}
	var recovered int64
	for _, key := range keys {
		err := r.db.Transaction(ctx, func(_ context.Context, tx *gorm.DB) error {
			attempt, err := loadAgentBudgetAttempt(tx, tenantID, key.RunID, key.Attempt)
			if errors.Is(err, ErrAgentBudgetAttemptNotFound) || (err == nil && attempt.Status != model.AgentBudgetAttemptStatusReserved) {
				return nil
			}
			if err != nil {
				return err
			}
			var run model.AgentRun
			runErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("tenant_id = ? AND run_id = ?", tenantID, key.RunID).Take(&run).Error
			if runErr != nil && !errors.Is(runErr, gorm.ErrRecordNotFound) {
				return fmt.Errorf("lock agent run for budget recovery: %w", runErr)
			}
			attempt, err = lockAgentBudgetAttempt(tx, tenantID, key.RunID, key.Attempt)
			if errors.Is(err, ErrAgentBudgetAttemptNotFound) || (err == nil && attempt.Status != model.AgentBudgetAttemptStatusReserved) {
				return nil
			}
			if err != nil {
				return err
			}
			if runErr == nil && budgetAttemptLeaseIsLive(attempt, &run, now) {
				return nil
			}
			day, err := lockBudgetBucket(tx, attempt.TenantID, model.AgentBudgetPeriodDay, attempt.DayPeriodStart)
			if err != nil {
				return err
			}
			month, err := lockBudgetBucket(tx, attempt.TenantID, model.AgentBudgetPeriodMonth, attempt.MonthPeriodStart)
			if err != nil {
				return err
			}
			for _, bucket := range []*model.AgentBudgetBucket{day, month} {
				if err := markBudgetBucketUnknown(tx, bucket, attempt); err != nil {
					return err
				}
			}
			result := tx.Model(&model.AgentBudgetAttempt{}).
				Where("tenant_id = ? AND run_id = ? AND attempt = ? AND version = ? AND status = ?",
					attempt.TenantID, attempt.RunID, attempt.Attempt, attempt.Version, model.AgentBudgetAttemptStatusReserved).
				Updates(map[string]any{
					"status":      model.AgentBudgetAttemptStatusUnknown,
					"usage_state": model.AgentUsageStateUnknown,
					"settled_at":  now,
					"version":     gorm.Expr("version + 1"),
					"updated_at":  now,
				})
			if result.Error != nil {
				return fmt.Errorf("recover unknown agent budget attempt: %w", result.Error)
			}
			if result.RowsAffected == 1 {
				recovered++
			}
			return nil
		})
		if err != nil {
			return recovered, err
		}
	}
	return recovered, nil
}

func (r *agentRunRepo) GetAgentBudgetAttempt(ctx context.Context, tenantID, runID string, attempt int) (*model.AgentBudgetAttempt, error) {
	if !validBoundedString(tenantID, 64) || !validBoundedString(runID, 64) || attempt < 1 {
		return nil, fmt.Errorf("tenant_id, run_id and attempt are required")
	}
	return loadAgentBudgetAttempt(r.db.DB(ctx), tenantID, runID, attempt)
}

func (r *agentRunRepo) GetAgentBudgetBucket(
	ctx context.Context,
	tenantID, periodKind string,
	periodStart time.Time,
) (*model.AgentBudgetBucket, error) {
	if !validBoundedString(tenantID, 64) || !validBudgetPeriodKind(periodKind) || periodStart.IsZero() {
		return nil, fmt.Errorf("valid tenant, period kind and start are required")
	}
	var bucket model.AgentBudgetBucket
	err := r.db.DB(ctx).Where("tenant_id = ? AND period_kind = ? AND period_start = ?",
		tenantID, periodKind, periodStart.UTC()).Take(&bucket).Error
	if err != nil {
		return nil, err
	}
	return &bucket, nil
}

func lockAgentBudgetPolicy(tx *gorm.DB, tenantID string) (*model.AgentBudgetPolicy, error) {
	var policy model.AgentBudgetPolicy
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ?", tenantID).Take(&policy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAgentBudgetPolicyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock agent budget policy: %w", err)
	}
	return &policy, nil
}

func lockOrCreateBudgetBucket(
	tx *gorm.DB,
	tenantID, periodKind string,
	periodStart time.Time,
	policyVersion int64,
) (*model.AgentBudgetBucket, error) {
	candidate := &model.AgentBudgetBucket{
		TenantID: tenantID, PeriodKind: periodKind, PeriodStart: periodStart.UTC(), PolicyVersion: policyVersion, Version: 1,
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(candidate).Error; err != nil {
		return nil, fmt.Errorf("create agent budget bucket: %w", err)
	}
	return lockBudgetBucket(tx, tenantID, periodKind, periodStart)
}

func lockBudgetBucket(tx *gorm.DB, tenantID, periodKind string, periodStart time.Time) (*model.AgentBudgetBucket, error) {
	var bucket model.AgentBudgetBucket
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND period_kind = ? AND period_start = ?", tenantID, periodKind, periodStart.UTC()).
		Take(&bucket).Error
	if err != nil {
		return nil, fmt.Errorf("lock agent budget bucket: %w", err)
	}
	return &bucket, nil
}

func lockAgentBudgetAttempt(tx *gorm.DB, tenantID, runID string, attempt int) (*model.AgentBudgetAttempt, error) {
	var value model.AgentBudgetAttempt
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND run_id = ? AND attempt = ?", tenantID, runID, attempt).Take(&value).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAgentBudgetAttemptNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock agent budget attempt: %w", err)
	}
	return &value, nil
}

func loadAgentBudgetAttempt(tx *gorm.DB, tenantID, runID string, attempt int) (*model.AgentBudgetAttempt, error) {
	var value model.AgentBudgetAttempt
	err := tx.Where("tenant_id = ? AND run_id = ? AND attempt = ?", tenantID, runID, attempt).Take(&value).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAgentBudgetAttemptNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load agent budget attempt: %w", err)
	}
	return &value, nil
}

func addBucketReservation(tx *gorm.DB, bucket *model.AgentBudgetBucket, policy *model.AgentBudgetPolicy) error {
	result := tx.Model(&model.AgentBudgetBucket{}).
		Where("tenant_id = ? AND period_kind = ? AND period_start = ? AND version = ?",
			bucket.TenantID, bucket.PeriodKind, bucket.PeriodStart, bucket.Version).
		Updates(map[string]any{
			"reserved_tokens":      gorm.Expr("reserved_tokens + ?", policy.MaxAttemptTokens),
			"reserved_cost_micros": gorm.Expr("reserved_cost_micros + ?", policy.MaxAttemptCostMicros),
			"policy_version":       policy.Version,
			"version":              gorm.Expr("version + 1"),
			"updated_at":           time.Now().UTC(),
		})
	if result.Error != nil {
		return fmt.Errorf("reserve agent budget bucket: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrAgentBudgetAttemptConflict
	}
	return nil
}

func settleBudgetBucket(tx *gorm.DB, bucket *model.AgentBudgetBucket, attempt *model.AgentBudgetAttempt, usage AgentBudgetUsage) error {
	if bucket.ReservedTokens < attempt.ReservedTokens || bucket.ReservedCostMicros < attempt.ReservedCostMicros ||
		wouldOverflow(bucket.SettledTokens, usage.TotalTokens) || wouldOverflow(bucket.SettledCostMicros, usage.CostMicros) {
		return ErrAgentBudgetAttemptConflict
	}
	return updateBudgetBucket(tx, bucket, map[string]any{
		"reserved_tokens":      gorm.Expr("reserved_tokens - ?", attempt.ReservedTokens),
		"reserved_cost_micros": gorm.Expr("reserved_cost_micros - ?", attempt.ReservedCostMicros),
		"settled_tokens":       gorm.Expr("settled_tokens + ?", usage.TotalTokens),
		"settled_cost_micros":  gorm.Expr("settled_cost_micros + ?", usage.CostMicros),
	})
}

func releaseBudgetBucket(tx *gorm.DB, bucket *model.AgentBudgetBucket, attempt *model.AgentBudgetAttempt) error {
	if bucket.ReservedTokens < attempt.ReservedTokens || bucket.ReservedCostMicros < attempt.ReservedCostMicros {
		return ErrAgentBudgetAttemptConflict
	}
	return updateBudgetBucket(tx, bucket, map[string]any{
		"reserved_tokens":      gorm.Expr("reserved_tokens - ?", attempt.ReservedTokens),
		"reserved_cost_micros": gorm.Expr("reserved_cost_micros - ?", attempt.ReservedCostMicros),
	})
}

func markBudgetBucketUnknown(tx *gorm.DB, bucket *model.AgentBudgetBucket, attempt *model.AgentBudgetAttempt) error {
	if bucket.ReservedTokens < attempt.ReservedTokens || bucket.ReservedCostMicros < attempt.ReservedCostMicros ||
		wouldOverflow(bucket.UnknownReservedTokens, attempt.ReservedTokens) ||
		wouldOverflow(bucket.UnknownReservedCostMicros, attempt.ReservedCostMicros) {
		return ErrAgentBudgetAttemptConflict
	}
	return updateBudgetBucket(tx, bucket, map[string]any{
		"unknown_reserved_tokens":      gorm.Expr("unknown_reserved_tokens + ?", attempt.ReservedTokens),
		"unknown_reserved_cost_micros": gorm.Expr("unknown_reserved_cost_micros + ?", attempt.ReservedCostMicros),
	})
}

func updateBudgetBucket(tx *gorm.DB, bucket *model.AgentBudgetBucket, updates map[string]any) error {
	updates["version"] = gorm.Expr("version + 1")
	updates["updated_at"] = time.Now().UTC()
	result := tx.Model(&model.AgentBudgetBucket{}).
		Where("tenant_id = ? AND period_kind = ? AND period_start = ? AND version = ?",
			bucket.TenantID, bucket.PeriodKind, bucket.PeriodStart, bucket.Version).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update agent budget bucket: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrAgentBudgetAttemptConflict
	}
	return nil
}

func sameBudgetReservation(attempt *model.AgentBudgetAttempt, run *model.AgentRun, lease AgentRunLease) bool {
	return attempt.TenantID == run.TenantID && attempt.RunID == run.RunID && attempt.Attempt == run.Attempt &&
		attempt.ProfileID == run.ProfileID && attempt.ProfileVersion == run.ProfileVersion &&
		attempt.LeaseOwner == lease.WorkerID && attempt.LeaseToken == lease.LeaseToken
}

func sameBudgetSettlement(attempt *model.AgentBudgetAttempt, settlement AgentBudgetSettlement) bool {
	expectedStatus := ""
	switch settlement.Usage.State {
	case model.AgentUsageStateExact:
		expectedStatus = model.AgentBudgetAttemptStatusSettled
		if settlement.Usage.TotalTokens > attempt.ReservedTokens || settlement.Usage.CostMicros > attempt.ReservedCostMicros {
			expectedStatus = model.AgentBudgetAttemptStatusOverdrawn
		}
	case model.AgentUsageStateNotStarted:
		expectedStatus = model.AgentBudgetAttemptStatusReleased
	case model.AgentUsageStateUnknown:
		expectedStatus = model.AgentBudgetAttemptStatusUnknown
	}
	return attempt.Status == expectedStatus && attempt.LeaseOwner == settlement.Lease.WorkerID &&
		attempt.LeaseToken == settlement.Lease.LeaseToken && attempt.UsageState == settlement.Usage.State &&
		attempt.ActualInputTokens == settlement.Usage.InputTokens && attempt.ActualOutputTokens == settlement.Usage.OutputTokens &&
		attempt.ActualCacheReadTokens == settlement.Usage.CacheReadTokens &&
		attempt.ActualCacheWriteTokens == settlement.Usage.CacheWriteTokens &&
		attempt.ActualTotalTokens == settlement.Usage.TotalTokens && attempt.ActualCostMicros == settlement.Usage.CostMicros
}

func budgetAttemptLeaseIsLive(attempt *model.AgentBudgetAttempt, run *model.AgentRun, now time.Time) bool {
	return run.Attempt == attempt.Attempt && run.LeaseOwner == attempt.LeaseOwner && run.LeaseToken == attempt.LeaseToken &&
		run.LeaseExpiresAt != nil && run.LeaseExpiresAt.After(now) && containsStatus(agentRunExecutionStatuses, run.Status)
}

func bucketCanReserve(bucket *model.AgentBudgetBucket, tokenLimit, costLimit, tokens, cost int64) bool {
	if bucket.ReservedTokens > tokenLimit || bucket.SettledTokens > tokenLimit-bucket.ReservedTokens ||
		tokens > tokenLimit-bucket.ReservedTokens-bucket.SettledTokens {
		return false
	}
	if bucket.ReservedCostMicros > costLimit || bucket.SettledCostMicros > costLimit-bucket.ReservedCostMicros ||
		cost > costLimit-bucket.ReservedCostMicros-bucket.SettledCostMicros {
		return false
	}
	return true
}

func budgetPeriodStarts(now time.Time) (time.Time, time.Time) {
	value := now.UTC()
	day := time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	month := time.Date(value.Year(), value.Month(), 1, 0, 0, 0, 0, time.UTC)
	return day, month
}

func databaseBudgetNow(tx *gorm.DB) (time.Time, error) {
	var now time.Time
	if err := tx.Raw("SELECT CURRENT_TIMESTAMP").Scan(&now).Error; err != nil {
		return time.Time{}, err
	}
	if now.IsZero() {
		return time.Time{}, fmt.Errorf("database returned zero current timestamp")
	}
	return now.UTC(), nil
}

func validateAgentBudgetPolicy(policy *model.AgentBudgetPolicy) error {
	const maxBridgeExactInteger int64 = 9_007_199_254_740_991
	if policy == nil || !validBoundedString(policy.TenantID, 64) {
		return fmt.Errorf("agent budget policy tenant_id is required")
	}
	if policy.DailyTokenLimit < 1 || policy.MonthlyTokenLimit < policy.DailyTokenLimit ||
		policy.DailyCostLimitMicros < 1 || policy.MonthlyCostLimitMicros < policy.DailyCostLimitMicros ||
		policy.MaxAttemptTokens < 1 || policy.MaxAttemptTokens > policy.DailyTokenLimit ||
		policy.MaxAttemptCostMicros < 1 || policy.MaxAttemptCostMicros > policy.DailyCostLimitMicros ||
		policy.MaxAttemptTokens > maxBridgeExactInteger || policy.MaxAttemptCostMicros > maxBridgeExactInteger {
		return fmt.Errorf("agent budget policy limits and max attempt bounds are invalid")
	}
	return nil
}

func validateBudgetReservation(value AgentBudgetReservation) error {
	if err := validateLease(value.Lease, false); err != nil {
		return err
	}
	if value.Attempt < 1 || !validBoundedString(value.ProfileID, 64) || value.ProfileVersion < 1 {
		return fmt.Errorf("budget reservation attempt and profile snapshot are required")
	}
	return nil
}

func validateBudgetSettlement(value AgentBudgetSettlement) error {
	if err := validateLease(value.Lease, false); err != nil {
		return err
	}
	if value.Attempt < 1 {
		return fmt.Errorf("budget settlement attempt is required")
	}
	usage := value.Usage
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CacheReadTokens < 0 || usage.CacheWriteTokens < 0 ||
		usage.TotalTokens < 0 || usage.CostMicros < 0 ||
		wouldOverflow(usage.InputTokens, usage.OutputTokens) ||
		wouldOverflow(usage.InputTokens+usage.OutputTokens, usage.CacheReadTokens) ||
		wouldOverflow(usage.InputTokens+usage.OutputTokens+usage.CacheReadTokens, usage.CacheWriteTokens) ||
		usage.TotalTokens != usage.InputTokens+usage.OutputTokens+usage.CacheReadTokens+usage.CacheWriteTokens {
		return fmt.Errorf("budget settlement usage is invalid")
	}
	switch usage.State {
	case model.AgentUsageStateExact:
	case model.AgentUsageStateNotStarted, model.AgentUsageStateUnknown:
		if usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.CacheReadTokens != 0 || usage.CacheWriteTokens != 0 ||
			usage.TotalTokens != 0 || usage.CostMicros != 0 {
			return fmt.Errorf("non-exact usage cannot contain counters")
		}
	default:
		return fmt.Errorf("budget settlement usage state is invalid")
	}
	return nil
}

func validBudgetPeriodKind(kind string) bool {
	return kind == model.AgentBudgetPeriodDay || kind == model.AgentBudgetPeriodMonth
}

func wouldOverflow(left, right int64) bool {
	return left < 0 || right < 0 || left > int64(^uint64(0)>>1)-right
}
