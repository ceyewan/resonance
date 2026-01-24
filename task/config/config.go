package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/config"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/registry"
)

// Config Task 服务配置
type Config struct {
	// 服务基础配置
	Service struct {
		Name string `mapstructure:"name"` // 服务名称
	} `mapstructure:"service"`

	// 基础组件配置
	Log   clog.Config           `mapstructure:"log"`   // 日志配置
	MySQL connector.MySQLConfig `mapstructure:"mysql"` // MySQL 配置
	Redis connector.RedisConfig `mapstructure:"redis"` // Redis 配置
	NATS  connector.NATSConfig  `mapstructure:"nats"`  // NATS 配置
	Etcd  connector.EtcdConfig  `mapstructure:"etcd"`  // Etcd 配置

	// 服务注册发现配置
	Registry RegistryConfig `mapstructure:"registry"`

	// Gateway 服务配置
	GatewayServiceName string `mapstructure:"gateway_service_name"` // Gateway 服务名称

	// 消费者配置
	StorageConsumer ConsumerConfig `mapstructure:"storage_consumer"` // 存储任务消费者
	PushConsumer    ConsumerConfig `mapstructure:"push_consumer"`    // 推送任务消费者
}

// RegistryConfig Registry 配置
type RegistryConfig struct {
	Namespace       string        `mapstructure:"namespace"`        // 服务命名空间
	DefaultTTL      time.Duration `mapstructure:"default_ttl"`      // 默认租约
	EnableCache     bool          `mapstructure:"enable_cache"`     // 是否启用缓存
	CacheExpiration time.Duration `mapstructure:"cache_expiration"` // 缓存过期时间
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
}

// Load 创建并加载 Task 配置（无参数）
// 配置加载顺序：环境变量 > .env > task.{env}.yaml > task.yaml
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
		return nil, err
	}

	var cfg Config
	if err := loader.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	// 设置默认值
	if cfg.StorageConsumer.WorkerCount <= 0 {
		cfg.StorageConsumer.WorkerCount = 20
	}
	if cfg.StorageConsumer.QueueGroup == "" {
		cfg.StorageConsumer.QueueGroup = "resonance_group_storage"
	}
	if cfg.PushConsumer.WorkerCount <= 0 {
		cfg.PushConsumer.WorkerCount = 50
	}
	if cfg.PushConsumer.QueueGroup == "" {
		cfg.PushConsumer.QueueGroup = "resonance_group_push"
	}
	// 继承 Topic 配置如果未设置（通常两个组订阅同一个 Topic）
	if cfg.StorageConsumer.Topic == "" && cfg.PushConsumer.Topic != "" {
		cfg.StorageConsumer.Topic = cfg.PushConsumer.Topic
	} else if cfg.PushConsumer.Topic == "" && cfg.StorageConsumer.Topic != "" {
		cfg.PushConsumer.Topic = cfg.StorageConsumer.Topic
	}

	// 在 debug 模式下，打印最终生效的配置
	if os.Getenv("DEBUG_CONFIG") == "true" || os.Getenv("RESONANCE_DEBUG_CONFIG") == "true" {
		dumpConfig(&cfg)
	}

	return &cfg, nil
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
	if sanitized.MySQL.Password != "" {
		sanitized.MySQL.Password = "***"
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
