package repo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	genesisdb "github.com/ceyewan/genesis/db"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/ceyewan/resonance/model"
)

func TestAgentBudgetRepo_SettlementStatesAndIdempotency(t *testing.T) {
	database, cleanup := setupBudgetTestContext(t)
	defer cleanup()

	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	base := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	budgetClock := setBudgetTestClock(t, runRepo, base)

	tests := []struct {
		name                string
		usage               AgentBudgetUsage
		wantStatus          string
		wantReserved        int64
		wantSettled         int64
		wantUnknownReserved int64
	}{
		{
			name: "exact",
			usage: AgentBudgetUsage{
				State: model.AgentUsageStateExact, InputTokens: 10, OutputTokens: 15,
				CacheReadTokens: 3, CacheWriteTokens: 2, TotalTokens: 30, CostMicros: 300,
			},
			wantStatus: model.AgentBudgetAttemptStatusSettled, wantSettled: 30,
		},
		{
			name:       "not-started releases",
			usage:      AgentBudgetUsage{State: model.AgentUsageStateNotStarted},
			wantStatus: model.AgentBudgetAttemptStatusReleased,
		},
		{
			name:       "unknown remains held",
			usage:      AgentBudgetUsage{State: model.AgentUsageStateUnknown},
			wantStatus: model.AgentBudgetAttemptStatusUnknown, wantReserved: 100, wantUnknownReserved: 100,
		},
		{
			name:       "actual above reservation is observable overdraw",
			usage:      AgentBudgetUsage{State: model.AgentUsageStateExact, InputTokens: 100, OutputTokens: 50, TotalTokens: 150, CostMicros: 1_500},
			wantStatus: model.AgentBudgetAttemptStatusOverdrawn, wantSettled: 150,
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tenantID := fmt.Sprintf("tenant-budget-state-%d", index)
			budgetClock.Set(base.Add(time.Duration(index) * time.Hour))
			_, err := runRepo.PutAgentBudgetPolicy(ctx, testBudgetPolicy(tenantID, 1_000, 10_000, 10_000, 100_000), 0)
			require.NoError(t, err)
			starting, reservation := startBudgetTestRun(t, ctx, runRepo, tenantID,
				fmt.Sprintf("run-budget-state-%d", index), fmt.Sprintf("conversation-budget-state-%d", index),
				int64(10_000+index), base.Add(time.Duration(index)*time.Hour), time.Hour)

			reserved, err := runRepo.ReserveAgentBudget(ctx, reservation)
			require.NoError(t, err)
			require.Equal(t, model.AgentBudgetAttemptStatusReserved, reserved.Status)
			require.Equal(t, int64(100), reserved.ReservedTokens)
			require.Equal(t, int64(1_000), reserved.ReservedCostMicros)

			// Reserve 响应丢失后，使用同一 fence 重放不会重复占用 Bucket。
			replayedReserve, err := runRepo.ReserveAgentBudget(ctx, reservation)
			require.NoError(t, err)
			require.Equal(t, reserved.Version, replayedReserve.Version)

			settlement := AgentBudgetSettlement{
				Lease: leaseFor(starting, reservation.Lease.Now.Add(time.Minute)), Attempt: starting.Attempt, Usage: test.usage,
			}
			settled, err := runRepo.SettleAgentBudget(ctx, settlement)
			require.NoError(t, err)
			require.Equal(t, test.wantStatus, settled.Status)
			require.Equal(t, test.usage.State, settled.UsageState)
			require.Equal(t, test.usage.CostMicros, settled.ActualCostMicros)

			// Settlement 响应丢失后，完全相同的终态重放不会再次修改 Bucket。
			replayedSettlement, err := runRepo.SettleAgentBudget(ctx, settlement)
			require.NoError(t, err)
			require.Equal(t, settled.Version, replayedSettlement.Version)

			conflict := settlement
			conflict.Usage = AgentBudgetUsage{State: model.AgentUsageStateUnknown}
			if test.usage.State == model.AgentUsageStateUnknown {
				conflict.Usage = AgentBudgetUsage{State: model.AgentUsageStateNotStarted}
			}
			_, err = runRepo.SettleAgentBudget(ctx, conflict)
			require.ErrorIs(t, err, ErrAgentBudgetAttemptConflict)

			dayStart, monthStart := budgetPeriodStarts(reservation.Lease.Now)
			for _, period := range []struct {
				kind  string
				start time.Time
			}{{model.AgentBudgetPeriodDay, dayStart}, {model.AgentBudgetPeriodMonth, monthStart}} {
				bucket, err := runRepo.GetAgentBudgetBucket(ctx, tenantID, period.kind, period.start)
				require.NoError(t, err)
				require.Equal(t, test.wantReserved, bucket.ReservedTokens)
				require.Equal(t, test.wantSettled, bucket.SettledTokens)
				require.Equal(t, test.wantUnknownReserved, bucket.UnknownReservedTokens)
			}
		})
	}
}

