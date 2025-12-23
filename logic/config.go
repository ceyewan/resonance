package logic

import (
	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/idgen"
)

// Config Logic 服务配置
type Config struct {
	// 服务基础配置
	ServerAddr string `mapstructure:"server_addr"` // gRPC 服务地址

	// 基础组件配置
	Log   clog.Config           `mapstructure:"log"`   // 日志配置
	MySQL connector.MySQLConfig `mapstructure:"mysql"` // MySQL 配置
	Redis connector.RedisConfig `mapstructure:"redis"` // Redis 配置
	NATS  connector.NATSConfig  `mapstructure:"nats"`  // NATS 配置

	// ID 生成器配置
	IDGen idgen.SnowflakeConfig `mapstructure:"idgen"` // Snowflake ID 生成器配置
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		ServerAddr: ":9090",
		Log: clog.Config{
			Level:  "debug",
			Format: "console",
			Output: "stdout",
		},
		IDGen: idgen.SnowflakeConfig{
			WorkerID:     1,
			DatacenterID: 1,
		},
	}
}

