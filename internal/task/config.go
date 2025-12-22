package task

import (
	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
)

// Config Task 服务配置
type Config struct {
	// 基础组件配置
	Log   clog.Config           `mapstructure:"log"`   // 日志配置
	MySQL connector.MySQLConfig `mapstructure:"mysql"` // MySQL 配置
	Redis connector.RedisConfig `mapstructure:"redis"` // Redis 配置
	NATS  connector.NATSConfig  `mapstructure:"nats"`  // NATS 配置

	// Gateway 集群配置
	GatewayAddrs []string `mapstructure:"gateway_addrs"` // Gateway 服务地址列表

	// 消费者配置
	ConsumerConfig ConsumerConfig `mapstructure:"consumer"`
}

// ConsumerConfig MQ 消费者配置
type ConsumerConfig struct {
	Topic         string `mapstructure:"topic"`          // 订阅的主题
	QueueGroup    string `mapstructure:"queue_group"`    // 队列组名称
	WorkerCount   int    `mapstructure:"worker_count"`   // 并发处理协程数
	MaxRetry      int    `mapstructure:"max_retry"`      // 最大重试次数
	RetryInterval int    `mapstructure:"retry_interval"` // 重试间隔（秒）
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Log: clog.DefaultConfig(),
		GatewayAddrs: []string{
			"localhost:8080",
		},
		ConsumerConfig: ConsumerConfig{
			Topic:         "resonance.push.event.v1",
			QueueGroup:    "task-service",
			WorkerCount:   10,
			MaxRetry:      3,
			RetryInterval: 5,
		},
	}
}

