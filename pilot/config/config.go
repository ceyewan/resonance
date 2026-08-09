package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ceyewan/genesis/clog"
	genesisconfig "github.com/ceyewan/genesis/config"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/mq"
	"github.com/ceyewan/genesis/registry"

	pilotobservability "github.com/ceyewan/resonance/pilot/observability"
)

var environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Config struct {
	Service struct {
		Name     string `mapstructure:"name"`
		HTTPPort int    `mapstructure:"http_port"`
	} `mapstructure:"service"`

	TenantID         string `mapstructure:"tenant_id"`
	AgentBot         string `mapstructure:"agent_bot"`
	LogicServiceName string `mapstructure:"logic_service_name"`

	Log        clog.Config                `mapstructure:"log"`
	PostgreSQL connector.PostgreSQLConfig `mapstructure:"postgres"`
	NATS       connector.NATSConfig       `mapstructure:"nats"`
	Etcd       connector.EtcdConfig       `mapstructure:"etcd"`
	Registry   RegistryConfig             `mapstructure:"registry"`
	JetStream  mq.JetStreamConfig         `mapstructure:"jetstream"`

	Ingress       IngressConfig             `mapstructure:"ingress"`
	Stream        StreamConfig              `mapstructure:"stream"`
	Worker        WorkerConfig              `mapstructure:"worker"`
	Runtime       RuntimeConfig             `mapstructure:"runtime"`
	Broker        BrokerConfig              `mapstructure:"broker"`
	Session       SessionConfig             `mapstructure:"session"`
	Profile       ProfileConfig             `mapstructure:"profile"`
	Budget        BudgetConfig              `mapstructure:"budget"`
	ServiceAuth   ServiceAuthConfig         `mapstructure:"service_auth"`
	Mutation      MutationConfig            `mapstructure:"mutation"`
	Observability pilotobservability.Config `mapstructure:"observability"`
}

type RegistryConfig struct {
	Namespace  string        `mapstructure:"namespace"`
	DefaultTTL time.Duration `mapstructure:"default_ttl"`
}

func (c RegistryConfig) ToRegistryConfig() *registry.Config {
	return &registry.Config{Namespace: c.Namespace, DefaultTTL: c.DefaultTTL}
}

type IngressConfig struct {
	Topic          string `mapstructure:"topic"`
	QueueGroup     string `mapstructure:"queue_group"`
	DLQTopic       string `mapstructure:"dlq_topic"`
	MaxInflight    int    `mapstructure:"max_inflight"`
	MaxPromptBytes int    `mapstructure:"max_prompt_bytes"`
}

type StreamConfig struct {
	Topic           string        `mapstructure:"topic"`
	FlushInterval   time.Duration `mapstructure:"flush_interval"`
	PublishTimeout  time.Duration `mapstructure:"publish_timeout"`
	MaxStreams      int           `mapstructure:"max_streams"`
	MaxPendingBytes int           `mapstructure:"max_pending_bytes"`
	MaxChunkBytes   int           `mapstructure:"max_chunk_bytes"`
}

type WorkerConfig struct {
	Count                int           `mapstructure:"count"`
	PollInterval         time.Duration `mapstructure:"poll_interval"`
	RecoveryInterval     time.Duration `mapstructure:"recovery_interval"`
	LeaseTTL             time.Duration `mapstructure:"lease_ttl"`
	HeartbeatInterval    time.Duration `mapstructure:"heartbeat_interval"`
	RunTimeout           time.Duration `mapstructure:"run_timeout"`
	RetryBackoff         time.Duration `mapstructure:"retry_backoff"`
	ShutdownDrainTimeout time.Duration `mapstructure:"shutdown_drain_timeout"`
	MaxAttempts          int           `mapstructure:"max_attempts"`
}

