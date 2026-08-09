package egressproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Option func(*Server)

func WithResolver(resolver Resolver) Option {
	return func(server *Server) {
		if resolver != nil {
			server.resolver = resolver
		}
	}
}

func WithDialer(dialer Dialer) Option {
	return func(server *Server) {
		if dialer != nil {
			server.dialer = dialer
		}
	}
}

// Server is a CONNECT-only, non-MITM Provider egress proxy. It validates DNS
// answers and the initial TLS ClientHello, then copies opaque TLS records.
type Server struct {
	config       Config
	allowedHosts map[string]struct{}
	resolver     Resolver
	dialer       Dialer

	mu         sync.Mutex
	listener   net.Listener
	httpServer *http.Server
	closed     bool
	total      int
	perClient  map[string]int
	active     map[*tunnel]struct{}
	errors     chan error
}

func New(config Config, options ...Option) (*Server, error) {
	config.setDefaults()
	if err := config.Validate(); err != nil {
		return nil, err
	}
	server := &Server{
		config: config, allowedHosts: make(map[string]struct{}, len(config.AllowedHosts)),
		resolver: net.DefaultResolver, dialer: &net.Dialer{}, perClient: make(map[string]int),
		active: make(map[*tunnel]struct{}), errors: make(chan error, 1),
	}
	for _, host := range config.AllowedHosts {
		server.allowedHosts[host] = struct{}{}
	}
	for _, option := range options {
		option(server)
	}
	return server, nil
}

func (s *Server) Run() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("egress proxy is closed")
	}
	if s.listener != nil {
		return fmt.Errorf("egress proxy is already running")
	}
	listener, err := net.Listen("tcp", s.config.Address)
	if err != nil {
		return fmt.Errorf("listen egress proxy: %w", err)
	}
	s.listener = listener
	s.httpServer = &http.Server{
		Handler:           s,
		ReadHeaderTimeout: s.config.ReadHeaderTimeout,
		MaxHeaderBytes:    s.config.MaxHeaderBytes,
		IdleTimeout:       s.config.IdleTimeout,
	}
	go func(httpServer *http.Server, activeListener net.Listener) {
		if serveErr := httpServer.Serve(activeListener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			select {
			case s.errors <- fmt.Errorf("serve egress proxy: %w", serveErr):
			default:
			}
		}
	}(s.httpServer, listener)
	return nil
}

func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

func (s *Server) Errors() <-chan error { return s.errors }

func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	httpServer := s.httpServer
	active := make([]*tunnel, 0, len(s.active))
	for connection := range s.active {
		active = append(active, connection)
	}
	s.mu.Unlock()

	var closeErr error
	if httpServer != nil {
		closeErr = httpServer.Close()
	}
	for _, connection := range active {
		connection.close()
	}
	return closeErr
}

