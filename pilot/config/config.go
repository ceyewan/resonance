package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ceyewan/genesis/clog"
	genesisconfig "github.com/ceyewan/genesis/config"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/mq"
	"github.com/ceyewan/genesis/registry"

	pilotobservability "github.com/ceyewan/resonance/pilot/observability"
)

type Config struct {
	Service struct {
		Name     string `mapstructure:"name"`
		HTTPPort int    `mapstructure:"http_port"`
	} `mapstructure:"service"`
	AgentBot         string                     `mapstructure:"agent_bot"`
	LogicServiceName string                     `mapstructure:"logic_service_name"`
	Log              clog.Config                `mapstructure:"log"`
	PostgreSQL       connector.PostgreSQLConfig `mapstructure:"postgres"`
	NATS             connector.NATSConfig       `mapstructure:"nats"`
	Etcd             connector.EtcdConfig       `mapstructure:"etcd"`
	Registry         RegistryConfig             `mapstructure:"registry"`
	JetStream        mq.JetStreamConfig         `mapstructure:"jetstream"`
	Ingress          IngressConfig              `mapstructure:"ingress"`
	Stream           StreamConfig               `mapstructure:"stream"`
	Worker           WorkerConfig               `mapstructure:"worker"`
	Runtime          RuntimeConfig              `mapstructure:"runtime"`
	Session          SessionConfig              `mapstructure:"session"`
	Assistant        AssistantConfig            `mapstructure:"assistant"`
	ServiceAuth      ServiceAuthConfig          `mapstructure:"service_auth"`
	Observability    pilotobservability.Config  `mapstructure:"observability"`
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
	Binary          string `mapstructure:"binary"`
	ExpectedVersion string `mapstructure:"expected_version"`
	ExtensionPath   string `mapstructure:"extension_path"`
	WorkDir         string `mapstructure:"work_dir"`
	AgentDir        string `mapstructure:"agent_dir"`
}
type SessionConfig struct {
	Root               string        `mapstructure:"root"`
	MaxSnapshotBytes   int64         `mapstructure:"max_snapshot_bytes"`
	RolloverBytes      int64         `mapstructure:"rollover_bytes"`
	RolloverEntryCount int64         `mapstructure:"rollover_entry_count"`
	GCInterval         time.Duration `mapstructure:"gc_interval"`
	GCGrace            time.Duration `mapstructure:"gc_grace"`
}
type AssistantConfig struct {
	Provider              string `mapstructure:"provider"`
	Model                 string `mapstructure:"model"`
	SystemPrompt          string `mapstructure:"system_prompt"`
	MaxOutputTokens       int64  `mapstructure:"max_output_tokens"`
	MaxFinalBytes         int    `mapstructure:"max_final_bytes"`
	HistoryLimit          int    `mapstructure:"history_limit"`
	MaxHistoryPromptBytes int    `mapstructure:"max_history_prompt_bytes"`
}
type ServiceAuthConfig struct {
	ServiceID string `mapstructure:"service_id"`
	Secret    string `mapstructure:"secret"`
}

func Load() (*Config, error) {
	loader, err := genesisconfig.New(&genesisconfig.Config{Name: "pilot", FileType: "yaml", Paths: []string{"./configs"}, EnvPrefix: "RESONANCE"})
	if err != nil {
		return nil, err
	}
	if err = loader.Load(context.Background()); err != nil {
		return nil, errors.Join(err, loader.Close())
	}
	var c Config
	if err = loader.Unmarshal(&c); err != nil {
		return nil, errors.Join(err, loader.Close())
	}
	if err = loader.Close(); err != nil {
		return nil, err
	}
	if c.AgentBot == "" || c.Assistant.Provider != "dashscope" || c.Assistant.Model == "" || c.Assistant.SystemPrompt == "" || c.Assistant.MaxOutputTokens < 1 || c.Assistant.MaxOutputTokens > 32768 {
		return nil, fmt.Errorf("pilot assistant configuration is incomplete")
	}
	if c.ServiceAuth.ServiceID == "" || len(c.ServiceAuth.Secret) < 32 {
		return nil, fmt.Errorf("pilot requires a separate service signing secret of at least 32 bytes")
	}
	if !filepath.IsAbs(c.Runtime.Binary) || !filepath.IsAbs(c.Runtime.ExtensionPath) || !filepath.IsAbs(c.Runtime.WorkDir) || !filepath.IsAbs(c.Runtime.AgentDir) || c.Runtime.ExpectedVersion == "" {
		return nil, fmt.Errorf("pi requires absolute paths and a pinned version")
	}
	if c.Worker.Count < 1 || c.Worker.MaxAttempts < 1 || c.Worker.ShutdownDrainTimeout <= 0 {
		return nil, fmt.Errorf("pilot worker limits are invalid")
	}
	return &c, nil
}
func (c *Config) HTTPAddr() string { return fmt.Sprintf(":%d", c.Service.HTTPPort) }

// Deliberately exclude all database, service signing and host configuration from Pi.
func (c *Config) RuntimeEnvironment() ([]string, error) {
	result := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + c.Runtime.AgentDir, "PI_OFFLINE=1"}
	for _, name := range []string{"DASHSCOPE_API_KEY", "DASHSCOPE_BASE_URL", "DASHSCOPE_MODEL"} {
		value := os.Getenv(name)
		if value == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
		if name == "DASHSCOPE_MODEL" && value != c.Assistant.Model {
			return nil, fmt.Errorf("pilot and Pi model configuration must match")
		}
		result = append(result, name+"="+value)
	}
	return result, nil
}