type RuntimeConfig struct {
	Mode                  string        `mapstructure:"mode"`
	SocketPath            string        `mapstructure:"socket_path"`
	RemoteMaxRequestBytes int           `mapstructure:"remote_max_request_bytes"`
	RemoteDialTimeout     time.Duration `mapstructure:"remote_dial_timeout"`
	Binary                string        `mapstructure:"binary"`
	ExpectedVersion       string        `mapstructure:"expected_version"`
	ExtensionPath         string        `mapstructure:"extension_path"`
	WorkDir               string        `mapstructure:"work_dir"`
	AgentDir              string        `mapstructure:"agent_dir"`
	ProviderEnvAllowlist  []string      `mapstructure:"provider_env_allowlist"`
	MaxFrameBytes         int           `mapstructure:"max_frame_bytes"`
	MaxOutputBytes        int64         `mapstructure:"max_output_bytes"`
	MaxStderrBytes        int           `mapstructure:"max_stderr_bytes"`
	EventQueueSize        int           `mapstructure:"event_queue_size"`
	StartupEventLimit     int           `mapstructure:"startup_event_limit"`
	EventOfferTimeout     time.Duration `mapstructure:"event_offer_timeout"`
	CommandTimeout        time.Duration `mapstructure:"command_timeout"`
	ProbeTimeout          time.Duration `mapstructure:"probe_timeout"`
	AbortGrace            time.Duration `mapstructure:"abort_grace"`
	TermGrace             time.Duration `mapstructure:"term_grace"`
	KillGrace             time.Duration `mapstructure:"kill_grace"`
}

type BrokerConfig struct {
	Address          string        `mapstructure:"address"`
	CapabilitySecret string        `mapstructure:"capability_secret"`
	CapabilityTTL    time.Duration `mapstructure:"capability_ttl"`
	MaxRequestBytes  int64         `mapstructure:"max_request_bytes"`
	MaxResponseBytes int           `mapstructure:"max_response_bytes"`
	RequestTimeout   time.Duration `mapstructure:"request_timeout"`
}

type SessionConfig struct {
	Root               string        `mapstructure:"root"`
	MaxSnapshotBytes   int64         `mapstructure:"max_snapshot_bytes"`
	RolloverBytes      int64         `mapstructure:"rollover_bytes"`
	RolloverEntryCount int64         `mapstructure:"rollover_entry_count"`
	GCInterval         time.Duration `mapstructure:"gc_interval"`
	GCGrace            time.Duration `mapstructure:"gc_grace"`
}

type ProfileConfig struct {
	ID                    string `mapstructure:"id"`
	Version               int64  `mapstructure:"version"`
	Provider              string `mapstructure:"provider"`
	Model                 string `mapstructure:"model"`
	SystemPrompt          string `mapstructure:"system_prompt"`
	BridgeVersion         string `mapstructure:"bridge_version"`
	MaxFinalBytes         int    `mapstructure:"max_final_bytes"`
	HistoryLimit          int    `mapstructure:"history_limit"`
	MaxHistoryPromptBytes int    `mapstructure:"max_history_prompt_bytes"`
	MaxProviderCalls      int    `mapstructure:"max_provider_calls"`
}

// BudgetConfig 只定义准入策略模式；具体租户额度和每 Attempt 上限来自
// PostgreSQL 的版本化 Policy。当前唯一允许的模式是缺失 Policy 即拒绝。
type BudgetConfig struct {
	PolicyMode string `mapstructure:"policy_mode"`
}

type ServiceAuthConfig struct {
	ServiceID string `mapstructure:"service_id"`
	Secret    string `mapstructure:"secret"`
}

type MutationConfig struct {
	ApprovalTopic  string        `mapstructure:"approval_topic"`
	QueueGroup     string        `mapstructure:"queue_group"`
	ApprovalTTL    time.Duration `mapstructure:"approval_ttl"`
	ReconcileEvery time.Duration `mapstructure:"reconcile_every"`
	BatchSize      int           `mapstructure:"batch_size"`
	MaxInflight    int           `mapstructure:"max_inflight"`
}