func (s *Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodConnect {
		response.Header().Set("Allow", http.MethodConnect)
		rejectHTTP(response, http.StatusMethodNotAllowed)
		return
	}
	clientKey, err := clientIdentity(request.RemoteAddr)
	if err != nil || !s.acquire(clientKey) {
		rejectHTTP(response, http.StatusTooManyRequests)
		return
	}
	defer s.release(clientKey)

	host, port, err := s.validateConnectRequest(request)
	if err != nil {
		rejectHTTP(response, http.StatusBadRequest)
		return
	}
	addresses, err := s.resolve(request.Context(), host)
	if err != nil {
		rejectHTTP(response, http.StatusBadGateway)
		return
	}
	upstream, err := s.dial(request.Context(), addresses[0], port)
	if err != nil {
		rejectHTTP(response, http.StatusBadGateway)
		return
	}

	hijacker, ok := response.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		rejectHTTP(response, http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	connection := newTunnel(client, upstream, s.config.IdleTimeout)
	if !s.register(connection) {
		connection.close()
		return
	}
	defer s.unregister(connection)
	defer connection.close()

	if err := writeConnectEstablished(buffered.Writer); err != nil {
		return
	}
	_ = client.SetReadDeadline(time.Now().Add(s.config.ClientHelloTimeout))
	rawHello, err := readClientHello(buffered.Reader, host, s.config.MaxClientHelloBytes)
	if err != nil {
		return
	}
	_ = client.SetReadDeadline(time.Time{})
	connection.touch()
	if err := writeAll(upstream, rawHello); err != nil {
		return
	}

	maxDuration := time.AfterFunc(s.config.MaxConnectionDuration, connection.close)
	defer maxDuration.Stop()
	connection.relay(buffered.Reader)
}

func (s *Server) validateConnectRequest(request *http.Request) (string, int, error) {
	if request.ProtoMajor != 1 || request.ProtoMinor != 1 || request.RequestURI != request.Host ||
		request.ContentLength > 0 || len(request.TransferEncoding) > 0 || request.URL.User != nil {
		return "", 0, fmt.Errorf("CONNECT request framing is invalid")
	}
	if strings.ContainsAny(request.Host, "@/?#\\") {
		return "", 0, fmt.Errorf("CONNECT authority contains forbidden syntax")
	}
	host, portText, err := net.SplitHostPort(request.Host)
	if err != nil {
		return "", 0, fmt.Errorf("CONNECT authority requires an explicit host and port")
	}
	canonical, err := canonicalDNSName(host)
	if err != nil || canonical != host {
		return "", 0, fmt.Errorf("CONNECT host is not canonical")
	}
	if _, allowed := s.allowedHosts[canonical]; !allowed {
		return "", 0, fmt.Errorf("CONNECT host is not allowed")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port != providerTLSPort || portText != strconv.Itoa(port) ||
		request.Host != net.JoinHostPort(canonical, portText) {
		return "", 0, fmt.Errorf("CONNECT port is not allowed")
	}
	return canonical, port, nil
}

func (s *Server) resolve(parent context.Context, host string) ([]netip.Addr, error) {
	ctx, cancel := context.WithTimeout(parent, s.config.DNSTimeout)
	defer cancel()
	addresses, err := s.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve provider host: %w", err)
	}
	return validateResolvedAddresses(addresses, s.config.AllowSyntheticBenchmarkAddresses)
}

func (s *Server) dial(parent context.Context, address netip.Addr, port int) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(parent, s.config.DialTimeout)
	defer cancel()
	// Use the already validated address. Passing the hostname here would allow
	// the Dialer to resolve it again and reopen DNS-rebinding attacks.
	connection, err := s.dialer.DialContext(ctx, "tcp", net.JoinHostPort(address.String(), strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("dial provider address: %w", err)
	}
	return connection, nil
}

func (s *Server) acquire(client string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.total >= s.config.MaxConnections || s.perClient[client] >= s.config.MaxConnectionsPerClient {
		return false
	}
	s.total++
	s.perClient[client]++
	return true
}

func (s *Server) release(client string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total--
	s.perClient[client]--
	if s.perClient[client] == 0 {
		delete(s.perClient, client)
	}
}

func (s *Server) register(connection *tunnel) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.active[connection] = struct{}{}
	return true
}

func (s *Server) unregister(connection *tunnel) {
	s.mu.Lock()
	delete(s.active, connection)
	s.mu.Unlock()
}

func clientIdentity(remoteAddress string) (string, error) {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		return "", fmt.Errorf("client address is invalid")
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return "", fmt.Errorf("client address is not an IP")
	}
	return address.Unmap().String(), nil
}

func rejectHTTP(response http.ResponseWriter, status int) {
	response.Header().Set("Connection", "close")
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.WriteHeader(status)
	_, _ = io.WriteString(response, http.StatusText(status)+"\n")
}

func writeConnectEstablished(writer *bufio.Writer) error {
	if _, err := writer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return err
	}
	return writer.Flush()
}

func writeAll(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}

type tunnel struct {
	client     net.Conn
	upstream   net.Conn
	idle       time.Duration
	closeOnce  sync.Once
	deadlineMu sync.Mutex
}

func newTunnel(client, upstream net.Conn, idle time.Duration) *tunnel {
	return &tunnel{client: client, upstream: upstream, idle: idle}
}

func (t *tunnel) touch() {
	t.deadlineMu.Lock()
	defer t.deadlineMu.Unlock()
	deadline := time.Now().Add(t.idle)
	_ = t.client.SetDeadline(deadline)
	_ = t.upstream.SetDeadline(deadline)
}

func (t *tunnel) close() {
	t.closeOnce.Do(func() {
		_ = t.client.Close()
		_ = t.upstream.Close()
	})
}

func (t *tunnel) relay(clientReader io.Reader) {
	results := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(activityWriter{target: t.upstream, tunnel: t}, activityReader{source: clientReader, tunnel: t})
		results <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(activityWriter{target: t.client, tunnel: t}, activityReader{source: t.upstream, tunnel: t})
		results <- struct{}{}
	}()
	<-results
	t.close()
	<-results
}

type activityReader struct {
	source io.Reader
	tunnel *tunnel
}

func (r activityReader) Read(payload []byte) (int, error) {
	count, err := r.source.Read(payload)
	if count > 0 {
		r.tunnel.touch()
	}
	return count, err
}

type activityWriter struct {
	target io.Writer
	tunnel *tunnel
}

func (w activityWriter) Write(payload []byte) (int, error) {
	count, err := w.target.Write(payload)
	if count > 0 {
		w.tunnel.touch()
	}
	return count, err
}
