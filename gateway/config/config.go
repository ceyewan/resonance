package config

import (
	"context"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/config"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/registry"
)

// Config Gateway 服务配置
type Config struct {
	// 服务基础配置
	Service struct {
		Name     string `mapstructure:"name"`      // 服务名称
		HTTPAddr string `mapstructure:"http_addr"` // HTTP 服务地址
		WSAddr   string `mapstructure:"ws_addr"`   // WebSocket 服务地址
	} `mapstructure:"service"`

	// Logic 服务地址
	LogicAddr string `mapstructure:"logic_addr"` // Logic gRPC 服务地址

	// 基础组件配置
	Log   clog.Config           `mapstructure:"log"`   // 日志配置
	Redis connector.RedisConfig `mapstructure:"redis"` // Redis 配置
	NATS  connector.NATSConfig  `mapstructure:"nats"`  // NATS 配置
	Etcd  connector.EtcdConfig  `mapstructure:"etcd"`  // Etcd 配置

	// 服务注册发现配置
	Registry RegistryConfig `mapstructure:"registry"`

	// WebSocket 配置
	WSConfig WSConfig `mapstructure:"ws_config"`
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

// WSConfig WebSocket 相关配置
type WSConfig struct {
	ReadBufferSize  int `mapstructure:"read_buffer_size"`  // 读缓冲区大小
	WriteBufferSize int `mapstructure:"write_buffer_size"` // 写缓冲区大小
	MaxMessageSize  int `mapstructure:"max_message_size"`  // 最大消息大小
	PingInterval    int `mapstructure:"ping_interval"`     // 心跳间隔（秒）
	PongTimeout     int `mapstructure:"pong_timeout"`      // 心跳超时（秒）
}

// Load 创建并加载 Gateway 配置（无参数）
// 配置加载顺序：环境变量 > .env > gateway.{env}.yaml > gateway.yaml
func Load() (*Config, error) {
	loader, err := config.New(&config.Config{
		Name:     "gateway",
		FileType: "yaml",
	},
		config.WithConfigPaths("./configs"),
		config.WithEnvPrefix("RESONANCE"),
	)
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
