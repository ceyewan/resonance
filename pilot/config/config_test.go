package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfig_ValidatesSecurityAndTimingBoundaries(t *testing.T) {
	base := validConfig()
	require.NoError(t, base.Validate())

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "shared task queue", mutate: func(c *Config) { c.Ingress.QueueGroup = "resonance_group_chat_event" }},
		{name: "stream shares durable chat topic", mutate: func(c *Config) { c.Stream.Topic = c.Ingress.Topic }},
		{name: "unbounded stream chunk", mutate: func(c *Config) { c.Stream.MaxChunkBytes = c.Stream.MaxPendingBytes + 1 }},
		{name: "short lease", mutate: func(c *Config) { c.Worker.LeaseTTL = 2 * c.Worker.HeartbeatInterval }},
		{name: "short capability", mutate: func(c *Config) { c.Broker.CapabilityTTL = c.Worker.RunTimeout - time.Second }},
		{name: "remote broker", mutate: func(c *Config) { c.Broker.Address = "10.0.0.1:15094" }},
		{name: "local runtime with Unix broker", mutate: func(c *Config) { c.Broker.Address = "unix:///run/resonance-agent/broker.sock" }},
		{name: "relative Pi", mutate: func(c *Config) { c.Runtime.Binary = "pi" }},
		{name: "relative Pi agent dir", mutate: func(c *Config) { c.Runtime.AgentDir = "pi-agent" }},
		{name: "service secret absent", mutate: func(c *Config) { c.ServiceAuth.Secret = "" }},
		{name: "service credential leak", mutate: func(c *Config) { c.Runtime.ProviderEnvAllowlist = []string{"RESONANCE_POSTGRES_PASSWORD"} }},
		{name: "node injection", mutate: func(c *Config) { c.Runtime.ProviderEnvAllowlist = []string{"NODE_OPTIONS"} }},
		{name: "Pi settings injection", mutate: func(c *Config) { c.Runtime.ProviderEnvAllowlist = []string{"PI_CODING_AGENT_DIR"} }},
		{name: "metrics conflicts with health", mutate: func(c *Config) { c.Observability.Metrics.Port = c.Service.HTTPPort }},
		{name: "metrics conflicts with broker", mutate: func(c *Config) { c.Observability.Metrics.Port = 15094 }},
		{name: "invalid trace sampler", mutate: func(c *Config) { c.Observability.Trace.Sampler = 1.1 }},
		{name: "short session GC grace", mutate: func(c *Config) { c.Session.GCGrace = c.Worker.RunTimeout }},
		{name: "rollover exceeds hard session limit", mutate: func(c *Config) { c.Session.RolloverBytes = c.Session.MaxSnapshotBytes + 1 }},
		{name: "invalid rollover entry count", mutate: func(c *Config) { c.Session.RolloverEntryCount = -1 }},
		{name: "permissive budget fallback", mutate: func(c *Config) { c.Budget.PolicyMode = "allow_missing" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validConfig()
			test.mutate(&cfg)
			require.Error(t, cfg.Validate())
		})
	}
}

func TestConfig_RemoteRuntimeRequiresUDSAndDoesNotInheritProviderCredentials(t *testing.T) {
	cfg := validConfig()
	cfg.Runtime.Mode = RuntimeModeRemote
	cfg.Runtime.SocketPath = "/run/resonance-agent/runtime.sock"
	cfg.Runtime.RemoteMaxRequestBytes = 1 << 20
	cfg.Runtime.RemoteDialTimeout = time.Second
	cfg.Runtime.Binary = ""
	cfg.Runtime.ExtensionPath = ""
	cfg.Runtime.WorkDir = ""
	cfg.Runtime.ProviderEnvAllowlist = nil
	cfg.Broker.Address = "unix:///run/resonance-agent/broker.sock"
	require.NoError(t, cfg.Validate())
	_, err := cfg.RuntimeEnvironment()
	require.Error(t, err)

	cfg.Broker.Address = "127.0.0.1:15094"
	require.Error(t, cfg.Validate())
	cfg.Broker.Address = "unix:///run/resonance-agent/broker.sock"
	cfg.Runtime.SocketPath = "runtime.sock"
	require.Error(t, cfg.Validate())
}

func TestConfig_RuntimeEnvironmentIsExplicitAndRequired(t *testing.T) {
	cfg := validConfig()
	cfg.Runtime.ProviderEnvAllowlist = []string{"PATH", "ANTHROPIC_API_KEY"}
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("ANTHROPIC_API_KEY", "provider-secret")
	t.Setenv("RESONANCE_POSTGRES_PASSWORD", "must-not-leak")
	environment, err := cfg.RuntimeEnvironment()
	require.NoError(t, err)
	require.Equal(t, []string{"PATH=/usr/bin", "ANTHROPIC_API_KEY=provider-secret"}, environment)
	require.NotContains(t, environment, "RESONANCE_POSTGRES_PASSWORD=must-not-leak")

	cfg.Runtime.ProviderEnvAllowlist = []string{"MISSING_PROVIDER_KEY"}
	_, err = cfg.RuntimeEnvironment()
	require.ErrorContains(t, err, "MISSING_PROVIDER_KEY")
}