func TestAgentBudgetRepo_FailsClosedAndFencesSnapshot(t *testing.T) {
	database, cleanup := setupBudgetTestContext(t)
	defer cleanup()

	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	base := time.Date(2026, 8, 9, 9, 0, 0, 0, time.UTC)
	setBudgetTestClock(t, runRepo, base)

	missingRun, missing := startBudgetTestRun(t, ctx, runRepo, "tenant-budget-missing",
		"run-budget-missing", "conversation-budget-missing", 20_001, base, time.Hour)
	_, err = runRepo.ReserveAgentBudget(ctx, missing)
	require.ErrorIs(t, err, ErrAgentBudgetPolicyNotFound)
	_, err = runRepo.GetAgentBudgetAttempt(ctx, missingRun.TenantID, missingRun.RunID, missingRun.Attempt)
	require.ErrorIs(t, err, ErrAgentBudgetAttemptNotFound)

	disabledTenant := "tenant-budget-disabled"
	disabledPolicy := testBudgetPolicy(disabledTenant, 1_000, 10_000, 10_000, 100_000)
	disabledPolicy.Enabled = false
	_, err = runRepo.PutAgentBudgetPolicy(ctx, disabledPolicy, 0)
	require.NoError(t, err)
	_, disabled := startBudgetTestRun(t, ctx, runRepo, disabledTenant,
		"run-budget-disabled", "conversation-budget-disabled", 20_002, base.Add(time.Hour), time.Hour)
	_, err = runRepo.ReserveAgentBudget(ctx, disabled)
	require.ErrorIs(t, err, ErrAgentBudgetPolicyDisabled)

	fencedTenant := "tenant-budget-fenced"
	_, err = runRepo.PutAgentBudgetPolicy(ctx, testBudgetPolicy(fencedTenant, 1_000, 10_000, 10_000, 100_000), 0)
	require.NoError(t, err)
	_, fenced := startBudgetTestRun(t, ctx, runRepo, fencedTenant,
		"run-budget-fenced", "conversation-budget-fenced", 20_003, base.Add(2*time.Hour), time.Hour)

	wrongProfile := fenced
	wrongProfile.ProfileVersion++
	_, err = runRepo.ReserveAgentBudget(ctx, wrongProfile)
	require.ErrorIs(t, err, ErrAgentBudgetAttemptConflict)

	stale := fenced
	stale.Lease.ExpectedVersion--
	_, err = runRepo.ReserveAgentBudget(ctx, stale)
	require.ErrorIs(t, err, ErrAgentRunFenceLost)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = runRepo.ReserveAgentBudget(cancelled, fenced)
	require.Error(t, err, "database/context failure must fail closed")
}

func TestAgentBudgetPolicy_RejectsAttemptLimitBeyondBridgeExactInteger(t *testing.T) {
	policy := testBudgetPolicy("tenant-unsafe-budget", 10, 10, 10, 10)
	policy.DailyTokenLimit = 9_007_199_254_740_992
	policy.MonthlyTokenLimit = policy.DailyTokenLimit
	policy.MaxAttemptTokens = policy.DailyTokenLimit
	require.Error(t, validateAgentBudgetPolicy(policy))
}

