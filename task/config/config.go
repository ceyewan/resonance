package config

import (
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
	ConsumerConfig ConsumerConfig `mapstructure:"consumer"`
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
		Namespace:   c.Namespace,
		DefaultTTL:  c.DefaultTTL,
		EnableCache: c.EnableCache,
	}

	if c.CacheExpiration > 0 {
		cfg.CacheExpiration = c.CacheExpiration
	}

	// 设置默认值
	if cfg.Namespace == "" {
		cfg.Namespace = "/resonance/services"
	}
	if cfg.DefaultTTL == 0 {
		cfg.DefaultTTL = 30 * time.Second
	}
	if cfg.CacheExpiration == 0 {
		cfg.CacheExpiration = 10 * time.Second
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
		Name:     "task",
		FileType: "yaml",
	},
		config.WithConfigPaths("./configs"),
		config.WithEnvPrefix("RESONANCE"),
	)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := loader.Unmarshal(&cfg); err != nil {
		return nil, err
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
