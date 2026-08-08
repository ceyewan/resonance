package pi

import (
	"bytes"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

const (
	defaultCapabilityEnvName = "RESONANCE_AGENT_CAPABILITY"
	toolBrokerURLEnvName     = "RESONANCE_TOOL_BROKER_URL"
	runIDEnvName             = "RESONANCE_AGENT_RUN_ID"
	maxTokensEnvName         = "RESONANCE_AGENT_MAX_TOTAL_TOKENS"
	maxCostEnvName           = "RESONANCE_AGENT_MAX_COST_MICROS"
	maxProviderCallsEnvName  = "RESONANCE_AGENT_MAX_PROVIDER_CALLS"
	piAgentDirEnvName        = "PI_CODING_AGENT_DIR"
	piSettingsFileName       = "settings.json"
	pinnedPiSettings         = `{
  "retry": {
    "enabled": true,
    "maxRetries": 3,
    "baseDelayMs": 2000,
    "provider": {
      "maxRetries": 0,
      "maxRetryDelayMs": 60000
    }
  }
}
`
)

var environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Config 是 Pi Adapter 的安全配置。启动参数不提供任意 extra args，避免绕过固定基线。
type Config struct {
	Binary            string
	ExpectedVersion   string
	ExtensionPath     string
	WorkDir           string
	AgentDir          string
	ToolBrokerURL     string
	Environment       []string
	CapabilityEnvName string

	MaxFrameBytes     int
	MaxOutputBytes    int64
	MaxStderrBytes    int
	EventQueueSize    int
	StartupEventLimit int
	EventOfferTimeout time.Duration
	CommandTimeout    time.Duration
	ProbeTimeout      time.Duration
	AbortGrace        time.Duration
	TermGrace         time.Duration
	KillGrace         time.Duration
}

func (c *Config) setDefaults() {
	if c.CapabilityEnvName == "" {
		c.CapabilityEnvName = defaultCapabilityEnvName
	}
	if c.MaxFrameBytes <= 0 {
		c.MaxFrameBytes = defaultMaxFrameBytes
	}
	if c.MaxOutputBytes <= 0 {
		c.MaxOutputBytes = defaultMaxOutputBytes
	}
	if c.MaxStderrBytes <= 0 {
		c.MaxStderrBytes = 64 << 10
	}
	if c.EventQueueSize <= 0 {
		c.EventQueueSize = defaultEventQueueSize
	}
	if c.StartupEventLimit <= 0 {
		c.StartupEventLimit = 1024
	}
	if c.EventOfferTimeout <= 0 {
		c.EventOfferTimeout = defaultEventOfferTimout
	}
	if c.CommandTimeout <= 0 {
		c.CommandTimeout = 5 * time.Second
	}
	if c.ProbeTimeout <= 0 {
		c.ProbeTimeout = 5 * time.Second
	}
	if c.AbortGrace <= 0 {
		c.AbortGrace = 2 * time.Second
	}
	if c.TermGrace <= 0 {
		c.TermGrace = 2 * time.Second
	}
	if c.KillGrace <= 0 {
		c.KillGrace = 2 * time.Second
	}
}

func (c Config) validate() error {
	if strings.TrimSpace(c.Binary) == "" {
		return fmt.Errorf("pi binary is required")
	}
	if !filepath.IsAbs(c.Binary) {
		return fmt.Errorf("pi binary must be an absolute path")
	}
	if strings.TrimSpace(c.ExpectedVersion) == "" {
		return fmt.Errorf("pi expected version is required")
	}
	if strings.TrimSpace(c.ExtensionPath) == "" {
		return fmt.Errorf("pi trusted extension path is required")
	}
	if !filepath.IsAbs(c.ExtensionPath) {
		return fmt.Errorf("pi trusted extension path must be absolute")
	}
	if strings.TrimSpace(c.WorkDir) == "" {
		return fmt.Errorf("pi empty work directory is required")
	}
	if !filepath.IsAbs(c.WorkDir) {
		return fmt.Errorf("pi empty work directory must be absolute")
	}
	if strings.TrimSpace(c.AgentDir) == "" || !filepath.IsAbs(c.AgentDir) || filepath.Clean(c.AgentDir) != c.AgentDir {
		return fmt.Errorf("pi pinned agent directory must be an absolute clean path")
	}
	brokerURL, err := url.Parse(c.ToolBrokerURL)
	if err != nil || brokerURL.Scheme != "http" || brokerURL.User != nil || brokerURL.Port() == "" ||
		(brokerURL.Path != "" && brokerURL.Path != "/") || brokerURL.RawQuery != "" || brokerURL.Fragment != "" {
		return fmt.Errorf("pi tool broker URL must be an explicit loopback HTTP endpoint")
	}
	brokerIP := net.ParseIP(brokerURL.Hostname())
	if brokerIP == nil || !brokerIP.IsLoopback() {
		return fmt.Errorf("pi tool broker URL must be an explicit loopback HTTP endpoint")
	}
	if !environmentNamePattern.MatchString(c.CapabilityEnvName) {
		return fmt.Errorf("pi capability environment name is invalid")
	}
	seen := make(map[string]struct{}, len(c.Environment))
	for _, item := range c.Environment {
		name, _, ok := strings.Cut(item, "=")
		if !ok || !environmentNamePattern.MatchString(name) {
			return fmt.Errorf("pi environment entry has invalid name")
		}
		if name == "NODE_OPTIONS" || name == "NODE_PATH" {
			return fmt.Errorf("pi environment entry %s is forbidden", name)
		}
		if name == c.CapabilityEnvName || name == toolBrokerURLEnvName || name == runIDEnvName || name == piAgentDirEnvName ||
			name == maxTokensEnvName || name == maxCostEnvName || name == maxProviderCallsEnvName {
			return fmt.Errorf("pi environment entry %s is reserved", name)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("pi environment entry %s is duplicated", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func (c Config) buildProcessSpec(req pilotruntime.RunRequest) (ProcessSpec, error) {
	if err := validatePinnedAgentDirectory(c.AgentDir); err != nil {
		return ProcessSpec{}, err
	}
	if strings.TrimSpace(req.RunID) == "" {
		return ProcessSpec{}, fmt.Errorf("run id is required")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return ProcessSpec{}, fmt.Errorf("prompt is required")
	}
	if strings.TrimSpace(req.Profile.Provider) == "" || strings.TrimSpace(req.Profile.Model) == "" {
		return ProcessSpec{}, fmt.Errorf("profile provider and model are required")
	}
	if strings.TrimSpace(req.Profile.SystemPrompt) == "" {
		return ProcessSpec{}, fmt.Errorf("profile system prompt is required")
	}
	if strings.TrimSpace(req.Session.Directory) == "" {
		return ProcessSpec{}, fmt.Errorf("staging session directory is required")
	}
	if _, err := secureSessionFile(req.Session.Directory, ""); err != nil {
		return ProcessSpec{}, err
	}
	if req.Capability.IsZero() {
		return ProcessSpec{}, fmt.Errorf("run capability is required")
	}
	if !req.Limits.Valid() {
		return ProcessSpec{}, fmt.Errorf("run execution limits are required")
	}
	if req.Session.FilePath != "" {
		if _, err := secureSessionFile(req.Session.Directory, req.Session.FilePath); err != nil {
			return ProcessSpec{}, err
		}
	}

	args := []string{
		"--mode", "rpc",
		"--provider", req.Profile.Provider,
		"--model", req.Profile.Model,
		"--session-dir", req.Session.Directory,
		"--system-prompt", req.Profile.SystemPrompt,
		"--no-builtin-tools",
		"--no-extensions",
		"--extension", c.ExtensionPath,
		"--no-skills",
		"--no-prompt-templates",
		"--no-context-files",
		"--no-themes",
		"--no-approve",
	}
	if req.Session.FilePath != "" {
		args = append(args, "--session", req.Session.FilePath)
	}

	env := make([]string, 0, len(c.Environment)+7)
	env = append(env, c.Environment...)
	env = append(env,
		c.CapabilityEnvName+"="+req.Capability.Reveal(),
		toolBrokerURLEnvName+"="+c.ToolBrokerURL,
		runIDEnvName+"="+req.RunID,
		piAgentDirEnvName+"="+c.AgentDir,
		maxTokensEnvName+"="+strconv.FormatInt(req.Limits.MaxTotalTokens, 10),
		maxCostEnvName+"="+strconv.FormatInt(req.Limits.MaxCostMicros, 10),
		maxProviderCallsEnvName+"="+strconv.Itoa(req.Limits.MaxProviderCalls),
	)
	return ProcessSpec{Path: c.Binary, Args: args, Env: env, Dir: c.WorkDir}, nil
}

// PreparePinnedAgentDirectory writes the only Pi settings accepted by the
// Runtime. Pi's SDK-level Provider retry must remain zero: every outer Agent
// retry re-enters before_provider_request and consumes a fresh Bridge budget,
// while an SDK retry would otherwise make multiple HTTP attempts under one
// budget decision.
func PreparePinnedAgentDirectory(directory string) error {
	if directory == "" || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return fmt.Errorf("pi pinned agent directory must be an absolute clean path")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create pi pinned agent directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("pi pinned agent directory must be a non-symlink directory")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return fmt.Errorf("resolve pi pinned agent directory: %w", err)
	}
	if !filepath.IsAbs(resolved) {
		return fmt.Errorf("resolved pi pinned agent directory must be absolute")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("protect pi pinned agent directory: %w", err)
	}
	settingsPath := filepath.Join(directory, piSettingsFileName)
	if current, statErr := os.Lstat(settingsPath); statErr == nil && current.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("pi pinned settings must not be a symbolic link")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect pi pinned settings: %w", statErr)
	}
	temporary, err := os.CreateTemp(directory, ".settings-*")
	if err != nil {
		return fmt.Errorf("create pi pinned settings: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect pi pinned settings: %w", err)
	}
	if _, err := temporary.WriteString(pinnedPiSettings); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write pi pinned settings: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync pi pinned settings: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close pi pinned settings: %w", err)
	}
	if err := os.Rename(temporaryPath, settingsPath); err != nil {
		return fmt.Errorf("publish pi pinned settings: %w", err)
	}
	return validatePinnedAgentDirectory(directory)
}

func validatePinnedAgentDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("pi pinned agent directory is missing or unsafe")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return fmt.Errorf("resolve pi pinned agent directory: %w", err)
	}
	if !filepath.IsAbs(resolved) {
		return fmt.Errorf("resolved pi pinned agent directory must be absolute")
	}
	settingsPath := filepath.Join(directory, piSettingsFileName)
	settingsInfo, err := os.Lstat(settingsPath)
	if err != nil || !settingsInfo.Mode().IsRegular() || settingsInfo.Mode()&os.ModeSymlink != 0 ||
		settingsInfo.Mode().Perm()&0o077 != 0 || settingsInfo.Size() != int64(len(pinnedPiSettings)) {
		return fmt.Errorf("pi pinned settings are missing or unsafe")
	}
	contents, err := os.ReadFile(settingsPath)
	if err != nil || !bytes.Equal(contents, []byte(pinnedPiSettings)) {
		return fmt.Errorf("pi pinned settings do not match the trusted retry policy")
	}
	return nil
}

