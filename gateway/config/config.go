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

// Config Gateway 服务配置
type Config struct {
	// 服务基础配置
	Service struct {
		Name     string `mapstructure:"name"`      // 服务名称
		Host     string `mapstructure:"host"`      // 服务主机名（环境变量 HOSTNAME）
		HTTPPort int    `mapstructure:"http_port"` // HTTP 服务端口
		GRPCPort int    `mapstructure:"grpc_port"` // gRPC 服务端口
	} `mapstructure:"service"`

	// Logic 服务名称（用于服务发现）
	LogicServiceName string `mapstructure:"logic_service_name"` // Logic 服务名称，默认 "logic-service"

	// 基础组件配置
	Log   clog.Config           `mapstructure:"log"`   // 日志配置
	Redis connector.RedisConfig `mapstructure:"redis"` // Redis 配置
	Etcd  connector.EtcdConfig  `mapstructure:"etcd"`  // Etcd 配置

	// 服务注册发现配置
	Registry RegistryConfig `mapstructure:"registry"`

	// WebSocket 配置
	WSConfig WSConfig `mapstructure:"ws_config"`

	// WorkerID 配置
	WorkerID WorkerIDConfig `mapstructure:"worker_id"`
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

// WorkerIDConfig WorkerID 分发配置
type WorkerIDConfig struct {
	MaxID int    `mapstructure:"max_id"` // 最大 ID 范围 [0, max_id)
	Key   string `mapstructure:"key"`    // Redis 中用于分配 workerID 的键前缀
}

// GetMaxID 获取最大 ID，默认 1024
func (c *WorkerIDConfig) GetMaxID() int {
	if c.MaxID <= 0 {
		return 1024
	}
	return c.MaxID
}

// GetKey 获取 Redis 键，默认 "resonance:gateway:worker"
func (c *WorkerIDConfig) GetKey() string {
	if c.Key == "" {
		return "resonance:gateway:worker"
	}
	return c.Key
}

// GetHost 获取服务主机名，优先使用配置，其次环境变量 HOSTNAME，最后 "localhost"
func (c *Config) GetHost() string {
	if c.Service.Host != "" {
		return c.Service.Host
	}
	if host := os.Getenv("HOSTNAME"); host != "" {
		return host
	}
	return "localhost"
}

// GetHTTPPort 获取 HTTP 端口
func (c *Config) GetHTTPPort() int {
	if c.Service.HTTPPort > 0 && c.Service.HTTPPort < 65536 {
		return c.Service.HTTPPort
	}
	return 8080
}

// GetGRPCPort 获取 gRPC 端口
func (c *Config) GetGRPCPort() int {
	if c.Service.GRPCPort > 0 {
		return c.Service.GRPCPort
	}
	return 15091
}

// GetHTTPAddr 获取 HTTP 绑定地址
func (c *Config) GetHTTPAddr() string {
	return fmt.Sprintf(":%d", c.GetHTTPPort())
}

// GetLogicServiceName 获取 Logic 服务名称
func (c *Config) GetLogicServiceName() string {
	if c.LogicServiceName != "" {
		return c.LogicServiceName
	}
	return "logic-service"
}

// Load 创建并加载 Gateway 配置（无参数）
// 配置加载顺序：环境变量 > .env > gateway.{env}.yaml > gateway.yaml
func Load() (*Config, error) {
	loader, err := config.New(&config.Config{
		Name:     "gateway",
		FileType: "yaml",
	},
		config.WithConfigName("gateway"),
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

	// 在 debug 模式下，打印最终生效的配置
	if os.Getenv("DEBUG_CONFIG") == "true" || os.Getenv("RESONANCE_DEBUG_CONFIG") == "true" {
		dumpConfig(&cfg)
	}

	return &cfg, nil
}

// dumpConfig 以 JSON 格式打印配置（脱敏敏感字段）
func dumpConfig(cfg *Config) {
	// 创建配置副本用于脱敏
	sanitized := *cfg
	if sanitized.Redis.Password != "" {
		sanitized.Redis.Password = "***"
	}

	data, _ := json.MarshalIndent(sanitized, "", "  ")
	fmt.Fprintf(os.Stderr, "\n=== Gateway Configuration ===\n%s\n=== End of Configuration ===\n\n", data)
}