func Load() (*Config, error) {
	loader, err := genesisconfig.New(&genesisconfig.Config{
		Name: "pilot", FileType: "yaml", Paths: []string{"./configs"}, EnvPrefix: "RESONANCE",
	})
	if err != nil {
		return nil, err
	}
	if err := loader.Load(context.Background()); err != nil {
		return nil, errors.Join(err, loader.Close())
	}
	var cfg Config
	if err := loader.Unmarshal(&cfg); err != nil {
		return nil, errors.Join(err, loader.Close())
	}
	if err := loader.Close(); err != nil {
		return nil, fmt.Errorf("close pilot config loader: %w", err)
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if os.Getenv("DEBUG_CONFIG") == "true" || os.Getenv("RESONANCE_DEBUG_CONFIG") == "true" {
		dumpConfig(&cfg)
	}
	return &cfg, nil
}

func (c *Config) setDefaults() {
	if c.Service.Name == "" {
		c.Service.Name = "pilot-service"
	}
	if c.Service.HTTPPort == 0 {
		c.Service.HTTPPort = 15093
	}
	if c.LogicServiceName == "" {
		c.LogicServiceName = "logic-service"
	}
	if c.Registry.Namespace == "" {
		c.Registry.Namespace = "/resonance/services"
	}
	if c.Registry.DefaultTTL == 0 {
		c.Registry.DefaultTTL = 30 * time.Second
	}
	if c.Ingress.Topic == "" {
		c.Ingress.Topic = "resonance.chat.event.v1"
	}
	if c.Ingress.QueueGroup == "" {
		c.Ingress.QueueGroup = "resonance_group_agent_ingress_" + profileQueueToken(c.Profile.ID)
	}
	if c.Ingress.DLQTopic == "" {
		c.Ingress.DLQTopic = c.Ingress.Topic + ".agent.dlq"
	}
	if c.Ingress.MaxInflight == 0 {
		c.Ingress.MaxInflight = 32
	}
	if c.Ingress.MaxPromptBytes == 0 {
		c.Ingress.MaxPromptBytes = 32 << 10
	}
	if c.Stream.Topic == "" {
		c.Stream.Topic = "resonance.agent.stream.v1"
	}
	if c.Stream.FlushInterval == 0 {
		c.Stream.FlushInterval = 50 * time.Millisecond
	}
	if c.Stream.PublishTimeout == 0 {
		c.Stream.PublishTimeout = 2 * time.Second
	}
	if c.Stream.MaxStreams == 0 {
		c.Stream.MaxStreams = 128
	}
	if c.Stream.MaxPendingBytes == 0 {
		c.Stream.MaxPendingBytes = 256 << 10
	}
	if c.Stream.MaxChunkBytes == 0 {
		c.Stream.MaxChunkBytes = 16 << 10
	}
	if c.Worker.Count == 0 {
		c.Worker.Count = 4
	}
	if c.Worker.PollInterval == 0 {
		c.Worker.PollInterval = 250 * time.Millisecond
	}
	if c.Worker.RecoveryInterval == 0 {
		c.Worker.RecoveryInterval = 15 * time.Second
	}
	if c.Worker.LeaseTTL == 0 {
		c.Worker.LeaseTTL = 30 * time.Second
	}
	if c.Worker.HeartbeatInterval == 0 {
		c.Worker.HeartbeatInterval = 5 * time.Second
	}
	if c.Worker.RunTimeout == 0 {
		c.Worker.RunTimeout = 10 * time.Minute
	}
	if c.Worker.RetryBackoff == 0 {
		c.Worker.RetryBackoff = 5 * time.Second
	}
	if c.Worker.ShutdownDrainTimeout == 0 {
		c.Worker.ShutdownDrainTimeout = 30 * time.Second
	}
	if c.Worker.MaxAttempts == 0 {
		c.Worker.MaxAttempts = 3
	}
	if c.Runtime.Mode == "" {
		c.Runtime.Mode = RuntimeModeRemote
	}
	if c.Runtime.SocketPath == "" {
		c.Runtime.SocketPath = "/run/resonance-agent/runtime.sock"
	}
	if c.Runtime.RemoteMaxRequestBytes == 0 {
		c.Runtime.RemoteMaxRequestBytes = 1 << 20
	}
	if c.Runtime.RemoteDialTimeout == 0 {
		c.Runtime.RemoteDialTimeout = 2 * time.Second
	}
	if c.Broker.CapabilityTTL == 0 {
		c.Broker.CapabilityTTL = 15 * time.Minute
	}
	if c.Broker.MaxRequestBytes == 0 {
		c.Broker.MaxRequestBytes = 64 << 10
	}
	if c.Broker.MaxResponseBytes == 0 {
		c.Broker.MaxResponseBytes = 64 << 10
	}
	if c.Broker.RequestTimeout == 0 {
		c.Broker.RequestTimeout = 5 * time.Second
	}
	if c.Session.MaxSnapshotBytes == 0 {
		c.Session.MaxSnapshotBytes = 64 << 20
	}
	if c.Session.RolloverBytes == 0 {
		c.Session.RolloverBytes = 32 << 20
		if c.Session.RolloverBytes > c.Session.MaxSnapshotBytes {
			c.Session.RolloverBytes = max(c.Session.MaxSnapshotBytes/2, 1)
		}
	}
	if c.Session.RolloverEntryCount == 0 {
		c.Session.RolloverEntryCount = 20_000
	}
	if c.Session.GCInterval == 0 {
		c.Session.GCInterval = 30 * time.Minute
	}
	if c.Session.GCGrace == 0 {
		c.Session.GCGrace = 24 * time.Hour
	}
	if c.Profile.MaxFinalBytes == 0 {
		c.Profile.MaxFinalBytes = 64 << 10
	}
	if c.Profile.HistoryLimit == 0 {
		c.Profile.HistoryLimit = 100
	}
	if c.Profile.MaxHistoryPromptBytes == 0 {
		c.Profile.MaxHistoryPromptBytes = 256 << 10
	}
	if c.Profile.MaxProviderCalls == 0 {
		c.Profile.MaxProviderCalls = 8
	}
	if c.Budget.PolicyMode == "" {
		c.Budget.PolicyMode = "require_explicit"
	}
	if c.Observability.Metrics.Port == 0 {
		c.Observability.Metrics.Port = 9093
	}
	if c.Observability.Metrics.Path == "" {
		c.Observability.Metrics.Path = "/metrics"
	}
	if c.Mutation.ApprovalTopic == "" {
		c.Mutation.ApprovalTopic = "resonance.agent.approval.decided.v1"
	}
	if c.Mutation.QueueGroup == "" {
		c.Mutation.QueueGroup = "resonance_group_agent_iam_mutation"
	}
	if c.Mutation.ApprovalTTL == 0 {
		c.Mutation.ApprovalTTL = 15 * time.Minute
	}
	if c.Mutation.ReconcileEvery == 0 {
		c.Mutation.ReconcileEvery = 10 * time.Second
	}
	if c.Mutation.BatchSize == 0 {
		c.Mutation.BatchSize = 50
	}
	if c.Mutation.MaxInflight == 0 {
		c.Mutation.MaxInflight = 16
	}
}

func (c *Config) Validate() error {
	if c.Service.Name == "" || c.Service.HTTPPort < 1 || c.Service.HTTPPort > 65535 || c.TenantID == "" ||
		c.AgentBot == "" || c.LogicServiceName == "" {
		return fmt.Errorf("pilot service identity configuration is incomplete")
	}
	if c.Ingress.Topic == "" || c.Ingress.QueueGroup == "" || c.Ingress.DLQTopic == "" ||
		c.Ingress.MaxInflight < 1 || c.Ingress.MaxPromptBytes < 1 {
		return fmt.Errorf("pilot ingress configuration is invalid")
	}
	if c.Ingress.QueueGroup == "resonance_group_chat_event" {
		return fmt.Errorf("pilot ingress must not share the Task queue group")
	}
	if c.Mutation.ApprovalTopic == "" || c.Mutation.QueueGroup == "" || c.Mutation.ApprovalTTL <= 0 ||
		c.Mutation.ReconcileEvery <= 0 || c.Mutation.BatchSize < 1 || c.Mutation.BatchSize > 100 || c.Mutation.MaxInflight < 1 {
		return fmt.Errorf("pilot IAM mutation configuration is invalid")
	}
	if token := profileQueueToken(c.Profile.ID); token == "" || !strings.Contains(c.Ingress.QueueGroup, token) {
		return fmt.Errorf("pilot ingress queue group must be profile-specific")
	}
	if c.Stream.Topic == "" || c.Stream.Topic == c.Ingress.Topic || c.Stream.FlushInterval <= 0 ||
		c.Stream.PublishTimeout <= 0 || c.Stream.MaxStreams < c.Worker.Count || c.Stream.MaxPendingBytes < 1 ||
		c.Stream.MaxChunkBytes < 1 || c.Stream.MaxChunkBytes > c.Stream.MaxPendingBytes {
		return fmt.Errorf("pilot stream configuration is invalid")
	}
	if c.Worker.Count < 1 || c.Worker.PollInterval <= 0 || c.Worker.RecoveryInterval <= 0 ||
		c.Worker.LeaseTTL < 3*c.Worker.HeartbeatInterval || c.Worker.HeartbeatInterval <= 0 ||
		c.Worker.RunTimeout <= 0 || c.Worker.RetryBackoff < 0 || c.Worker.ShutdownDrainTimeout <= 0 ||
		c.Worker.MaxAttempts < 1 || c.Worker.MaxAttempts > 100 {
		return fmt.Errorf("pilot worker timing configuration is invalid")
	}
	if c.Runtime.ExpectedVersion == "" {
		return fmt.Errorf("pilot runtime pinned version is required")
	}
	switch c.Runtime.Mode {
	case RuntimeModeLocal:
		if c.Runtime.Binary == "" || !filepath.IsAbs(c.Runtime.Binary) ||
			c.Runtime.ExtensionPath == "" || !filepath.IsAbs(c.Runtime.ExtensionPath) ||
			c.Runtime.WorkDir == "" || !filepath.IsAbs(c.Runtime.WorkDir) ||
			c.Runtime.AgentDir == "" || !filepath.IsAbs(c.Runtime.AgentDir) || filepath.Clean(c.Runtime.AgentDir) != c.Runtime.AgentDir {
			return fmt.Errorf("local pilot runtime paths are required")
		}
		if err := validateProviderEnvironment(c.Runtime.ProviderEnvAllowlist); err != nil {
			return err
		}
	case RuntimeModeRemote:
		if !validPrivateSocketPath(c.Runtime.SocketPath) || c.Runtime.RemoteMaxRequestBytes < 1 ||
			c.Runtime.RemoteMaxRequestBytes > 8<<20 || c.Runtime.RemoteDialTimeout <= 0 {
			return fmt.Errorf("remote pilot runtime configuration is invalid")
		}
	default:
		return fmt.Errorf("pilot runtime mode must be local or remote")
	}
	if c.Broker.Address == "" || len(c.Broker.CapabilitySecret) < 32 || c.Broker.CapabilityTTL < c.Worker.RunTimeout ||
		c.Broker.CapabilityTTL > time.Hour || c.Broker.MaxRequestBytes < 1 || c.Broker.MaxResponseBytes < 1 ||
		c.Broker.RequestTimeout <= 0 {
		return fmt.Errorf("pilot tool broker configuration is invalid")
	}
	brokerPort, brokerIsUnix, err := validateBrokerAddress(c.Broker.Address)
	if err != nil || (c.Runtime.Mode == RuntimeModeRemote) != brokerIsUnix {
		return fmt.Errorf("pilot tool broker must use a private Unix socket for remote runtime and loopback TCP for local runtime")
	}
	if c.Session.Root == "" || !filepath.IsAbs(c.Session.Root) || c.Session.MaxSnapshotBytes < 1 ||
		c.Session.RolloverBytes < 1 || c.Session.RolloverBytes > c.Session.MaxSnapshotBytes ||
		c.Session.RolloverEntryCount < 1 ||
		c.Session.GCInterval <= 0 || c.Session.GCGrace < 2*c.Worker.RunTimeout {
		return fmt.Errorf("pilot session store configuration is invalid")
	}
	if c.Profile.ID == "" || c.Profile.Version < 1 || c.Profile.Provider == "" || c.Profile.Model == "" ||
		strings.TrimSpace(c.Profile.SystemPrompt) == "" || c.Profile.BridgeVersion == "" || c.Profile.MaxFinalBytes < 1 ||
		c.Profile.HistoryLimit < 1 || c.Profile.HistoryLimit > 100 || c.Profile.MaxHistoryPromptBytes < 1 ||
		c.Profile.MaxProviderCalls < 1 || c.Profile.MaxProviderCalls > 128 {
		return fmt.Errorf("pilot profile snapshot is incomplete")
	}
	if c.Budget.PolicyMode != "require_explicit" {
		return fmt.Errorf("pilot budget policy mode must fail closed with require_explicit")
	}
	if c.ServiceAuth.ServiceID == "" || len(c.ServiceAuth.Secret) < 32 {
		return fmt.Errorf("pilot service authentication is required")
	}
	if c.Observability.Trace.Sampler < 0 || c.Observability.Trace.Sampler > 1 ||
		c.Observability.Metrics.Port < 1 || c.Observability.Metrics.Port > 65535 ||
		!strings.HasPrefix(c.Observability.Metrics.Path, "/") ||
		c.Observability.Metrics.Port == c.Service.HTTPPort || (brokerPort != 0 && c.Observability.Metrics.Port == brokerPort) {
		return fmt.Errorf("pilot observability configuration is invalid or conflicts with another listener")
	}
	return nil
}

const (
	RuntimeModeLocal  = "local"
	RuntimeModeRemote = "remote"
)

func validPrivateSocketPath(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path && len(path) <= 240
}

func validateBrokerAddress(address string) (port int, unix bool, err error) {
	if after, ok := strings.CutPrefix(address, "unix://"); ok {
		if !validPrivateSocketPath(after) {
			return 0, true, fmt.Errorf("invalid broker Unix socket")
		}
		return 0, true, nil
	}
	host, rawPort, splitErr := net.SplitHostPort(address)
	if splitErr != nil || rawPort == "" || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return 0, false, fmt.Errorf("invalid broker loopback address")
	}
	parsed, parseErr := strconv.Atoi(rawPort)
	if parseErr != nil || parsed < 1 || parsed > 65535 || rawPort != strconv.Itoa(parsed) {
		return 0, false, fmt.Errorf("invalid broker port")
	}
	return parsed, false, nil
}

