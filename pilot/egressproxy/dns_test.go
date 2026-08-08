package egressproxy

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfigValidateFailsClosedForUnsafeAllowlistAndLimits(t *testing.T) {
	base := ProductionConfig()
	require.NoError(t, base.Validate())
	_, err := New(Config{})
	require.Error(t, err, "startup must fail when the production allowlist is missing")

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "empty allowlist", mutate: func(config *Config) { config.AllowedHosts = nil }},
		{name: "wildcard", mutate: func(config *Config) { config.AllowedHosts = []string{"*.anthropic.com"} }},
		{name: "uppercase", mutate: func(config *Config) { config.AllowedHosts = []string{"API.ANTHROPIC.COM"} }},
		{name: "trailing dot", mutate: func(config *Config) { config.AllowedHosts = []string{"api.anthropic.com."} }},
		{name: "IP literal", mutate: func(config *Config) { config.AllowedHosts = []string{"1.1.1.1"} }},
		{name: "duplicate", mutate: func(config *Config) { config.AllowedHosts = []string{"api.anthropic.com", "api.anthropic.com"} }},
		{name: "implicit listen port", mutate: func(config *Config) { config.Address = "127.0.0.1" }},
		{name: "non-numeric listen port", mutate: func(config *Config) { config.Address = "127.0.0.1:https" }},
		{name: "unbounded DNS", mutate: func(config *Config) { config.DNSTimeout = -time.Second }},
		{name: "idle exceeds lifetime", mutate: func(config *Config) { config.IdleTimeout = config.MaxConnectionDuration }},
		{name: "unbounded concurrency", mutate: func(config *Config) { config.MaxConnections = 0; config.MaxConnectionsPerClient = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base
			config.AllowedHosts = append([]string(nil), base.AllowedHosts...)
			test.mutate(&config)
			require.Error(t, config.Validate())
		})
	}
}

func TestCanonicalDNSNameDefinesLowercaseIDNARules(t *testing.T) {
	canonical, err := canonicalDNSName("api.anthropic.com")
	require.NoError(t, err)
	require.Equal(t, "api.anthropic.com", canonical)

	canonical, err = canonicalDNSName("API.ANTHROPIC.COM")
	require.NoError(t, err)
	require.Equal(t, "api.anthropic.com", canonical)

	canonical, err = canonicalDNSName("bücher.example")
	require.NoError(t, err)
	require.Equal(t, "xn--bcher-kva.example", canonical)

	for _, host := range []string{"api.anthropic.com.", "*.anthropic.com", "1.1.1.1", "[::1]", "user@api.anthropic.com"} {
		_, err := canonicalDNSName(host)
		require.Error(t, err, host)
	}
}

func TestValidateResolvedAddressesRejectsEveryNonPublicAndMixedAnswer(t *testing.T) {
	forbidden := []string{
		"0.0.0.0", "127.0.0.1", "10.0.0.1", "100.100.100.200", "168.63.129.16",
		"169.254.169.254", "224.0.0.1", "192.0.2.1", "198.18.0.1", "198.51.100.2",
		"203.0.113.3", "255.255.255.255", "::", "::1", "fe80::1", "fc00::1",
		"ff02::1", "2001:db8::1", "2002:0808:0808::1", "::ffff:8.8.8.8",
	}
	for _, raw := range forbidden {
		t.Run(raw, func(t *testing.T) {
			_, err := validateResolvedAddresses([]netip.Addr{netip.MustParseAddr(raw)})
			require.Error(t, err)
		})
	}

	public := []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("2606:4700:4700::1111")}
	validated, err := validateResolvedAddresses(public)
	require.NoError(t, err)
	require.Equal(t, public, validated)

	_, err = validateResolvedAddresses([]netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("10.0.0.1")})
	require.Error(t, err, "a mixed public/private DNS answer must reject the whole set")
}
