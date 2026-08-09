package observability

type Config struct {
	Trace   TraceConfig   `mapstructure:"trace"`
	Metrics MetricsConfig `mapstructure:"metrics"`
}

type TraceConfig struct {
	Disable  bool    `mapstructure:"disable"`
	Endpoint string  `mapstructure:"endpoint"`
	Sampler  float64 `mapstructure:"sampler"`
	Insecure bool    `mapstructure:"insecure"`
}

type MetricsConfig struct {
	Port          int    `mapstructure:"port"`
	Path          string `mapstructure:"path"`
	EnableRuntime bool   `mapstructure:"enable_runtime"`
}