func profileQueueToken(profileID string) string {
	return strings.ReplaceAll(strings.TrimSpace(profileID), "-", "_")
}

func validateProviderEnvironment(names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if !environmentNamePattern.MatchString(name) || strings.HasPrefix(name, "RESONANCE_") ||
			slices.Contains([]string{"HOME", "NODE_OPTIONS", "NODE_PATH", "PI_CODING_AGENT_DIR", "DATABASE_URL", "NATS_URL"}, name) {
			return fmt.Errorf("runtime provider environment name %q is forbidden", name)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("runtime provider environment name %q is duplicated", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func (c *Config) RuntimeEnvironment() ([]string, error) {
	if c.Runtime.Mode != RuntimeModeLocal {
		return nil, fmt.Errorf("provider environment is only available to an in-process local runtime")
	}
	environment := make([]string, 0, len(c.Runtime.ProviderEnvAllowlist))
	for _, name := range c.Runtime.ProviderEnvAllowlist {
		value, ok := os.LookupEnv(name)
		if !ok || value == "" {
			return nil, fmt.Errorf("required runtime provider environment %s is not set", name)
		}
		environment = append(environment, name+"="+value)
	}
	return environment, nil
}

func (c *Config) HTTPAddr() string { return fmt.Sprintf(":%d", c.Service.HTTPPort) }
func (c *Config) ToolBrokerURL() string {
	if strings.HasPrefix(c.Broker.Address, "unix://") {
		return ""
	}
	return "http://" + c.Broker.Address
}

func dumpConfig(cfg *Config) {
	payload, err := sanitizedConfigJSON(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n=== Pilot Configuration ===\n<unavailable: %v>\n=== End of Configuration ===\n\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "\n=== Pilot Configuration ===\n%s\n=== End of Configuration ===\n\n", payload)
}

func sanitizedConfigJSON(cfg *Config) ([]byte, error) {
	sanitized := *cfg
	sanitized.PostgreSQL.Password = redacted(cfg.PostgreSQL.Password)
	sanitized.NATS.Password = redacted(cfg.NATS.Password)
	sanitized.Etcd.Password = redacted(cfg.Etcd.Password)
	sanitized.Broker.CapabilitySecret = redacted(cfg.Broker.CapabilitySecret)
	sanitized.ServiceAuth.Secret = redacted(cfg.ServiceAuth.Secret)
	return json.MarshalIndent(sanitized, "", "  ")
}

func redacted(value string) string {
	if value == "" {
		return ""
	}
	return "***configured***"
}
