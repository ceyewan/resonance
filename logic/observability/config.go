package observability

// TraceConfig Trace 配置
type TraceConfig struct {
	Disable  bool    `mapstructure:"disable"`  // 是否禁用 Trace
	Endpoint string  `mapstructure:"endpoint"` // OTLP Collector 地址
	Insecure bool    `mapstructure:"insecure"` // 是否使用不安全连接
	Sampler  float64 `mapstructure:"sampler"`  // 采样率 (0.0-1.0)
}

// MetricsConfig Metrics 配置
type MetricsConfig struct {
	Port          int    `mapstructure:"port"`           // Prometheus 端口
	Path          string `mapstructure:"path"`           // Metrics 路径
	EnableRuntime bool   `mapstructure:"enable_runtime"` // 是否启用运行时指标
}

// Config 可观测性配置
type Config struct {
	Trace   TraceConfig   `mapstructure:"trace"`
	Metrics MetricsConfig `mapstructure:"metrics"`
}