func TestAgentBudgetRepo_UsesDatabaseTimeForPeriodAttribution(t *testing.T) {
	database, cleanup := setupBudgetTestContext(t)
	defer cleanup()

	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	tenantID := "tenant-budget-database-time"
	_, err = runRepo.PutAgentBudgetPolicy(ctx, testBudgetPolicy(tenantID, 1_000, 10_000, 10_000, 100_000), 0)
	require.NoError(t, err)

	// Worker 提供的 fence 时间故意与数据库时钟相差多年，不能用它选择预算周期。
	workerNow := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	_, reservation := startBudgetTestRun(t, ctx, runRepo, tenantID,
		"run-budget-database-time", "conversation-budget-database-time", 25_001, workerNow, time.Hour)
	databaseBefore, err := databaseBudgetNow(database.DB(ctx))
	require.NoError(t, err)
	beforeDay, beforeMonth := budgetPeriodStarts(databaseBefore)
	attempt, err := runRepo.ReserveAgentBudget(ctx, reservation)
	require.NoError(t, err)
	databaseAfter, err := databaseBudgetNow(database.DB(ctx))
	require.NoError(t, err)
	afterDay, afterMonth := budgetPeriodStarts(databaseAfter)
	require.Contains(t, []time.Time{beforeDay, afterDay}, attempt.DayPeriodStart)
	require.Contains(t, []time.Time{beforeMonth, afterMonth}, attempt.MonthPeriodStart)
	workerDay, _ := budgetPeriodStarts(workerNow)
	require.NotEqual(t, workerDay, attempt.DayPeriodStart)
}

func TestAgentBudgetRepo_ConcurrentReservationsPreventOverspendAndIsolateTenants(t *testing.T) {
	database, cleanup := setupBudgetTestContext(t)
	defer cleanup()

	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	base := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	setBudgetTestClock(t, runRepo, base)
	tenantID := "tenant-budget-concurrent"
	_, err = runRepo.PutAgentBudgetPolicy(ctx, testBudgetPolicy(tenantID, 200, 200, 2_000, 2_000), 0)
	require.NoError(t, err)

	reservations := make([]AgentBudgetReservation, 0, 8)
	for index := range 8 {
		_, reservation := startBudgetTestRun(t, ctx, runRepo, tenantID,
			fmt.Sprintf("run-budget-concurrent-%d", index), fmt.Sprintf("conversation-budget-concurrent-%d", index),
			int64(30_000+index), base.Add(time.Duration(index)*time.Millisecond), time.Hour)
		reservations = append(reservations, reservation)
	}

	start := make(chan struct{})
	errorsByWorker := make(chan error, len(reservations))
	var workers sync.WaitGroup
	for _, reservation := range reservations {
		workers.Go(func() {
			<-start
			_, reserveErr := runRepo.ReserveAgentBudget(ctx, reservation)
			errorsByWorker <- reserveErr
		})
	}
	close(start)
	workers.Wait()
	close(errorsByWorker)

	var successes, exhausted int
	for reserveErr := range errorsByWorker {
		switch {
		case reserveErr == nil:
			successes++
		case errors.Is(reserveErr, ErrAgentBudgetExceeded):
			exhausted++
		default:
			require.NoError(t, reserveErr)
		}
	}
	require.Equal(t, 2, successes)
	require.Equal(t, 6, exhausted)
	dayStart, monthStart := budgetPeriodStarts(base)
	for _, period := range []struct {
		kind  string
		start time.Time
	}{{model.AgentBudgetPeriodDay, dayStart}, {model.AgentBudgetPeriodMonth, monthStart}} {
		bucket, err := runRepo.GetAgentBudgetBucket(ctx, tenantID, period.kind, period.start)
		require.NoError(t, err)
		require.Equal(t, int64(200), bucket.ReservedTokens)
		require.Equal(t, int64(2_000), bucket.ReservedCostMicros)
	}

	// 另一租户有独立的 Policy 与 Bucket，不能被 tenantID 的耗尽影响。
	otherTenant := "tenant-budget-concurrent-other"
	_, err = runRepo.PutAgentBudgetPolicy(ctx, testBudgetPolicy(otherTenant, 100, 100, 1_000, 1_000), 0)
	require.NoError(t, err)
	_, otherReservation := startBudgetTestRun(t, ctx, runRepo, otherTenant,
		"run-budget-other", "conversation-budget-other", 31_000, base, time.Hour)
	_, err = runRepo.ReserveAgentBudget(ctx, otherReservation)
	require.NoError(t, err)

	// 同一 Attempt 的并发响应重放只能产生一条 ledger 和一次 Bucket 增量。
	idempotentTenant := "tenant-budget-idempotent"
	_, err = runRepo.PutAgentBudgetPolicy(ctx, testBudgetPolicy(idempotentTenant, 100, 100, 1_000, 1_000), 0)
	require.NoError(t, err)
	_, idempotentReservation := startBudgetTestRun(t, ctx, runRepo, idempotentTenant,
		"run-budget-idempotent", "conversation-budget-idempotent", 32_000, base, time.Hour)
	idempotentErrors := make(chan error, 8)
	workers = sync.WaitGroup{}
	for range 8 {
		workers.Go(func() {
			_, reserveErr := runRepo.ReserveAgentBudget(ctx, idempotentReservation)
			idempotentErrors <- reserveErr
		})
	}
	workers.Wait()
	close(idempotentErrors)
	for reserveErr := range idempotentErrors {
		require.NoError(t, reserveErr)
	}
	idempotentDay, _ := budgetPeriodStarts(base)
	bucket, err := runRepo.GetAgentBudgetBucket(ctx, idempotentTenant, model.AgentBudgetPeriodDay, idempotentDay)
	require.NoError(t, err)
	require.Equal(t, int64(100), bucket.ReservedTokens)
}

