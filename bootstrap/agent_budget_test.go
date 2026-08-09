package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/model"
)

func TestValidateInitialAgentBudgetPolicyRequiresEnabledBoundedLimits(t *testing.T) {
	valid := &model.AgentBudgetPolicy{
		TenantID: "default", Enabled: true,
		DailyTokenLimit: 2_000_000, MonthlyTokenLimit: 20_000_000,
		DailyCostLimitMicros: 50_000_000, MonthlyCostLimitMicros: 500_000_000,
		MaxAttemptTokens: 65_536, MaxAttemptCostMicros: 10_000_000,
	}
	require.NoError(t, validateInitialAgentBudgetPolicy(valid))

	for _, mutate := range []func(*model.AgentBudgetPolicy){
		func(policy *model.AgentBudgetPolicy) { policy.Enabled = false },
		func(policy *model.AgentBudgetPolicy) { policy.TenantID = "" },
		func(policy *model.AgentBudgetPolicy) { policy.MonthlyTokenLimit = policy.DailyTokenLimit - 1 },
		func(policy *model.AgentBudgetPolicy) { policy.MaxAttemptTokens = policy.DailyTokenLimit + 1 },
		func(policy *model.AgentBudgetPolicy) { policy.MaxAttemptCostMicros = policy.DailyCostLimitMicros + 1 },
	} {
		candidate := *valid
		mutate(&candidate)
		require.Error(t, validateInitialAgentBudgetPolicy(&candidate))
	}
}
