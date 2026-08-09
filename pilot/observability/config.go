package observability

type Config struct {
	Version     string        `mapstructure:"version"`
	InstanceID  string        `mapstructure:"instance"`
	Environment string        `mapstructure:"environment"`
	Trace       TraceConfig   `mapstructure:"trace"`
	Metrics     MetricsConfig `mapstructure:"metrics"`
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