func TestAgentBudgetRepo_PeriodRolloverUsesOriginalBucketsAndBothGates(t *testing.T) {
	database, cleanup := setupBudgetTestContext(t)
	defer cleanup()

	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	budgetClock := setBudgetTestClock(t, runRepo, time.Date(2026, 8, 10, 23, 59, 0, 0, time.UTC))
	tenantID := "tenant-budget-period"
	// Token 限额很宽，成本日限 1_000、月限 2_000，证明日/月两个成本门都生效。
	_, err = runRepo.PutAgentBudgetPolicy(ctx, testBudgetPolicy(tenantID, 1_000, 10_000, 1_000, 2_000), 0)
	require.NoError(t, err)

	dayOne := time.Date(2026, 8, 10, 23, 59, 0, 0, time.UTC)
	budgetClock.Set(dayOne)
	startingOne, reservationOne := startBudgetTestRun(t, ctx, runRepo, tenantID,
		"run-budget-period-1", "conversation-budget-period-1", 40_001, dayOne, 3*time.Hour)
	_, err = runRepo.ReserveAgentBudget(ctx, reservationOne)
	require.NoError(t, err)

	dayTwo := dayOne.Add(2 * time.Minute)
	budgetClock.Set(dayTwo)
	settledOne, err := runRepo.SettleAgentBudget(ctx, AgentBudgetSettlement{
		Lease: leaseFor(startingOne, dayTwo), Attempt: startingOne.Attempt,
		Usage: AgentBudgetUsage{State: model.AgentUsageStateExact, InputTokens: 20, OutputTokens: 30, TotalTokens: 50, CostMicros: 600},
	})
	require.NoError(t, err)
	require.Equal(t, model.AgentBudgetAttemptStatusSettled, settledOne.Status)

	dayOneStart, monthStart := budgetPeriodStarts(reservationOne.Lease.Now)
	dayOneBucket, err := runRepo.GetAgentBudgetBucket(ctx, tenantID, model.AgentBudgetPeriodDay, dayOneStart)
	require.NoError(t, err)
	require.Zero(t, dayOneBucket.ReservedCostMicros)
	require.Equal(t, int64(600), dayOneBucket.SettledCostMicros, "late settlement must update the original day bucket")

	_, reservationTwo := startBudgetTestRun(t, ctx, runRepo, tenantID,
		"run-budget-period-2", "conversation-budget-period-2", 40_002, dayTwo, 3*time.Hour)
	_, err = runRepo.ReserveAgentBudget(ctx, reservationTwo)
	require.NoError(t, err)

	// 同一天第二个 max=1_000 reservation 被日限拒绝，即使月限还有余额。
	_, sameDay := startBudgetTestRun(t, ctx, runRepo, tenantID,
		"run-budget-period-same-day", "conversation-budget-period-same-day", 40_003, dayTwo.Add(time.Minute), 3*time.Hour)
	_, err = runRepo.ReserveAgentBudget(ctx, sameDay)
	require.ErrorIs(t, err, ErrAgentBudgetExceeded)

	// 新的一天日 Bucket 为空，但原月 Bucket 已结算 600 + 预留 1_000，只剩 400，月门拒绝。
	dayThree := dayTwo.Add(24 * time.Hour)
	budgetClock.Set(dayThree)
	_, nextDay := startBudgetTestRun(t, ctx, runRepo, tenantID,
		"run-budget-period-next-day", "conversation-budget-period-next-day", 40_004, dayThree, 3*time.Hour)
	_, err = runRepo.ReserveAgentBudget(ctx, nextDay)
	require.ErrorIs(t, err, ErrAgentBudgetExceeded)

	monthBucket, err := runRepo.GetAgentBudgetBucket(ctx, tenantID, model.AgentBudgetPeriodMonth, monthStart)
	require.NoError(t, err)
	require.Equal(t, int64(1_000), monthBucket.ReservedCostMicros)
	require.Equal(t, int64(600), monthBucket.SettledCostMicros)
}

