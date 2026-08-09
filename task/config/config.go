package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/config"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/mq"
	"github.com/ceyewan/genesis/registry"

	"github.com/ceyewan/resonance/task/observability"
)

// Config Task 服务配置
type Config struct {
	// 服务基础配置
	Service struct {
		Name     string `mapstructure:"name"`      // 服务名称
		HTTPPort int    `mapstructure:"http_port"` // HTTP 健康检查端口（0=禁用）
	} `mapstructure:"service"`

	// 基础组件配置
	Log        clog.Config                `mapstructure:"log"`      // 日志配置
	PostgreSQL connector.PostgreSQLConfig `mapstructure:"postgres"` // PostgreSQL 配置
	Redis      connector.RedisConfig      `mapstructure:"redis"`    // Redis 配置
	NATS       connector.NATSConfig       `mapstructure:"nats"`     // NATS 配置
	Etcd       connector.EtcdConfig       `mapstructure:"etcd"`     // Etcd 配置
	JetStream  mq.JetStreamConfig         `mapstructure:"jetstream"`

	// 服务注册发现配置
	Registry RegistryConfig `mapstructure:"registry"`

	// Gateway 服务配置
	GatewayServiceName string `mapstructure:"gateway_service_name"` // Gateway 服务名称
	GatewayQueueSize   int    `mapstructure:"gateway_queue_size"`   // 每个 Gateway 的推送队列大小
	GatewayPusherCount int    `mapstructure:"gateway_pusher_count"` // 每个 Gateway 的并发推送协程数

	// 消费者配置
	Consumer            ConsumerConfig `mapstructure:"consumer"` // 持久 ChatEvent：先存储后推送
	StreamConsumer      ConsumerConfig `mapstructure:"stream_consumer"`
	StreamMaxDeltaBytes int            `mapstructure:"stream_max_delta_bytes"`

	// 可观测性配置
	Observability struct {
		Version     string                      `mapstructure:"version"`
		InstanceID  string                      `mapstructure:"instance"`
		Environment string                      `mapstructure:"environment"`
		Trace       observability.TraceConfig   `mapstructure:"trace"`   // Trace 配置
		Metrics     observability.MetricsConfig `mapstructure:"metrics"` // Metrics 配置
	} `mapstructure:"observability"`
}

// RegistryConfig Registry 配置
type RegistryConfig struct {
	Namespace       string        `mapstructure:"namespace"`        // 服务命名空间
	DefaultTTL      time.Duration `mapstructure:"default_ttl"`      // 默认租约
	EnableCache     bool          `mapstructure:"enable_cache"`     // 是否启用缓存
	CacheExpiration time.Duration `mapstructure:"cache_expiration"` // 缓存过期时间
	PollInterval    time.Duration `mapstructure:"poll_interval"`    // 服务发现轮询间隔
}

// ToRegistryConfig 转换为 registry.Config
func (c *RegistryConfig) ToRegistryConfig() *registry.Config {
	cfg := &registry.Config{
		Namespace:  c.Namespace,
		DefaultTTL: c.DefaultTTL,
	}

	// 设置默认值
	if cfg.Namespace == "" {
		cfg.Namespace = "/resonance/services"
	}
	if cfg.DefaultTTL == 0 {
		cfg.DefaultTTL = 30 * time.Second
	}

	return cfg
}

// ConsumerConfig MQ 消费者配置
type ConsumerConfig struct {
	Topic         string `mapstructure:"topic"`          // 订阅的主题
	QueueGroup    string `mapstructure:"queue_group"`    // 队列组名称
	WorkerCount   int    `mapstructure:"worker_count"`   // 并发处理协程数
	MaxRetry      int    `mapstructure:"max_retry"`      // 最大重试次数
	RetryInterval int    `mapstructure:"retry_interval"` // 重试间隔（秒）
	DLQTopic      string `mapstructure:"dlq_topic"`      // 死信队列主题（无法解析的消息转投此处）
}

// GetHTTPAddr 获取 HTTP 健康检查地址，默认 :15092
func (c *Config) GetHTTPAddr() string {
	port := c.Service.HTTPPort
	if port <= 0 {
		port = 15092
	}
	return fmt.Sprintf(":%d", port)
}

