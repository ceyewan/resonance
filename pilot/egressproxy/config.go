package egressproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	genesisconfig "github.com/ceyewan/genesis/config"
)

const providerTLSPort = 443

// Config defines the fail-closed limits for the Provider CONNECT proxy.
// AllowedHosts contains exact, lower-case ASCII IDNA A-labels. Wildcards,
// trailing dots, IP literals and implicit ports are never accepted.
type Config struct {
	Address      string   `mapstructure:"address"`
	AllowedHosts []string `mapstructure:"allowed_hosts"`
	// AllowSyntheticBenchmarkAddresses is a local-development compatibility
	// switch for Docker Desktop/VPN DNS stacks that synthesize public hosts into
	// RFC 2544 198.18.0.0/15. Production Compose explicitly forces it off.
	AllowSyntheticBenchmarkAddresses bool          `mapstructure:"allow_synthetic_benchmark_addresses"`
	DNSTimeout                       time.Duration `mapstructure:"dns_timeout"`
	DialTimeout                      time.Duration `mapstructure:"dial_timeout"`
	ClientHelloTimeout               time.Duration `mapstructure:"client_hello_timeout"`
	IdleTimeout                      time.Duration `mapstructure:"idle_timeout"`
	MaxConnectionDuration            time.Duration `mapstructure:"max_connection_duration"`
	ReadHeaderTimeout                time.Duration `mapstructure:"read_header_timeout"`
	MaxHeaderBytes                   int           `mapstructure:"max_header_bytes"`
	MaxClientHelloBytes              int           `mapstructure:"max_client_hello_bytes"`
	MaxConnections                   int           `mapstructure:"max_connections"`
	MaxConnectionsPerClient          int           `mapstructure:"max_connections_per_client"`
}

func Load() (*Config, error) {
	loader, err := genesisconfig.New(&genesisconfig.Config{
		Name: "egress-proxy", FileType: "yaml", Paths: []string{"./configs"}, EnvPrefix: "RESONANCE",
	})
	if err != nil {
		return nil, err
	}
	if err := loader.Load(context.Background()); err != nil {
		return nil, errors.Join(err, loader.Close())
	}
	var cfg Config
	if err := loader.Unmarshal(&cfg); err != nil {
		return nil, errors.Join(err, loader.Close())
	}
	if err := loader.Close(); err != nil {
		return nil, fmt.Errorf("close egress proxy config loader: %w", err)
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) setDefaults() {
	if c.Address == "" {
		c.Address = "127.0.0.1:18080"
	}
	if c.DNSTimeout == 0 {
		c.DNSTimeout = 2 * time.Second
	}
	if c.DialTimeout == 0 {
		c.DialTimeout = 5 * time.Second
	}
	if c.ClientHelloTimeout == 0 {
		c.ClientHelloTimeout = 3 * time.Second
	}
	if c.IdleTimeout == 0 {
		c.IdleTimeout = 45 * time.Second
	}
	if c.MaxConnectionDuration == 0 {
		c.MaxConnectionDuration = 10 * time.Minute
	}
	if c.ReadHeaderTimeout == 0 {
		c.ReadHeaderTimeout = 3 * time.Second
	}
	if c.MaxHeaderBytes == 0 {
		c.MaxHeaderBytes = 8 << 10
	}
	if c.MaxClientHelloBytes == 0 {
		c.MaxClientHelloBytes = 64 << 10
	}
	if c.MaxConnections == 0 {
		c.MaxConnections = 128
	}
	if c.MaxConnectionsPerClient == 0 {
		c.MaxConnectionsPerClient = 8
	}
}

func (c Config) Validate() error {
	listenHost, listenPort, err := net.SplitHostPort(c.Address)
	if err != nil || listenHost == "" || listenPort == "" {
		return fmt.Errorf("egress proxy requires an explicit listen host and port")
	}
	port, err := strconv.Atoi(listenPort)
	listenIP := net.ParseIP(listenHost)
	loopbackEphemeral := port == 0 && listenIP != nil && listenIP.IsLoopback()
	if err != nil || (!loopbackEphemeral && (port < 1 || port > 65535)) || listenPort != strconv.Itoa(port) {
		return fmt.Errorf("egress proxy listen port is invalid")
	}
	if len(c.AllowedHosts) == 0 {
		return fmt.Errorf("egress proxy allowed_hosts must not be empty")
	}
	seen := make(map[string]struct{}, len(c.AllowedHosts))
	for _, host := range c.AllowedHosts {
		canonical, err := canonicalDNSName(host)
		if err != nil || canonical != host {
			return fmt.Errorf("egress proxy allowed host must be an exact canonical DNS A-label")
		}
		if _, duplicate := seen[host]; duplicate {
			return fmt.Errorf("egress proxy allowed_hosts contains a duplicate")
		}
		seen[host] = struct{}{}
	}
	if c.DNSTimeout <= 0 || c.DialTimeout <= 0 || c.ClientHelloTimeout <= 0 ||
		c.IdleTimeout <= 0 || c.MaxConnectionDuration <= 0 || c.ReadHeaderTimeout <= 0 ||
		c.IdleTimeout >= c.MaxConnectionDuration {
		return fmt.Errorf("egress proxy timeouts are invalid")
	}
	if c.MaxHeaderBytes < 1024 || c.MaxHeaderBytes > 64<<10 ||
		c.MaxClientHelloBytes < 1024 || c.MaxClientHelloBytes > 256<<10 {
		return fmt.Errorf("egress proxy protocol bounds are invalid")
	}
	if c.MaxConnections < 1 || c.MaxConnections > 4096 ||
		c.MaxConnectionsPerClient < 1 || c.MaxConnectionsPerClient > c.MaxConnections {
		return fmt.Errorf("egress proxy concurrency bounds are invalid")
	}
	return nil
}

// ProductionConfig returns the explicit production contract. The allowlist is
// intentionally not defaulted by Load: deleting it from deployment config must
// make startup fail instead of silently restoring network access.
func ProductionConfig() Config {
	cfg := Config{AllowedHosts: []string{"llm-3rwbpx52jtt7759p.cn-beijing.maas.aliyuncs.com"}}
	cfg.setDefaults()
	return cfg
}
