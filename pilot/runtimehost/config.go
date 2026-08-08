package runtimehost

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ceyewan/genesis/clog"
	genesisconfig "github.com/ceyewan/genesis/config"
)

var environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Config struct {
	Service struct {
		Name     string `mapstructure:"name"`
		HTTPPort int    `mapstructure:"http_port"`
	} `mapstructure:"service"`
	Log     clog.Config     `mapstructure:"log"`
	Runtime RuntimeConfig   `mapstructure:"runtime"`
	Remote  RemoteConfig    `mapstructure:"remote"`
	Relay   ToolRelayConfig `mapstructure:"tool_relay"`
}

type RuntimeConfig struct {
	Binary               string        `mapstructure:"binary"`
	ExpectedVersion      string        `mapstructure:"expected_version"`
	ExtensionPath        string        `mapstructure:"extension_path"`
	WorkDir              string        `mapstructure:"work_dir"`
	AgentDir             string        `mapstructure:"agent_dir"`
	ProviderEnvAllowlist []string      `mapstructure:"provider_env_allowlist"`
	ProviderProxyURL     string        `mapstructure:"provider_proxy_url"`
	MaxFrameBytes        int           `mapstructure:"max_frame_bytes"`
	MaxOutputBytes       int64         `mapstructure:"max_output_bytes"`
	MaxStderrBytes       int           `mapstructure:"max_stderr_bytes"`
	EventQueueSize       int           `mapstructure:"event_queue_size"`
	StartupEventLimit    int           `mapstructure:"startup_event_limit"`
	EventOfferTimeout    time.Duration `mapstructure:"event_offer_timeout"`
	CommandTimeout       time.Duration `mapstructure:"command_timeout"`
	ProbeTimeout         time.Duration `mapstructure:"probe_timeout"`
	AbortGrace           time.Duration `mapstructure:"abort_grace"`
	TermGrace            time.Duration `mapstructure:"term_grace"`
	KillGrace            time.Duration `mapstructure:"kill_grace"`
}

type RemoteConfig struct {
	SocketPath      string        `mapstructure:"socket_path"`
	SessionRoot     string        `mapstructure:"session_root"`
	MaxRequestBytes int64         `mapstructure:"max_request_bytes"`
	MaxFrameBytes   int           `mapstructure:"max_frame_bytes"`
	HeaderTimeout   time.Duration `mapstructure:"header_timeout"`
}

type ToolRelayConfig struct {
	ListenAddress    string        `mapstructure:"listen_address"`
	BrokerSocket     string        `mapstructure:"broker_socket"`
	MaxRequestBytes  int64         `mapstructure:"max_request_bytes"`
	MaxResponseBytes int64         `mapstructure:"max_response_bytes"`
	RequestTimeout   time.Duration `mapstructure:"request_timeout"`
	MaxConcurrent    int           `mapstructure:"max_concurrent"`
}

func Load() (*Config, error) {
	loader, err := genesisconfig.New(&genesisconfig.Config{
		Name: "pilot-runtime", FileType: "yaml", Paths: []string{"./configs"}, EnvPrefix: "RESONANCE",
	})
	if err != nil {
		return nil, err
	}
	if err := loader.Load(context.Background()); err != nil {
		return nil, err
	}
	var config Config
	if err := loader.Unmarshal(&config); err != nil {
		return nil, err
	}
	config.setDefaults()
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &config, nil
}

func (c *Config) setDefaults() {
	if c.Service.Name == "" {
		c.Service.Name = "pilot-runtime"
	}
	if c.Service.HTTPPort == 0 {
		c.Service.HTTPPort = 15095
	}
	if c.Remote.SocketPath == "" {
		c.Remote.SocketPath = "/run/resonance-agent/runtime.sock"
	}
	if c.Remote.SessionRoot == "" {
		c.Remote.SessionRoot = "/var/lib/resonance-pilot/sessions"
	}
	if c.Remote.MaxRequestBytes == 0 {
		c.Remote.MaxRequestBytes = 1 << 20
	}
	if c.Remote.MaxFrameBytes == 0 {
		c.Remote.MaxFrameBytes = 8 << 20
	}
	if c.Remote.HeaderTimeout == 0 {
		c.Remote.HeaderTimeout = 2 * time.Second
	}
	if c.Relay.ListenAddress == "" {
		c.Relay.ListenAddress = "127.0.0.1:15094"
	}
	if c.Relay.BrokerSocket == "" {
		c.Relay.BrokerSocket = "/run/resonance-agent/broker.sock"
	}
	if c.Relay.MaxRequestBytes == 0 {
		c.Relay.MaxRequestBytes = 64 << 10
	}
	if c.Relay.MaxResponseBytes == 0 {
		c.Relay.MaxResponseBytes = 64 << 10
	}
	if c.Relay.RequestTimeout == 0 {
		c.Relay.RequestTimeout = 5 * time.Second
	}
	if c.Relay.MaxConcurrent == 0 {
		c.Relay.MaxConcurrent = 32
	}
}

