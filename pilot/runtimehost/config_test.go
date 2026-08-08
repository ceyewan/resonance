package runtimehost

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfig_RequiresFixedProviderProxyAndOfflineEnvironment(t *testing.T) {
	config := validRuntimeHostConfig()
	require.NoError(t, config.Validate())

	for _, invalid := range [][]string{
		{"PATH", "ANTHROPIC_API_KEY"},
		{"PATH", "ANTHROPIC_API_KEY", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "PI_OFFLINE", "RESONANCE_POSTGRES_PASSWORD"},
		{"PATH", "ANTHROPIC_API_KEY", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "PI_OFFLINE", "PI_OFFLINE"},
	} {
		copy := config
		copy.Runtime.ProviderEnvAllowlist = invalid
		require.Error(t, copy.Validate())
	}
}

func TestConfig_RuntimeEnvironmentDoesNotInheritServiceCredentials(t *testing.T) {
	config := validRuntimeHostConfig()
	for name, value := range map[string]string{
		"PATH": "/usr/bin", "ANTHROPIC_API_KEY": "provider", "HTTP_PROXY": "http://proxy:18080",
		"HTTPS_PROXY": "http://proxy:18080", "NO_PROXY": "127.0.0.1,localhost", "PI_OFFLINE": "1", "PI_TELEMETRY": "0",
	} {
		t.Setenv(name, value)
	}
	t.Setenv("RESONANCE_POSTGRES_PASSWORD", "must-not-leak")
	environment, err := config.RuntimeEnvironment()
	require.NoError(t, err)
	require.NotContains(t, environment, "RESONANCE_POSTGRES_PASSWORD=must-not-leak")
}

func validRuntimeHostConfig() Config {
	var config Config
	config.Service.Name = "pilot-runtime"
	config.Service.HTTPPort = 15095
	config.Runtime.Binary = "/opt/resonance/bridge/node_modules/.bin/pi"
	config.Runtime.ExpectedVersion = "0.84.1"
	config.Runtime.ExtensionPath = "/opt/resonance/bridge/src/index.ts"
	config.Runtime.WorkDir = "/var/lib/resonance-pilot/work"
	config.Runtime.AgentDir = "/var/lib/resonance-pilot/pi-agent"
	config.Runtime.ProviderEnvAllowlist = []string{"PATH", "ANTHROPIC_API_KEY", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "PI_OFFLINE", "PI_TELEMETRY"}
	config.Runtime.ProviderProxyURL = "http://proxy:18080"
	config.Remote.SocketPath = "/run/resonance-agent/runtime.sock"
	config.Remote.SessionRoot = "/var/lib/resonance-pilot/sessions"
	config.Remote.MaxRequestBytes = 1 << 20
	config.Remote.MaxFrameBytes = 8 << 20
	config.Remote.HeaderTimeout = time.Second
	config.Relay.ListenAddress = "127.0.0.1:15094"
	config.Relay.BrokerSocket = "/run/resonance-agent/broker.sock"
	config.Relay.MaxRequestBytes = 64 << 10
	config.Relay.MaxResponseBytes = 64 << 10
	config.Relay.RequestTimeout = time.Second
	config.Relay.MaxConcurrent = 8
	return config
}
