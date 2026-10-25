package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func validConfig() *Config {
	c := &Config{AgentBot: "resonance-agent"}
	c.Assistant.Provider = "dashscope"
	c.Assistant.Model = "qwen3.8-max"
	c.Assistant.SystemPrompt = "you are pilot"
	c.Assistant.MaxOutputTokens = 8192
	c.ServiceAuth.ServiceID = "pilot-service"
	c.ServiceAuth.Secret = strings.Repeat("s", 32)
	c.Runtime.Binary = "/opt/resonance/bridge/node_modules/.bin/pi"
	c.Runtime.ExtensionPath = "/opt/resonance/bridge/src/index.ts"
	c.Runtime.WorkDir = "/var/lib/resonance-pilot/work"
	c.Runtime.AgentDir = "/var/lib/resonance-pilot/agent"
	c.Runtime.ExpectedVersion = "0.84.1"
	c.Runtime.ExpectedBridgeProtocol = "1"
	c.Ingress.QueueGroup = "resonance_group_pilot"
	c.Stream.MaxPendingBytes = 262144
	c.Stream.MaxChunkBytes = 16384
	c.Worker.Count = 4
	c.Worker.MaxAttempts = 3
	c.Worker.ShutdownDrainTimeout = 30_000_000_000
	return c
}

func TestConfig_AcceptsTheShippedShape(t *testing.T) {
	require.NoError(t, validConfig().Validate())
}

// 配成 Task 的 queue group 会把本该写扩散的消息抢到 Pilot，任务侧静默丢消息。
func TestConfig_RejectsTaskQueueGroup(t *testing.T) {
	c := validConfig()
	c.Ingress.QueueGroup = taskQueueGroup
	require.ErrorContains(t, c.Validate(), "must not share the Task queue group")
}

// 分块上限大于待发上限时流控实际失效：待发队列先满，分块永远发不出去。
func TestConfig_RejectsChunkLimitAbovePendingBuffer(t *testing.T) {
	c := validConfig()
	c.Stream.MaxChunkBytes = c.Stream.MaxPendingBytes + 1
	require.ErrorContains(t, c.Validate(), "max_chunk_bytes")

	c = validConfig()
	c.Stream.MaxChunkBytes = 0
	require.ErrorContains(t, c.Validate(), "max_chunk_bytes")
}

func TestConfig_RequiresSeparateSigningSecret(t *testing.T) {
	c := validConfig()
	c.ServiceAuth.Secret = strings.Repeat("s", 31)
	require.ErrorContains(t, c.Validate(), "service signing secret")
}

func TestConfig_RequiresAbsoluteRuntimePaths(t *testing.T) {
	c := validConfig()
	c.Runtime.Binary = "pi"
	require.ErrorContains(t, c.Validate(), "absolute paths")
}
