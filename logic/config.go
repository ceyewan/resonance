package logic

import (
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/config"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/registry"
)

// Config Logic 服务配置
type Config struct {
	// 服务基础配置
	Service struct {
		Name       string `mapstructure:"name"`        // 服务名称
		ServerAddr string `mapstructure:"server_addr"` // gRPC 服务地址
	} `mapstructure:"service"`

	// 基础组件配置
	Log   clog.Config           `mapstructure:"log"`   // 日志配置
	MySQL connector.MySQLConfig `mapstructure:"mysql"` // MySQL 配置
	Redis connector.RedisConfig `mapstructure:"redis"` // Redis 配置
	NATS  connector.NATSConfig  `mapstructure:"nats"`  // NATS 配置
	Etcd  connector.EtcdConfig  `mapstructure:"etcd"`  // Etcd 配置

	// 服务注册发现配置
	Registry RegistryConfig `mapstructure:"registry"`

	// ID 生成器配置
	IDGen idgen.SnowflakeConfig `mapstructure:"idgen"` // Snowflake ID 生成器配置
}

// RegistryConfig 服务注册配置
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

// Load 创建并加载 Logic 配置（无参数）
// 配置加载顺序：环境变量 > .env > logic.{env}.yaml > logic.yaml
func Load() (*Config, error) {
	loader := config.MustLoad(
		config.WithConfigName("logic"),
		config.WithConfigPaths("./configs"),
		config.WithEnvPrefix("RESONANCE"),
	)

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