// secureSessionFile 把可信 staging root 解析为真实路径，并拒绝 root 内部的符号链接跳转。
// filePath 允许不存在的最终文件，但它的父目录必须已经存在且不是符号链接。
func secureSessionFile(directory, filePath string) (string, error) {
	directoryAbs, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolve staging session directory: %w", err)
	}
	directoryLinkInfo, err := os.Lstat(directoryAbs)
	if err != nil {
		return "", fmt.Errorf("inspect staging session directory: %w", err)
	}
	if directoryLinkInfo.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("staging session directory must not be a symbolic link")
	}
	resolvedDirectory, err := filepath.EvalSymlinks(directoryAbs)
	if err != nil {
		return "", fmt.Errorf("resolve staging session directory symlinks: %w", err)
	}
	directoryInfo, err := os.Stat(resolvedDirectory)
	if err != nil {
		return "", fmt.Errorf("stat staging session directory: %w", err)
	}
	if !directoryInfo.IsDir() {
		return "", fmt.Errorf("staging session directory is not a directory")
	}
	if directoryInfo.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("staging session directory permissions must not allow group or other access")
	}
	if filePath == "" {
		return resolvedDirectory, nil
	}

	fileAbs, err := filepath.Abs(filePath)
	if err != nil {
		return "", fmt.Errorf("resolve staging session file: %w", err)
	}
	relative, err := filepath.Rel(directoryAbs, fileAbs)
	if err != nil {
		return "", fmt.Errorf("compare staging session paths: %w", err)
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("staging session file must be inside session directory")
	}

	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	candidate := resolvedDirectory
	for index, part := range parts {
		candidate = filepath.Join(candidate, part)
		info, statErr := os.Lstat(candidate)
		if statErr != nil {
			if os.IsNotExist(statErr) && index == len(parts)-1 {
				return candidate, nil
			}
			return "", fmt.Errorf("inspect staging session path: %w", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("staging session path must not contain symbolic links")
		}
		if index < len(parts)-1 {
			if !info.IsDir() {
				return "", fmt.Errorf("staging session parent is not a directory")
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("staging session file must be a regular file")
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
			return "", fmt.Errorf("staging session file must not have multiple hard links")
		}
	}
	return candidate, nil
}
