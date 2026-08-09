//go:build pi_contract

package pi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

// TestRealPiRPCContract exercises the pinned upstream binary without making a
// provider request. Fake/helper tests remain the default correctness gate;
// this tagged test catches upstream flag and JSONL envelope drift.
func TestRealPiRPCContract(t *testing.T) {
	binary := os.Getenv("RESONANCE_PI_BINARY")
	if binary == "" {
		t.Skip("RESONANCE_PI_BINARY is not configured")
	}
	absolute, err := filepath.Abs(binary)
	require.NoError(t, err)
	require.Equal(t, absolute, binary)
	expectedVersion := os.Getenv("RESONANCE_PI_EXPECTED_VERSION")
	if expectedVersion == "" {
		expectedVersion = "0.84.1"
	}
	bridge := os.Getenv("RESONANCE_PI_BRIDGE")
	if bridge == "" {
		t.Skip("RESONANCE_PI_BRIDGE is not configured")
	}
	bridge, err = filepath.Abs(bridge)
	require.NoError(t, err)

	broker := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodGet, request.Method)
		require.Equal(t, "/v1/manifest", request.URL.Path)
		require.Equal(t, "Bearer contract-capability-secret", request.Header.Get("Authorization"))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"profile_id":"user-assistant","profile_version":1,"expires_at":"2999-01-01T00:00:00Z","tools":[{"name":"get_my_profile","label":"Get my profile","description":"Read my profile","input_schema":{"type":"object","properties":{},"additionalProperties":false},"risk":"ReadSelf","schema_version":1}]}`)
	}))
	defer broker.Close()

	versionContext, cancelVersion := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelVersion()
	versionOutput, err := exec.CommandContext(versionContext, binary, "--version").Output()
	require.NoError(t, err)
	require.Equal(t, expectedVersion, strings.TrimSpace(string(versionOutput)))

	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	require.NoError(t, PreparePinnedAgentDirectory(agentDir))
	process, err := (&execProcessStarter{}).Start(context.Background(), ProcessSpec{
		Path: binary,
		Args: []string{
			"--mode", "rpc", "--session-dir", filepath.Join(home, "sessions"), "--no-builtin-tools", "--no-extensions", "--extension", bridge,
			"--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files",
			"--offline", "--provider", "dashscope", "--model", "qwen3.8-max",
		},
		Env: []string{
			"PATH=" + os.Getenv("PATH"), "HOME=" + home, piAgentDirEnvName + "=" + agentDir, "PI_OFFLINE=1", "PI_TELEMETRY=0",
			"RESONANCE_TOOL_BROKER_URL=" + broker.URL,
			"RESONANCE_AGENT_CAPABILITY=contract-capability-secret",
			"RESONANCE_AGENT_RUN_ID=contract-run",
			"RESONANCE_AGENT_MAX_TOTAL_TOKENS=20000",
			"RESONANCE_AGENT_MAX_COST_MICROS=100000",
			"RESONANCE_AGENT_MAX_PROVIDER_CALLS=8",
			"DASHSCOPE_API_KEY=contract-test-key",
			"DASHSCOPE_BASE_URL=https://llm-3rwbpx52jtt7759p.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
			"DASHSCOPE_MODEL=qwen3.8-max",
		},
		Dir: home,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = process.Kill() })
	go func() { _, _ = io.Copy(io.Discard, process.Stderr()) }()

	client := NewRPCClient(process.Stdin(), process.Stdout(), ClientConfig{
		MaxFrameBytes: 1 << 20, MaxOutputBytes: 4 << 20,
		EventQueueSize: 16, EventOfferTimeout: time.Second,
	})
	contractContext, cancelContract := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelContract()
	state, err := client.GetState(contractContext)
	require.NoError(t, err)
	require.NotNil(t, state.Model)
	require.Equal(t, "dashscope", state.Model.Provider)
	require.Equal(t, "qwen3.8-max", state.Model.ID)
	require.NotEmpty(t, state.SessionID)
	commands, err := client.GetCommands(contractContext)
	require.NoError(t, err)
	if err := verifyBridgeCommands(commands, pilotruntime.ProfileSnapshot{ID: "user-assistant", Version: 1}); err != nil {
		t.Fatalf("verify Bridge readiness: %v; commands=%+v", err, commands.Commands)
	}
	stats, err := client.GetSessionStats(contractContext)
	require.NoError(t, err)
	require.Equal(t, state.SessionID, stats.SessionID)
	require.Equal(t, state.SessionFile, stats.SessionFile)
	require.Equal(t, stats.Tokens.Total,
		stats.Tokens.Input+stats.Tokens.Output+stats.Tokens.CacheRead+stats.Tokens.CacheWrite)
	require.NoError(t, client.Abort(contractContext))
	require.NoError(t, client.Close())

	wait := make(chan error, 1)
	go func() { wait <- process.Wait() }()
	select {
	case err := <-wait:
		require.NoError(t, err)
	case <-contractContext.Done():
		_ = process.Kill()
		t.Fatal("real Pi did not exit after stdin close")
	}
	require.NoError(t, validatePinnedAgentDirectory(agentDir), "pinned Pi must not mutate the trusted retry policy")
}