func (c Config) Validate() error {
	if c.Service.Name == "" || c.Service.HTTPPort < 1 || c.Service.HTTPPort > 65535 {
		return fmt.Errorf("runtime host service configuration is invalid")
	}
	if !absoluteClean(c.Runtime.Binary) || !absoluteClean(c.Runtime.ExtensionPath) ||
		!absoluteClean(c.Runtime.WorkDir) || !absoluteClean(c.Runtime.AgentDir) || c.Runtime.ExpectedVersion == "" {
		return fmt.Errorf("runtime host requires pinned absolute Pi paths")
	}
	proxyURL, err := url.Parse(c.Runtime.ProviderProxyURL)
	if err != nil || proxyURL.Scheme != "http" || proxyURL.User != nil || proxyURL.Hostname() == "" ||
		proxyURL.Port() == "" || proxyURL.Path != "" || proxyURL.RawQuery != "" || proxyURL.Fragment != "" {
		return fmt.Errorf("runtime host requires an explicit trusted Provider proxy URL")
	}
	if !absoluteClean(c.Remote.SocketPath) || !absoluteClean(c.Remote.SessionRoot) ||
		!absoluteClean(c.Relay.BrokerSocket) || filepath.Dir(c.Remote.SocketPath) != filepath.Dir(c.Relay.BrokerSocket) {
		return fmt.Errorf("runtime host sockets and session root are invalid")
	}
	if c.Remote.MaxRequestBytes < 1 || c.Remote.MaxRequestBytes > 8<<20 ||
		c.Remote.MaxFrameBytes < 1 || c.Remote.MaxFrameBytes > 8<<20 || c.Remote.HeaderTimeout <= 0 {
		return fmt.Errorf("runtime host remote protocol limits are invalid")
	}
	host, port, err := net.SplitHostPort(c.Relay.ListenAddress)
	if err != nil || port == "" || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() ||
		port == fmt.Sprintf("%d", c.Service.HTTPPort) {
		return fmt.Errorf("runtime host tool relay must use a distinct explicit loopback listener")
	}
	if c.Relay.MaxRequestBytes < 1 || c.Relay.MaxRequestBytes > 8<<20 ||
		c.Relay.MaxResponseBytes < 1 || c.Relay.MaxResponseBytes > 8<<20 ||
		c.Relay.RequestTimeout <= 0 || c.Relay.MaxConcurrent < 1 || c.Relay.MaxConcurrent > 1024 {
		return fmt.Errorf("runtime host tool relay limits are invalid")
	}
	if err := validateProviderEnvironment(c.Runtime.ProviderEnvAllowlist); err != nil {
		return err
	}
	return nil
}

func validateProviderEnvironment(names []string) error {
	required := []string{"PATH", "ANTHROPIC_API_KEY", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "PI_OFFLINE", "PI_TELEMETRY"}
	if len(names) != len(required) {
		return fmt.Errorf("runtime host provider environment must use the fixed production allowlist")
	}
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if !environmentNamePattern.MatchString(name) || strings.HasPrefix(name, "RESONANCE_") ||
			!slices.Contains(required, name) {
			return fmt.Errorf("runtime host provider environment %q is forbidden", name)
		}
		seen[name] = struct{}{}
	}
	for _, name := range required {
		if _, ok := seen[name]; !ok {
			return fmt.Errorf("runtime host provider environment %s is required", name)
		}
	}
	return nil
}

func (c Config) RuntimeEnvironment() ([]string, error) {
	environment := make([]string, 0, len(c.Runtime.ProviderEnvAllowlist))
	for _, name := range c.Runtime.ProviderEnvAllowlist {
		value, ok := os.LookupEnv(name)
		if !ok || value == "" {
			return nil, fmt.Errorf("required runtime environment %s is not set", name)
		}
		environment = append(environment, name+"="+value)
	}
	if os.Getenv("HTTP_PROXY") != c.Runtime.ProviderProxyURL || os.Getenv("HTTPS_PROXY") != c.Runtime.ProviderProxyURL ||
		os.Getenv("NO_PROXY") != "127.0.0.1,localhost" || os.Getenv("PI_OFFLINE") != "1" ||
		os.Getenv("PI_TELEMETRY") != "0" {
		return nil, fmt.Errorf("runtime Provider proxy and offline environment do not match the pinned contract")
	}
	return environment, nil
}

func absoluteClean(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path && len(path) <= 240
}

func (c Config) HTTPAddr() string { return fmt.Sprintf(":%d", c.Service.HTTPPort) }