// Load 创建并加载 Task 配置（无参数）
// 配置加载顺序：环境变量 > .env > task.yaml
func Load() (*Config, error) {
	loader, err := config.New(&config.Config{
		Name:      "task",
		FileType:  "yaml",
		Paths:     []string{"./configs"},
		EnvPrefix: "RESONANCE",
	})
	if err != nil {
		return nil, err
	}

	// 必须先 Load 才能读取配置
	ctx := context.Background()
	if err := loader.Load(ctx); err != nil {
		return nil, errors.Join(err, loader.Close())
	}

	var cfg Config
	if err := loader.Unmarshal(&cfg); err != nil {
		return nil, errors.Join(err, loader.Close())
	}
	if err := loader.Close(); err != nil {
		return nil, fmt.Errorf("close task config loader: %w", err)
	}

	// 设置默认值
	if cfg.Consumer.WorkerCount <= 0 {
		cfg.Consumer.WorkerCount = 20
	}
	if cfg.Consumer.QueueGroup == "" {
		cfg.Consumer.QueueGroup = "resonance_group_chat_event"
	}
	if cfg.Consumer.MaxRetry <= 0 {
		cfg.Consumer.MaxRetry = 3
	}
	if cfg.Consumer.RetryInterval <= 0 {
		cfg.Consumer.RetryInterval = 5
	}
	if cfg.Consumer.Topic == "" {
		cfg.Consumer.Topic = "resonance.chat.event.v1"
	}
	if cfg.Consumer.DLQTopic == "" {
		cfg.Consumer.DLQTopic = cfg.Consumer.Topic + ".dlq"
	}
	if cfg.StreamConsumer.WorkerCount <= 0 {
		cfg.StreamConsumer.WorkerCount = 10
	}
	if cfg.StreamConsumer.QueueGroup == "" {
		cfg.StreamConsumer.QueueGroup = "resonance_group_agent_stream"
	}
	if cfg.StreamConsumer.MaxRetry <= 0 {
		cfg.StreamConsumer.MaxRetry = 2
	}
	if cfg.StreamConsumer.Topic == "" {
		cfg.StreamConsumer.Topic = "resonance.agent.stream.v1"
	}
	if cfg.StreamConsumer.DLQTopic == "" {
		cfg.StreamConsumer.DLQTopic = cfg.StreamConsumer.Topic + ".dlq"
	}
	if cfg.StreamMaxDeltaBytes <= 0 {
		cfg.StreamMaxDeltaBytes = 64 << 10
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// 在 debug 模式下，打印最终生效的配置
	if os.Getenv("DEBUG_CONFIG") == "true" || os.Getenv("RESONANCE_DEBUG_CONFIG") == "true" {
		dumpConfig(&cfg)
	}

	return &cfg, nil
}

func (c *Config) Validate() error {
	if c.Consumer.Topic == "" || c.Consumer.QueueGroup == "" || c.Consumer.WorkerCount < 1 ||
		c.Consumer.MaxRetry < 1 || c.Consumer.RetryInterval < 0 || c.Consumer.DLQTopic == "" {
		return fmt.Errorf("task durable consumer configuration is invalid")
	}
	if c.StreamConsumer.Topic == "" || c.StreamConsumer.QueueGroup == "" || c.StreamConsumer.WorkerCount < 1 ||
		c.StreamConsumer.MaxRetry < 1 || c.StreamConsumer.RetryInterval < 0 || c.StreamConsumer.DLQTopic == "" ||
		c.StreamMaxDeltaBytes < 1 {
		return fmt.Errorf("task agent stream consumer configuration is invalid")
	}
	if c.StreamConsumer.Topic == c.Consumer.Topic || c.StreamConsumer.QueueGroup == c.Consumer.QueueGroup ||
		c.StreamConsumer.DLQTopic == c.Consumer.DLQTopic {
		return fmt.Errorf("task agent stream channel must be isolated from durable chat events")
	}
	return nil
}

// MustLoad 创建并加载配置，出错时 panic
func MustLoad() *Config {
	cfg, err := Load()
	if err != nil {
		panic(err)
	}
	return cfg
}

// dumpConfig 以 JSON 格式打印配置（脱敏敏感字段）
func dumpConfig(cfg *Config) {
	// 创建配置副本用于脱敏
	sanitized := *cfg
	if sanitized.PostgreSQL.Password != "" {
		sanitized.PostgreSQL.Password = "***"
	}
	if sanitized.Redis.Password != "" {
		sanitized.Redis.Password = "***"
	}
	if sanitized.NATS.Password != "" {
		sanitized.NATS.Password = "***"
	}
	if sanitized.Etcd.Password != "" {
		sanitized.Etcd.Password = "***"
	}

	data, _ := json.MarshalIndent(sanitized, "", "  ")
	fmt.Fprintf(os.Stderr, "\n=== Task Configuration ===\n%s\n=== End of Configuration ===\n\n", data)
}
