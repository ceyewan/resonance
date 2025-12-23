package gateway

import (
	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
)

// Config Gateway 服务配置
type Config struct {
	// 服务基础配置
	GatewayID string `mapstructure:"gateway_id"` // 网关实例ID
	HTTPAddr  string `mapstructure:"http_addr"`  // HTTP 服务地址
	WSAddr    string `mapstructure:"ws_addr"`    // WebSocket 服务地址

	// Logic 服务地址
	LogicAddr string `mapstructure:"logic_addr"` // Logic gRPC 服务地址

	// 基础组件配置
	Log   clog.Config           `mapstructure:"log"`   // 日志配置
	Redis connector.RedisConfig `mapstructure:"redis"` // Redis 配置
	NATS  connector.NATSConfig  `mapstructure:"nats"`  // NATS 配置

	// WebSocket 配置
	WSConfig WSConfig `mapstructure:"ws_config"`
}

// WSConfig WebSocket 相关配置
type WSConfig struct {
	ReadBufferSize  int `mapstructure:"read_buffer_size"`  // 读缓冲区大小
	WriteBufferSize int `mapstructure:"write_buffer_size"` // 写缓冲区大小
	MaxMessageSize  int `mapstructure:"max_message_size"`  // 最大消息大小
	PingInterval    int `mapstructure:"ping_interval"`     // 心跳间隔（秒）
	PongTimeout     int `mapstructure:"pong_timeout"`      // 心跳超时（秒）
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		GatewayID: "gateway-1",
		HTTPAddr:  ":8080",
		WSAddr:    ":8081",
		LogicAddr: "localhost:9090",
		Log: clog.Config{
			Level:  "debug",
			Format: "console",
			Output: "stdout",
		},
		WSConfig: WSConfig{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			MaxMessageSize:  1024 * 1024, // 1MB
			PingInterval:    30,
			PongTimeout:     60,
		},
	}
}
