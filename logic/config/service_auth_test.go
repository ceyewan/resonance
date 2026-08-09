package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidatePilotServiceAuth_RequiresDistinctProfileWorkloads(t *testing.T) {
	base := ServiceAuthConfig{
		GatewayServiceID:  "gateway-service",
		GatewaySecret:     "gateway-secret-0123456789abcdef01",
		PilotServiceID:    "pilot-user-service",
		PilotSecret:       "pilot-user-secret-0123456789abcdef",
		PilotTenantID:     "tenant-a",
		IAMPilotServiceID: "pilot-iam-service",
		IAMPilotSecret:    "pilot-iam-secret-0123456789abcdef0",
		IAMPilotTenantID:  "tenant-a",
	}
	require.NoError(t, validatePilotServiceAuth(base, "resonance-agent"))

	tests := map[string]func(*ServiceAuthConfig){
		"missing user tenant":    func(config *ServiceAuthConfig) { config.PilotTenantID = "" },
		"missing iam service id": func(config *ServiceAuthConfig) { config.IAMPilotServiceID = "" },
		"same service id":        func(config *ServiceAuthConfig) { config.IAMPilotServiceID = config.PilotServiceID },
		"same profile secret":    func(config *ServiceAuthConfig) { config.IAMPilotSecret = config.PilotSecret },
		"gateway id reuse":       func(config *ServiceAuthConfig) { config.PilotServiceID = config.GatewayServiceID },
		"gateway secret reuse":   func(config *ServiceAuthConfig) { config.IAMPilotSecret = config.GatewaySecret },
		"invalid tenant":         func(config *ServiceAuthConfig) { config.IAMPilotTenantID = " tenant-a" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			config := base
			mutate(&config)
			require.Error(t, validatePilotServiceAuth(config, "resonance-agent"))
		})
	}
}

func TestValidatePilotServiceAuth_AllowsDisabledProfileFailClosed(t *testing.T) {
	config := ServiceAuthConfig{
		GatewayServiceID: "gateway-service",
		GatewaySecret:    "gateway-secret-0123456789abcdef01",
		PilotServiceID:   "pilot-user-service",
		PilotTenantID:    "tenant-a",
	}
	require.NoError(t, validatePilotServiceAuth(config, "resonance-agent"))

	config.PilotSecret = "short"
	require.Error(t, validatePilotServiceAuth(config, "resonance-agent"))
}