func TestAgentBudgetRepo_ExpiredAttemptBecomesUnknownBeforeRunRetry(t *testing.T) {
	database, cleanup := setupBudgetTestContext(t)
	defer cleanup()

	runRepo, err := NewAgentRunRepo(database)
	require.NoError(t, err)
	ctx := context.Background()
	tenantID := "tenant-budget-recovery"
	base := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	setBudgetTestClock(t, runRepo, base)
	_, err = runRepo.PutAgentBudgetPolicy(ctx, testBudgetPolicy(tenantID, 300, 300, 3_000, 3_000), 0)
	require.NoError(t, err)

	startingOne, reservationOne := startBudgetTestRun(t, ctx, runRepo, tenantID,
		"run-budget-recovery", "conversation-budget-recovery", 50_001, base, time.Minute)
	_, err = runRepo.ReserveAgentBudget(ctx, reservationOne)
	require.NoError(t, err)
	recoveryAt := base.Add(3 * time.Minute)

	recovered, err := runRepo.RecoverExpiredAgentBudgetAttempts(ctx, tenantID, recoveryAt)
	require.NoError(t, err)
	require.Equal(t, int64(1), recovered)
	replayedRecovery, err := runRepo.RecoverExpiredAgentBudgetAttempts(ctx, tenantID, recoveryAt.Add(time.Second))
	require.NoError(t, err)
	require.Zero(t, replayedRecovery)

	attemptOne, err := runRepo.GetAgentBudgetAttempt(ctx, tenantID, startingOne.RunID, startingOne.Attempt)
	require.NoError(t, err)
	require.Equal(t, model.AgentBudgetAttemptStatusUnknown, attemptOne.Status)
	dayStart, _ := budgetPeriodStarts(base)
	bucket, err := runRepo.GetAgentBudgetBucket(ctx, tenantID, model.AgentBudgetPeriodDay, dayStart)
	require.NoError(t, err)
	require.Equal(t, int64(100), bucket.ReservedTokens)
	require.Equal(t, int64(100), bucket.UnknownReservedTokens)

	runRecovery, err := runRepo.RecoverExpiredAgentRuns(ctx, tenantID, recoveryAt)
	require.NoError(t, err)
	require.Equal(t, int64(1), runRecovery.RetryableExecutions)
	reclaimed, err := runRepo.ClaimNextAgentRun(ctx, testClaim(tenantID, "worker-budget-retry", "token-budget-retry", recoveryAt.Add(time.Second)))
	require.NoError(t, err)
	require.Equal(t, 2, reclaimed.Attempt)
	startingTwo, err := runRepo.AdvanceAgentRun(ctx, AgentRunTransition{
		Lease: leaseFor(reclaimed, recoveryAt.Add(2*time.Second)),
		From:  model.AgentRunStatusClaimed, To: model.AgentRunStatusStartingRuntime,
	})
	require.NoError(t, err)
	_, err = runRepo.ReserveAgentBudget(ctx, AgentBudgetReservation{
		Lease: leaseFor(startingTwo, recoveryAt.Add(3*time.Second)), Attempt: startingTwo.Attempt,
		ProfileID: startingTwo.ProfileID, ProfileVersion: startingTwo.ProfileVersion,
	})
	require.NoError(t, err)

	bucket, err = runRepo.GetAgentBudgetBucket(ctx, tenantID, model.AgentBudgetPeriodDay, dayStart)
	require.NoError(t, err)
	require.Equal(t, int64(200), bucket.ReservedTokens, "retry is a new reservation while the unknown attempt stays held")
	require.Equal(t, int64(100), bucket.UnknownReservedTokens)
	attemptOneAgain, err := runRepo.GetAgentBudgetAttempt(ctx, tenantID, startingOne.RunID, 1)
	require.NoError(t, err)
	require.Equal(t, attemptOne.Version, attemptOneAgain.Version)
}