func TestConfig_BudgetPolicyModeDefaultsToFailClosed(t *testing.T) {
	var cfg Config
	cfg.Session.MaxSnapshotBytes = 1024
	cfg.setDefaults()
	require.Equal(t, "require_explicit", cfg.Budget.PolicyMode)
	require.Equal(t, int64(512), cfg.Session.RolloverBytes)
	require.Equal(t, int64(20_000), cfg.Session.RolloverEntryCount)
}

func TestSanitizedConfigJSON_RedactsEveryControlPlaneSecret(t *testing.T) {
	cfg := validConfig()
	cfg.PostgreSQL.Password = "postgres-password-marker"
	cfg.NATS.Password = "nats-password-marker"
	cfg.Etcd.Password = "etcd-password-marker"
	cfg.Broker.CapabilitySecret = "capability-secret-marker"
	cfg.ServiceAuth.Secret = "service-auth-secret-marker"

	payload, err := sanitizedConfigJSON(&cfg)
	require.NoError(t, err)
	serialized := string(payload)
	for _, secret := range []string{
		cfg.PostgreSQL.Password,
		cfg.NATS.Password,
		cfg.Etcd.Password,
		cfg.Broker.CapabilitySecret,
		cfg.ServiceAuth.Secret,
	} {
		require.NotContains(t, serialized, secret)
	}
	require.Equal(t, 5, strings.Count(serialized, "***configured***"))
}

func validConfig() Config {
	var cfg Config
	cfg.Service.Name = "pilot-service"
	cfg.Service.HTTPPort = 15093
	cfg.TenantID = "default"
	cfg.AgentBot = "resonance-agent"
	cfg.LogicServiceName = "logic-service"
	cfg.Ingress.Topic = "resonance.chat.event.v1"
	cfg.Ingress.QueueGroup = "resonance_group_agent_ingress_user_assistant"
	cfg.Ingress.DLQTopic = "resonance.chat.event.v1.agent.dlq"
	cfg.Ingress.MaxInflight = 8
	cfg.Ingress.MaxPromptBytes = 1024
	cfg.Stream.Topic = "resonance.agent.stream.v1"
	cfg.Stream.FlushInterval = 50 * time.Millisecond
	cfg.Stream.PublishTimeout = time.Second
	cfg.Stream.MaxStreams = 8
	cfg.Stream.MaxPendingBytes = 4096
	cfg.Stream.MaxChunkBytes = 1024
	cfg.Worker.Count = 2
	cfg.Worker.PollInterval = time.Second
	cfg.Worker.RecoveryInterval = time.Second
	cfg.Worker.LeaseTTL = 30 * time.Second
	cfg.Worker.HeartbeatInterval = 5 * time.Second
	cfg.Worker.RunTimeout = 10 * time.Minute
	cfg.Worker.ShutdownDrainTimeout = time.Second
	cfg.Worker.MaxAttempts = 3
	cfg.Runtime.Mode = RuntimeModeLocal
	cfg.Runtime.Binary = "/opt/resonance/pi"
	cfg.Runtime.ExpectedVersion = "0.84.1"
	cfg.Runtime.ExtensionPath = "/opt/resonance/bridge/index.ts"
	cfg.Runtime.WorkDir = "/var/lib/resonance-pilot/work"
	cfg.Runtime.AgentDir = "/var/lib/resonance-pilot/pi-agent"
	cfg.Broker.Address = "127.0.0.1:15094"
	cfg.Broker.CapabilitySecret = "01234567890123456789012345678901"
	cfg.Broker.CapabilityTTL = 15 * time.Minute
	cfg.Broker.MaxRequestBytes = 1024
	cfg.Broker.MaxResponseBytes = 1024
	cfg.Broker.RequestTimeout = time.Second
	cfg.Session.Root = "/var/lib/resonance-pilot/sessions"
	cfg.Session.MaxSnapshotBytes = 1024
	cfg.Session.RolloverBytes = 512
	cfg.Session.RolloverEntryCount = 100
	cfg.Session.GCInterval = time.Hour
	cfg.Session.GCGrace = 24 * time.Hour
	cfg.Profile.ID = "user-assistant"
	cfg.Profile.Version = 1
	cfg.Profile.Provider = "anthropic"
	cfg.Profile.Model = "model"
	cfg.Profile.SystemPrompt = "safe"
	cfg.Profile.BridgeVersion = "0.1.0"
	cfg.Profile.MaxFinalBytes = 1024
	cfg.Profile.HistoryLimit = 100
	cfg.Profile.MaxHistoryPromptBytes = 64 << 10
	cfg.Profile.MaxProviderCalls = 8
	cfg.Budget.PolicyMode = "require_explicit"
	cfg.ServiceAuth.ServiceID = "pilot-service"
	cfg.ServiceAuth.Secret = "01234567890123456789012345678901"
	cfg.Mutation.ApprovalTopic = "resonance.agent.approval.decided.v1"
	cfg.Mutation.QueueGroup = "resonance_group_agent_iam_mutation"
	cfg.Mutation.ApprovalTTL = 15 * time.Minute
	cfg.Mutation.ReconcileEvery = time.Second
	cfg.Mutation.BatchSize = 20
	cfg.Mutation.MaxInflight = 8
	cfg.Observability.Trace.Disable = true
	cfg.Observability.Metrics.Port = 9093
	cfg.Observability.Metrics.Path = "/metrics"
	return cfg
}
