package observability

// Config 可观测性配置
type Config struct {
	Trace   TraceConfig
	Metrics MetricsConfig
}

// TraceConfig Trace 配置
type TraceConfig struct {
	// Disable 是否禁用 Trace（禁用后只生成 TraceID 不上报）
	Disable bool `mapstructure:"disable"`
	// Endpoint OTLP gRPC 收集器地址（如 localhost:4317）
	Endpoint string `mapstructure:"endpoint"`
	// Sampler 采样率 0.0-1.0
	Sampler float64 `mapstructure:"sampler"`
	// Insecure 是否使用非安全连接
	Insecure bool `mapstructure:"insecure"`
}

// MetricsConfig Metrics 配置
type MetricsConfig struct {
	// Port Prometheus HTTP 服务器端口
	Port int `mapstructure:"port"`
	// Path Prometheus 指标的 HTTP 路径
	Path string `mapstructure:"path"`
	// EnableRuntime 是否启用 Go Runtime 指标采集
	EnableRuntime bool `mapstructure:"enable_runtime"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Trace: TraceConfig{
			Disable: false,
			Endpoint: "localhost:4317",
			Sampler:  1.0,
			Insecure: true,
		},
		Metrics: MetricsConfig{
			Port:          9090,
			Path:          "/metrics",
			EnableRuntime: true,
		},
	}
}
