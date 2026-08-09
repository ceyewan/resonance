package egressproxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServerLoopbackPinsValidatedIPAndRelaysOpaqueTLS(t *testing.T) {
	dialer := newPipeDialer()
	server := startLoopbackServer(t, testProxyConfig(), staticResolver{addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1")}}, dialer)

	client, reader, status := openRawProxyRequest(t, server, "CONNECT llm-3rwbpx52jtt7759p.cn-beijing.maas.aliyuncs.com:443 HTTP/1.1\r\nHost: llm-3rwbpx52jtt7759p.cn-beijing.maas.aliyuncs.com:443\r\n\r\n")
	require.Equal(t, http.StatusOK, status)
	backend := <-dialer.connections
	t.Cleanup(func() { _ = backend.Close() })
	require.Equal(t, []string{"1.1.1.1:443"}, dialer.addressesSnapshot(), "the dialer must receive the validated IP, never the hostname")

	hello := fragmentFirstTLSRecord(t, captureTLSClientHello(t, "llm-3rwbpx52jtt7759p.cn-beijing.maas.aliyuncs.com", []string{"h2", "http/1.1"}), 19)
	backendHello := make(chan []byte, 1)
	go func() {
		payload := make([]byte, len(hello))
		_, _ = io.ReadFull(backend, payload)
		backendHello <- payload
	}()
	for offset := 0; offset < len(hello); {
		end := offset + 7
		if end > len(hello) {
			end = len(hello)
		}
		_, err := client.Write(hello[offset:end])
		require.NoError(t, err)
		offset = end
	}
	require.Equal(t, hello, <-backendHello)

	go func() {
		payload := make([]byte, len("client-data"))
		_, _ = io.ReadFull(backend, payload)
		if string(payload) == "client-data" {
			_, _ = backend.Write([]byte("upstream-data"))
		}
	}()
	_, err := client.Write([]byte("client-data"))
	require.NoError(t, err)
	reply := make([]byte, len("upstream-data"))
	_, err = io.ReadFull(reader, reply)
	require.NoError(t, err)
	require.Equal(t, "upstream-data", string(reply))
}

func TestServerRejectsOrdinaryHTTPAndNonCanonicalAuthoritiesBeforeDNS(t *testing.T) {
	tests := []struct {
		name    string
		request string
		status  int
	}{
		{name: "ordinary HTTP", request: "GET https://api.anthropic.com/ HTTP/1.1\r\nHost: api.anthropic.com\r\n\r\n", status: http.StatusMethodNotAllowed},
		{name: "IP literal", request: "CONNECT 1.1.1.1:443 HTTP/1.1\r\nHost: 1.1.1.1:443\r\n\r\n", status: http.StatusBadRequest},
		{name: "IPv6 literal", request: "CONNECT [2606:4700:4700::1111]:443 HTTP/1.1\r\nHost: [2606:4700:4700::1111]:443\r\n\r\n", status: http.StatusBadRequest},
		{name: "wrong port", request: "CONNECT api.anthropic.com:8443 HTTP/1.1\r\nHost: api.anthropic.com:8443\r\n\r\n", status: http.StatusBadRequest},
		{name: "leading-zero port", request: "CONNECT api.anthropic.com:0443 HTTP/1.1\r\nHost: api.anthropic.com:0443\r\n\r\n", status: http.StatusBadRequest},
		{name: "implicit port", request: "CONNECT api.anthropic.com HTTP/1.1\r\nHost: api.anthropic.com\r\n\r\n", status: http.StatusBadRequest},
		{name: "uppercase", request: "CONNECT API.ANTHROPIC.COM:443 HTTP/1.1\r\nHost: API.ANTHROPIC.COM:443\r\n\r\n", status: http.StatusBadRequest},
		{name: "trailing dot", request: "CONNECT api.anthropic.com.:443 HTTP/1.1\r\nHost: api.anthropic.com.:443\r\n\r\n", status: http.StatusBadRequest},
		{name: "userinfo", request: "CONNECT user@api.anthropic.com:443 HTTP/1.1\r\nHost: user@api.anthropic.com:443\r\n\r\n", status: http.StatusBadRequest},
		{name: "unlisted host", request: "CONNECT console.anthropic.com:443 HTTP/1.1\r\nHost: console.anthropic.com:443\r\n\r\n", status: http.StatusBadRequest},
		{name: "request body", request: "CONNECT api.anthropic.com:443 HTTP/1.1\r\nHost: api.anthropic.com:443\r\nContent-Length: 1\r\n\r\nx", status: http.StatusBadRequest},
		{name: "HTTP 1.0", request: "CONNECT api.anthropic.com:443 HTTP/1.0\r\nHost: api.anthropic.com:443\r\n\r\n", status: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dialer := newPipeDialer()
			server := startLoopbackServer(t, testProxyConfig(), staticResolver{addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1")}}, dialer)
			client, _, status := openRawProxyRequest(t, server, test.request)
			_ = client.Close()
			require.Equal(t, test.status, status)
			require.Empty(t, dialer.addressesSnapshot())
		})
	}
}

func TestServerRejectsPrivateMetadataAndMixedDNSWithoutDial(t *testing.T) {
	tests := []struct {
		name      string
		addresses []netip.Addr
	}{
		{name: "private", addresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")}},
		{name: "metadata", addresses: []netip.Addr{netip.MustParseAddr("169.254.169.254")}},
		{name: "mixed", addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("10.0.0.1")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dialer := newPipeDialer()
			server := startLoopbackServer(t, testProxyConfig(), staticResolver{addresses: test.addresses}, dialer)
			client, _, status := openRawProxyRequest(t, server, canonicalConnectRequest())
			_ = client.Close()
			require.Equal(t, http.StatusBadGateway, status)
			require.Empty(t, dialer.addressesSnapshot())
		})
	}
}

func TestServerDevelopmentModePinsSyntheticBenchmarkAddress(t *testing.T) {
	config := testProxyConfig()
	config.AllowSyntheticBenchmarkAddresses = true
	dialer := newPipeDialer()
	server := startLoopbackServer(t, config, staticResolver{
		addresses: []netip.Addr{netip.MustParseAddr("198.18.1.151")},
	}, dialer)

	client, _, status := openRawProxyRequest(t, server, canonicalConnectRequest())
	require.Equal(t, http.StatusOK, status)
	backend := <-dialer.connections
	defer backend.Close()
	require.Equal(t, []string{"198.18.1.151:443"}, dialer.addressesSnapshot())
	_ = client.Close()
}

func TestServerBoundsDNSDialClientHelloIdleConcurrencyAndClose(t *testing.T) {
	t.Run("DNS timeout", func(t *testing.T) {
		config := testProxyConfig()
		config.DNSTimeout = 30 * time.Millisecond
		server := startLoopbackServer(t, config, blockingResolver{}, newPipeDialer())
		started := time.Now()
		client, _, status := openRawProxyRequest(t, server, canonicalConnectRequest())
		_ = client.Close()
		require.Equal(t, http.StatusBadGateway, status)
		require.Less(t, time.Since(started), time.Second)
	})

	t.Run("dial timeout", func(t *testing.T) {
		config := testProxyConfig()
		config.DialTimeout = 30 * time.Millisecond
		server := startLoopbackServer(t, config, staticResolver{addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1")}}, blockingDialer{})
		client, _, status := openRawProxyRequest(t, server, canonicalConnectRequest())
		_ = client.Close()
		require.Equal(t, http.StatusBadGateway, status)
	})

	t.Run("ClientHello timeout", func(t *testing.T) {
		config := testProxyConfig()
		config.ClientHelloTimeout = 40 * time.Millisecond
		dialer := newPipeDialer()
		server := startLoopbackServer(t, config, staticResolver{addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1")}}, dialer)
		client, _, status := openRawProxyRequest(t, server, canonicalConnectRequest())
		require.Equal(t, http.StatusOK, status)
		backend := <-dialer.connections
		defer backend.Close()
		_ = client.SetReadDeadline(time.Now().Add(time.Second))
		one := make([]byte, 1)
		_, err := client.Read(one)
		require.Error(t, err)
	})

	t.Run("idle half-open tunnel", func(t *testing.T) {
		config := testProxyConfig()
		config.IdleTimeout = 60 * time.Millisecond
		config.MaxConnectionDuration = time.Second
		client, backend := openValidatedTunnel(t, config)
		defer backend.Close()
		_ = client.SetReadDeadline(time.Now().Add(time.Second))
		_, err := client.Read(make([]byte, 1))
		require.Error(t, err)
	})

	t.Run("per-client concurrency", func(t *testing.T) {
		config := testProxyConfig()
		config.MaxConnections = 2
		config.MaxConnectionsPerClient = 1
		config.ClientHelloTimeout = time.Second
		dialer := newPipeDialer()
		server := startLoopbackServer(t, config, staticResolver{addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1")}}, dialer)
		first, _, status := openRawProxyRequest(t, server, canonicalConnectRequest())
		require.Equal(t, http.StatusOK, status)
		backend := <-dialer.connections
		defer backend.Close()
		second, _, status := openRawProxyRequest(t, server, canonicalConnectRequest())
		defer second.Close()
		require.Equal(t, http.StatusTooManyRequests, status)
		_ = first.Close()
	})

	t.Run("server close tears down hijacked tunnel", func(t *testing.T) {
		config := testProxyConfig()
		config.ClientHelloTimeout = time.Second
		dialer := newPipeDialer()
		server := startLoopbackServer(t, config, staticResolver{addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1")}}, dialer)
		client, _, status := openRawProxyRequest(t, server, canonicalConnectRequest())
		require.Equal(t, http.StatusOK, status)
		backend := <-dialer.connections
		defer backend.Close()
		require.NoError(t, server.Close())
		_ = client.SetReadDeadline(time.Now().Add(time.Second))
		_, err := client.Read(make([]byte, 1))
		require.Error(t, err)
	})
}

func TestServerLimiterBoundsGlobalAndPerClientConnections(t *testing.T) {
	config := testProxyConfig()
	config.MaxConnections = 2
	config.MaxConnectionsPerClient = 1
	server, err := New(config)
	require.NoError(t, err)
	require.True(t, server.acquire("instance-a"))
	require.False(t, server.acquire("instance-a"), "one instance cannot consume more than its quota")
	require.True(t, server.acquire("instance-b"))
	require.False(t, server.acquire("instance-c"), "global connection quota must remain bounded")
	server.release("instance-a")
	require.True(t, server.acquire("instance-c"))
	server.release("instance-b")
	server.release("instance-c")
}

func openValidatedTunnel(t *testing.T, config Config) (net.Conn, net.Conn) {
	t.Helper()
	dialer := newPipeDialer()
	server := startLoopbackServer(t, config, staticResolver{addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1")}}, dialer)
	client, _, status := openRawProxyRequest(t, server, canonicalConnectRequest())
	require.Equal(t, http.StatusOK, status)
	backend := <-dialer.connections
	hello := captureTLSClientHello(t, "llm-3rwbpx52jtt7759p.cn-beijing.maas.aliyuncs.com", []string{"h2"})
	backendHello := make(chan struct{})
	go func() {
		_, _ = io.ReadFull(backend, make([]byte, len(hello)))
		close(backendHello)
	}()
	_, err := client.Write(hello)
	require.NoError(t, err)
	<-backendHello
	return client, backend
}

func startLoopbackServer(t *testing.T, config Config, resolver Resolver, dialer Dialer) *Server {
	t.Helper()
	server, err := New(config, WithResolver(resolver), WithDialer(dialer))
	require.NoError(t, err)
	require.NoError(t, server.Run())
	t.Cleanup(func() { _ = server.Close() })
	require.NotNil(t, server.Addr())
	return server
}

func openRawProxyRequest(t *testing.T, server *Server, request string) (net.Conn, *bufio.Reader, int) {
	t.Helper()
	client, err := net.Dial("tcp", server.Addr().String())
	require.NoError(t, err)
	_, err = io.WriteString(client, request)
	require.NoError(t, err)
	reader := bufio.NewReader(client)
	statusLine, err := reader.ReadString('\n')
	require.NoError(t, err)
	fields := strings.Fields(statusLine)
	require.GreaterOrEqual(t, len(fields), 2)
	status, err := strconv.Atoi(fields[1])
	require.NoError(t, err)
	for {
		line, err := reader.ReadString('\n')
		require.NoError(t, err)
		if line == "\r\n" {
			break
		}
	}
	return client, reader, status
}

func canonicalConnectRequest() string {
	return "CONNECT llm-3rwbpx52jtt7759p.cn-beijing.maas.aliyuncs.com:443 HTTP/1.1\r\nHost: llm-3rwbpx52jtt7759p.cn-beijing.maas.aliyuncs.com:443\r\n\r\n"
}

func testProxyConfig() Config {
	config := ProductionConfig()
	config.Address = "127.0.0.1:0"
	config.DNSTimeout = 200 * time.Millisecond
	config.DialTimeout = 200 * time.Millisecond
	config.ClientHelloTimeout = 200 * time.Millisecond
	config.IdleTimeout = 500 * time.Millisecond
	config.MaxConnectionDuration = 2 * time.Second
	return config
}

type staticResolver struct {
	addresses []netip.Addr
	err       error
}

func (r staticResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), r.addresses...), r.err
}

type blockingResolver struct{}

func (blockingResolver) LookupNetIP(ctx context.Context, _, _ string) ([]netip.Addr, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type blockingDialer struct{}

func (blockingDialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type pipeDialer struct {
	mu          sync.Mutex
	addresses   []string
	connections chan net.Conn
}

func newPipeDialer() *pipeDialer {
	return &pipeDialer{connections: make(chan net.Conn, 16)}
}

func (d *pipeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" {
		return nil, fmt.Errorf("unexpected network %s", network)
	}
	client, backend := net.Pipe()
	d.mu.Lock()
	d.addresses = append(d.addresses, address)
	d.mu.Unlock()
	select {
	case d.connections <- backend:
		return client, nil
	case <-ctx.Done():
		_ = client.Close()
		_ = backend.Close()
		return nil, ctx.Err()
	}
}

func (d *pipeDialer) addressesSnapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.addresses...)
}
