//go:build pi_contract

package pi

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Starts the pinned Pi without making any Provider requests.
func TestRealPiRPCContract(t *testing.T) {
	binary := os.Getenv("RESONANCE_PI_BINARY")
	extension := os.Getenv("RESONANCE_PI_BRIDGE")
	if binary == "" || extension == "" {
		t.Skip("set RESONANCE_PI_BINARY and RESONANCE_PI_BRIDGE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	directory := t.TempDir()
	agentDir := filepath.Join(directory, "agent")
	require.NoError(t, PreparePinnedAgentDirectory(agentDir))
	process := exec.CommandContext(ctx, binary, "--mode", "rpc", "--provider", "dashscope", "--model", "qwen3.8-max", "--no-session", "--no-builtin-tools", "--no-extensions", "--extension", extension, "--no-skills", "--no-prompt-templates", "--no-context-files", "--no-themes", "--no-approve")
	process.Dir = directory
	process.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + directory, "PI_CODING_AGENT_DIR=" + agentDir, "PI_OFFLINE=1", "DASHSCOPE_API_KEY=offline-contract-key", "DASHSCOPE_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1", "DASHSCOPE_MODEL=qwen3.8-max", "RESONANCE_AGENT_MAX_OUTPUT_TOKENS=8192"}
	stdin, err := process.StdinPipe()
	require.NoError(t, err)
	stdout, err := process.StdoutPipe()
	require.NoError(t, err)
	process.Stderr = io.Discard
	require.NoError(t, process.Start())
	defer func() { _ = process.Process.Kill(); _ = process.Wait() }()
	client := NewRPCClient(stdin, stdout, ClientConfig{})
	commands, err := client.GetCommands(ctx)
	require.NoError(t, err)
	require.NoError(t, verifyBridgeCommands(commands))
}
