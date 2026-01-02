package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/config"
)

// Config 定义 Web 模块配置
type Config struct {
	Service ServiceConfig `mapstructure:"service"`
	Static  StaticConfig  `mapstructure:"static"`
	Log     clog.Config   `mapstructure:"log"`
}

// ServiceConfig 基础服务配置
type ServiceConfig struct {
	Name     string `mapstructure:"name"`
	Host     string `mapstructure:"host"`
	HTTPPort int    `mapstructure:"http_port"`
}

// StaticConfig 静态资源相关配置
type StaticConfig struct {
	DistDir           string `mapstructure:"dist_dir"`
	IndexFile         string `mapstructure:"index_file"`
	EnableSPAFallback bool   `mapstructure:"enable_spa_fallback"`
	CacheControl      string `mapstructure:"cache_control"`
}

// GetHTTPAddr 返回监听地址，默认为 :4173
func (s *ServiceConfig) GetHTTPAddr() string {
	host := s.Host
	if host == "" {
		host = "0.0.0.0"
	}
	port := s.HTTPPort
	if port == 0 {
		port = 4173
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// GetDistDir 返回 dist 目录，默认 web/dist
func (s *StaticConfig) GetDistDir() string {
	if s.DistDir == "" {
		return filepath.Clean("./web/dist")
	}
	return filepath.Clean(s.DistDir)
}

// GetIndexFile 返回 SPA Fallback 文件，默认 index.html
func (s *StaticConfig) GetIndexFile() string {
	if s.IndexFile == "" {
		return "index.html"
	}
	return s.IndexFile
}

// GetCacheControl 返回静态资源缓存策略，默认 public,max-age=86400
func (s *StaticConfig) GetCacheControl() string {
	if s.CacheControl == "" {
		return "public, max-age=86400"
	}
	return s.CacheControl
}

// Load 加载 web.yaml 配置
func Load() (*Config, error) {
	loader, err := config.New(&config.Config{
		Name:     "web",
		FileType: "yaml",
	},
		config.WithConfigPaths("./configs"),
		config.WithEnvPrefix("RESONANCE"),
	)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	if err := loader.Load(ctx); err != nil {
		return nil, err
	}

	var cfg Config
	if err := loader.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	if os.Getenv("DEBUG_CONFIG") == "true" || os.Getenv("RESONANCE_DEBUG_CONFIG") == "true" {
		dumpConfig(&cfg)
	}

	return &cfg, nil
}

// MustLoad panic on failure
func MustLoad() *Config {
	cfg, err := Load()
	if err != nil {
		panic(err)
	}
	return cfg
}

func dumpConfig(cfg *Config) {
	sanitized := *cfg
	data, _ := json.MarshalIndent(sanitized, "", "  ")
	fmt.Fprintf(os.Stderr, "\n=== Web Configuration ===\n%s\n=== End of Configuration ===\n\n", data)
}
