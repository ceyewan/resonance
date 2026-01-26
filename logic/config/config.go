package config

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/ceyewan/genesis/auth"
	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/config"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/registry"
)

// Config Logic 服务配置
type Config struct {
	// 服务基础配置
	Service struct {
		Name       string `mapstructure:"name"`        // 服务名称
		Host       string `mapstructure:"host"`        // 服务主机名
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

	// 认证配置
	Auth auth.Config `mapstructure:"auth"`

	// 管理员初始化配置
	Admin AdminConfig `mapstructure:"admin"`

	// WorkerID 配置
	WorkerID WorkerIDConfig `mapstructure:"worker_id"`

	// 可观测性配置
	Observability struct {
		Trace struct {
			Disable  bool    `mapstructure:"disable"`  // 是否禁用 Trace
			Endpoint string  `mapstructure:"endpoint"` // OTLP Collector 地址
			Insecure bool    `mapstructure:"insecure"` // 是否使用不安全连接
			Sampler  float64 `mapstructure:"sampler"`  // 采样率 (0.0-1.0)
		} `mapstructure:"trace"`
		Metrics struct {
			Port          int    `mapstructure:"port"`           // Prometheus 端口
			Path          string `mapstructure:"path"`           // Metrics 路径
			EnableRuntime bool   `mapstructure:"enable_runtime"` // 是否启用运行时指标
		} `mapstructure:"metrics"`
	} `mapstructure:"observability"`

	// Outbox 配置
	Outbox OutboxConfig `mapstructure:"outbox"`
}

// OutboxConfig Outbox Job 配置
type OutboxConfig struct {
	BatchSize   int           `mapstructure:"batch_size"`   // 每次处理的消息批次大小
	MaxRetries  int           `mapstructure:"max_retries"`  // 最大重试次数
	TickerTime  time.Duration `mapstructure:"ticker_time"`  // 扫描间隔
	WorkerCount int           `mapstructure:"worker_count"` // 并发处理的 Worker 数量
}

// GetBatchSize 获取批次大小，默认 100
func (c *OutboxConfig) GetBatchSize() int {
	if c.BatchSize <= 0 {
		return 100
	}
	return c.BatchSize
}

// GetMaxRetries 获取最大重试次数，默认 5
func (c *OutboxConfig) GetMaxRetries() int {
	if c.MaxRetries <= 0 {
		return 5
	}
	return c.MaxRetries
}

// GetTickerTime 获取扫描间隔，默认 1 秒
func (c *OutboxConfig) GetTickerTime() time.Duration {
	if c.TickerTime <= 0 {
		return time.Second
	}
	return c.TickerTime
}

// GetWorkerCount 获取 Worker 数量，默认 5
func (c *OutboxConfig) GetWorkerCount() int {
	if c.WorkerCount <= 0 {
		return 5
	}
	return c.WorkerCount
}

// RegistryConfig 服务注册配置
type RegistryConfig struct {
	Namespace       string        `mapstructure:"namespace"`        // 服务命名空间
	DefaultTTL      time.Duration `mapstructure:"default_ttl"`      // 租约 TTL
	EnableCache     bool          `mapstructure:"enable_cache"`     // 开启缓存
	CacheExpiration time.Duration `mapstructure:"cache_expiration"` // 缓存过期时间
}

// AdminConfig 管理员初始化配置
type AdminConfig struct {
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	Nickname string `mapstructure:"nickname"`
}

// WorkerIDConfig WorkerID 分发配置 (对齐 Gateway)
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

// GetKey 获取 Redis 键，默认 "resonance:logic:worker"
func (c *WorkerIDConfig) GetKey() string {
	if c.Key == "" {
		return "resonance:logic:worker"
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

// GetServerAddr 获取监听地址，默认 :15090
func (c *Config) GetServerAddr() string {
	if strings.TrimSpace(c.Service.ServerAddr) == "" {
		return ":15090"
	}
	return c.Service.ServerAddr
}

// GetAdvertiseEndpoint 返回服务注册使用的 host:port
func (c *Config) GetAdvertiseEndpoint() string {
	addr := strings.TrimSpace(c.GetServerAddr())

	// 去掉 scheme
	if idx := strings.Index(addr, "://"); idx >= 0 {
		addr = addr[idx+3:]
	}

	host := c.GetHost()
	if host == "" {
		host = "localhost"
	}

	if strings.HasPrefix(addr, ":") {
		port := strings.TrimPrefix(addr, ":")
		return net.JoinHostPort(host, port)
	}

	hostname, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return net.JoinHostPort(host, strings.TrimPrefix(addr, ":"))
	}

	if hostname == "" || hostname == "0.0.0.0" || hostname == "::" || hostname == "[::]" {
		hostname = host
	}

	return net.JoinHostPort(hostname, port)
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

// Load 创建并加载 Logic 配置（无参数）
// 配置加载顺序：环境变量 > .env > logic.{env}.yaml > logic.yaml
func Load() (*Config, error) {
	loader, err := config.New(&config.Config{
		Name:      "logic",
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
	if sanitized.Auth.SecretKey != "" {
		sanitized.Auth.SecretKey = "***"
	}
	if sanitized.Admin.Password != "" {
		sanitized.Admin.Password = "***"
	}

	data, _ := json.MarshalIndent(sanitized, "", "  ")
	fmt.Fprintf(os.Stderr, "\n=== Logic Configuration ===\n%s\n=== End of Configuration ===\n\n", data)
}
