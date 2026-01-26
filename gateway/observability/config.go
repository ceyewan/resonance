package observability

// Config 可观测性配置
type Config struct {
	Trace   TraceConfig   `mapstructure:"trace"`
	Metrics MetricsConfig `mapstructure:"metrics"`
}

// TraceConfig 链路追踪配置
type TraceConfig struct {
	Disable  bool    `mapstructure:"disable"`  // 是否禁用 Trace 上报
	Endpoint string  `mapstructure:"endpoint"` // OTLP 端点，例如 "localhost:4317"
	Insecure bool    `mapstructure:"insecure"` // 是否使用不安全连接
	Sampler  float64 `mapstructure:"sampler"`  // 采样率（0-1）
}

// MetricsConfig 指标收集配置
type MetricsConfig struct {
	Port          int    `mapstructure:"port"`           // Prometheus 暴露端口
	Path          string `mapstructure:"path"`           // Prometheus 暴露路径
	EnableRuntime bool   `mapstructure:"enable_runtime"` // 是否启用运行时指标
}
