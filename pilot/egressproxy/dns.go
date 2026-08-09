package egressproxy

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"golang.org/x/net/idna"
)

type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

var forbiddenAddressPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	// Cloud-provider metadata endpoints that are not covered by the generic
	// private/link-local checks on every platform.
	netip.MustParsePrefix("100.100.100.200/32"),
	netip.MustParsePrefix("168.63.129.16/32"),
	netip.MustParsePrefix("169.254.169.254/32"),
	// IPv6 documentation, discard, benchmarking and transition mechanisms can
	// otherwise tunnel to an address that was not validated here.
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:10::/28"),
	netip.MustParsePrefix("2001:20::/28"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
}

var syntheticBenchmarkPrefix = netip.MustParsePrefix("198.18.0.0/15")

func canonicalDNSName(host string) (string, error) {
	if host == "" || strings.TrimSpace(host) != host || strings.HasSuffix(host, ".") ||
		strings.ContainsAny(host, "*\\/@[]:#?") {
		return "", fmt.Errorf("host is not a canonical DNS name")
	}
	if address, err := netip.ParseAddr(host); err == nil && address.IsValid() {
		return "", fmt.Errorf("IP literals are forbidden")
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", fmt.Errorf("convert IDNA host: %w", err)
	}
	ascii = strings.ToLower(ascii)
	if len(ascii) > 253 || ascii == "" {
		return "", fmt.Errorf("host length is invalid")
	}
	for _, label := range strings.Split(ascii, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("host label is invalid")
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return "", fmt.Errorf("host label contains an invalid character")
			}
		}
	}
	return ascii, nil
}

func validateResolvedAddresses(addresses []netip.Addr, allowSyntheticBenchmarkAddresses bool) ([]netip.Addr, error) {
	if len(addresses) == 0 {
		return nil, fmt.Errorf("DNS returned no addresses")
	}
	validated := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		if err := validateResolvedAddress(address, allowSyntheticBenchmarkAddresses); err != nil {
			// Reject the whole answer set. Accepting one public answer alongside a
			// private answer would leave room for rebinding/failover mistakes.
			return nil, err
		}
		if _, duplicate := seen[address]; duplicate {
			continue
		}
		seen[address] = struct{}{}
		validated = append(validated, address)
	}
	if len(validated) == 0 {
		return nil, fmt.Errorf("DNS returned no unique addresses")
	}
	return validated, nil
}

func validateResolvedAddress(address netip.Addr, allowSyntheticBenchmarkAddresses bool) error {
	if !address.IsValid() || address.Zone() != "" || address.Is4In6() ||
		!address.IsGlobalUnicast() || address.IsUnspecified() || address.IsLoopback() ||
		address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() {
		return fmt.Errorf("DNS returned a forbidden address")
	}
	if syntheticBenchmarkPrefix.Contains(address) {
		if allowSyntheticBenchmarkAddresses {
			return nil
		}
		return fmt.Errorf("DNS returned a reserved or metadata address")
	}
	for _, prefix := range forbiddenAddressPrefixes {
		if prefix.Contains(address) {
			return fmt.Errorf("DNS returned a reserved or metadata address")
		}
	}
	return nil
}
