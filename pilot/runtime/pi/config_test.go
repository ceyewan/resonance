package pi

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

func TestConfig_RejectsUnpinnedOrInjectableRuntime(t *testing.T) {
	base := func() Config {
		agentDir := filepath.Join(t.TempDir(), "agent")
		require.NoError(t, PreparePinnedAgentDirectory(agentDir))
		return Config{
			Binary:          "/usr/local/bin/pi",
			ExpectedVersion: "pi-1.2.3",
			ExtensionPath:   "/opt/resonance/bridge/index.ts",
			WorkDir:         t.TempDir(),
			AgentDir:        agentDir,
			ToolBrokerURL:   "http://127.0.0.1:15094",
			Environment:     []string{"PATH=/usr/local/bin:/usr/bin"},
		}
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "missing version", mutate: func(config *Config) { config.ExpectedVersion = "" }},
		{name: "relative binary", mutate: func(config *Config) { config.Binary = "pi" }},
		{name: "relative extension", mutate: func(config *Config) { config.ExtensionPath = "bridge.ts" }},
		{name: "relative agent dir", mutate: func(config *Config) { config.AgentDir = "agent" }},
		{name: "invalid capability env", mutate: func(config *Config) { config.CapabilityEnvName = "BAD-NAME" }},
		{name: "node options", mutate: func(config *Config) { config.Environment = []string{"NODE_OPTIONS=--require=/tmp/inject.js"} }},
		{name: "node path", mutate: func(config *Config) { config.Environment = []string{"NODE_PATH=/tmp/inject"} }},
		{name: "duplicate env", mutate: func(config *Config) { config.Environment = []string{"PATH=/a", "PATH=/b"} }},
		{name: "invalid env", mutate: func(config *Config) { config.Environment = []string{"BAD-NAME=value"} }},
		{name: "remote broker", mutate: func(config *Config) { config.ToolBrokerURL = "http://10.0.0.2:15094" }},
		{name: "broker credentials", mutate: func(config *Config) { config.ToolBrokerURL = "http://user@127.0.0.1:15094" }},
		{name: "reserved broker env", mutate: func(config *Config) { config.Environment = []string{"RESONANCE_TOOL_BROKER_URL=http://127.0.0.1:1"} }},
		{name: "reserved run env", mutate: func(config *Config) { config.Environment = []string{"RESONANCE_AGENT_RUN_ID=forged"} }},
		{name: "reserved budget env", mutate: func(config *Config) { config.Environment = []string{"RESONANCE_AGENT_MAX_TOTAL_TOKENS=999999"} }},
		{name: "reserved agent dir env", mutate: func(config *Config) { config.Environment = []string{"PI_CODING_AGENT_DIR=/tmp/forged"} }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base()
			test.mutate(&config)
			config.setDefaults()
			require.Error(t, config.validate())
		})
	}
}

func TestConfig_RequiresPrivateExistingSessionDirectory(t *testing.T) {
	agentDir := filepath.Join(t.TempDir(), "agent")
	require.NoError(t, PreparePinnedAgentDirectory(agentDir))
	config := Config{
		Binary:          "/usr/local/bin/pi",
		ExpectedVersion: "pi-1.2.3",
		ExtensionPath:   "/opt/resonance/bridge/index.ts",
		WorkDir:         t.TempDir(),
		AgentDir:        agentDir,
		ToolBrokerURL:   "http://127.0.0.1:15094",
	}
	config.setDefaults()
	require.NoError(t, config.validate())

	req := testRunRequest(t, "run-permissions")
	require.NoError(t, os.Chmod(req.Session.Directory, 0o755))
	_, err := config.buildProcessSpec(req)
	require.ErrorContains(t, err, "permissions")

	require.NoError(t, os.Chmod(req.Session.Directory, 0o700))
	req.Session.FilePath = filepath.Join(req.Session.Directory, "missing", "session.jsonl")
	_, err = config.buildProcessSpec(req)
	require.ErrorContains(t, err, "inspect staging session path")
}

func TestConfig_RejectsBudgetLimitsThatTypeScriptCannotRepresentExactly(t *testing.T) {
	agentDir := filepath.Join(t.TempDir(), "agent")
	require.NoError(t, PreparePinnedAgentDirectory(agentDir))
	config := Config{
		Binary: "/usr/local/bin/pi", ExpectedVersion: "pi-1.2.3",
		ExtensionPath: "/opt/resonance/bridge/index.ts", WorkDir: t.TempDir(), AgentDir: agentDir,
		ToolBrokerURL: "http://127.0.0.1:15094",
	}
	config.setDefaults()
	require.NoError(t, config.validate())

	req := testRunRequest(t, "run-unsafe-budget")
	req.Limits = pilotruntime.ExecutionLimits{
		MaxTotalTokens: 9_007_199_254_740_992, MaxCostMicros: 1, MaxProviderCalls: 1,
	}
	_, err := config.buildProcessSpec(req)
	require.ErrorContains(t, err, "execution limits")
}

func TestPinnedAgentDirectory_FailsClosedOnRetryPolicyTampering(t *testing.T) {
	agentDir := filepath.Join(t.TempDir(), "agent")
	require.NoError(t, PreparePinnedAgentDirectory(agentDir))
	require.NoError(t, validatePinnedAgentDirectory(agentDir))

	require.NoError(t, os.WriteFile(filepath.Join(agentDir, piSettingsFileName), []byte(`{"retry":{"provider":{"maxRetries":1}}}`), 0o600))
	require.Error(t, validatePinnedAgentDirectory(agentDir))

	require.NoError(t, PreparePinnedAgentDirectory(agentDir))
	require.NoError(t, validatePinnedAgentDirectory(agentDir))
}

func TestSecureSessionFile_RejectsUnsafeFilesystemObjects(t *testing.T) {
	t.Run("session root symlink", func(t *testing.T) {
		target := t.TempDir()
		require.NoError(t, os.Chmod(target, 0o700))
		link := filepath.Join(t.TempDir(), "session-root")
		require.NoError(t, os.Symlink(target, link))

		_, err := secureSessionFile(link, "")
		require.ErrorContains(t, err, "must not be a symbolic link")
	})

	t.Run("fifo", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.Chmod(root, 0o700))
		fifo := filepath.Join(root, "session.jsonl")
		require.NoError(t, syscall.Mkfifo(fifo, 0o600))

		_, err := secureSessionFile(root, fifo)
		require.ErrorContains(t, err, "regular file")
	})

	t.Run("hard link", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.Chmod(root, 0o700))
		outside := filepath.Join(t.TempDir(), "outside.jsonl")
		require.NoError(t, os.WriteFile(outside, []byte("session"), 0o600))
		linked := filepath.Join(root, "session.jsonl")
		require.NoError(t, os.Link(outside, linked))

		_, err := secureSessionFile(root, linked)
		require.ErrorContains(t, err, "multiple hard links")
	})
}