func testBudgetPolicy(tenantID string, dailyTokens, monthlyTokens, dailyCostMicros, monthlyCostMicros int64) *model.AgentBudgetPolicy {
	return &model.AgentBudgetPolicy{
		TenantID: tenantID, Enabled: true,
		DailyTokenLimit: dailyTokens, MonthlyTokenLimit: monthlyTokens,
		DailyCostLimitMicros: dailyCostMicros, MonthlyCostLimitMicros: monthlyCostMicros,
		MaxAttemptTokens: 100, MaxAttemptCostMicros: 1_000,
	}
}

func startBudgetTestRun(
	t *testing.T,
	ctx context.Context,
	runRepo AgentRunRepo,
	tenantID, runID, conversationID string,
	eventID int64,
	base time.Time,
	leaseDuration time.Duration,
) (*model.AgentRun, AgentBudgetReservation) {
	t.Helper()
	run := newTestAgentRun(runID, tenantID, conversationID, eventID, 1, base)
	_, err := runRepo.EnqueueAgentRun(ctx, run)
	require.NoError(t, err)
	claim := testClaim(tenantID, "worker-"+runID, "token-"+runID, base.Add(time.Second))
	claim.LeaseDuration = leaseDuration
	claimed, err := runRepo.ClaimNextAgentRun(ctx, claim)
	require.NoError(t, err)
	transitionLease := leaseFor(claimed, base.Add(2*time.Second))
	transitionLease.LeaseDuration = leaseDuration
	starting, err := runRepo.AdvanceAgentRun(ctx, AgentRunTransition{
		Lease: transitionLease, From: model.AgentRunStatusClaimed, To: model.AgentRunStatusStartingRuntime,
	})
	require.NoError(t, err)
	reservation := AgentBudgetReservation{
		Lease: leaseFor(starting, base.Add(3*time.Second)), Attempt: starting.Attempt,
		ProfileID: starting.ProfileID, ProfileVersion: starting.ProfileVersion,
	}
	return starting, reservation
}

func setupBudgetTestContext(t *testing.T) (genesisdb.DB, func()) {
	t.Helper()
	database, cleanup := setupTestContext(t)
	cleanupAgentBudgetData(t, database)
	return database, func() {
		cleanupAgentBudgetData(t, database)
		cleanup()
	}
}

func cleanupAgentBudgetData(t *testing.T, database genesisdb.DB) {
	t.Helper()
	if database == nil {
		return
	}
	err := database.DB(context.Background()).Exec(
		"TRUNCATE TABLE t_agent_budget_attempt, t_agent_budget_bucket, t_agent_budget_policy RESTART IDENTITY CASCADE",
	).Error
	require.NoError(t, err)
}

type budgetTestClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *budgetTestClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now.UTC()
}

func (c *budgetTestClock) Now(*gorm.DB) (time.Time, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now, nil
}

func setBudgetTestClock(t *testing.T, runRepo AgentRunRepo, now time.Time) *budgetTestClock {
	t.Helper()
	implementation, ok := runRepo.(*agentRunRepo)
	require.True(t, ok)
	clock := &budgetTestClock{now: now.UTC()}
	implementation.budgetNow = clock.Now
	return clock
}
