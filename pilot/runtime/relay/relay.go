// Package relay exposes the control-plane Tool Broker on loopback inside the
// isolated Pi Runtime container and forwards only the two trusted Bridge APIs
// over a private Unix socket. It is not a general HTTP proxy.
package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Config struct {
	ListenAddress    string
	BrokerSocket     string
	MaxRequestBytes  int64
	MaxResponseBytes int64
	RequestTimeout   time.Duration
	MaxConcurrent    int
}

type Relay struct {
	config    Config
	transport *http.Transport
	client    *http.Client
	semaphore chan struct{}
	errors    chan error

	mu       sync.Mutex
	listener net.Listener
	server   *http.Server
}

func New(config Config) (*Relay, error) {
	if config.MaxRequestBytes == 0 {
		config.MaxRequestBytes = 64 << 10
	}
	if config.MaxResponseBytes == 0 {
		config.MaxResponseBytes = 64 << 10
	}
	if config.RequestTimeout == 0 {
		config.RequestTimeout = 5 * time.Second
	}
	if config.MaxConcurrent == 0 {
		config.MaxConcurrent = 32
	}
	host, _, err := net.SplitHostPort(config.ListenAddress)
	if err != nil {
		return nil, fmt.Errorf("tool relay listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("tool relay must bind an explicit loopback IP")
	}
	if config.BrokerSocket == "" || !filepath.IsAbs(config.BrokerSocket) ||
		filepath.Clean(config.BrokerSocket) != config.BrokerSocket || len(config.BrokerSocket) > 240 {
		return nil, fmt.Errorf("tool relay broker socket must be a bounded absolute clean path")
	}
	if config.MaxRequestBytes < 1 || config.MaxRequestBytes > 8<<20 ||
		config.MaxResponseBytes < 1 || config.MaxResponseBytes > 8<<20 ||
		config.RequestTimeout <= 0 || config.MaxConcurrent < 1 || config.MaxConcurrent > 1024 {
		return nil, fmt.Errorf("tool relay limits are invalid")
	}
	dialer := &net.Dialer{Timeout: min(config.RequestTimeout, 2*time.Second)}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", config.BrokerSocket)
		},
		DisableCompression: true, MaxConnsPerHost: config.MaxConcurrent,
		MaxIdleConns: config.MaxConcurrent, MaxIdleConnsPerHost: config.MaxConcurrent,
		IdleConnTimeout: 30 * time.Second,
	}
	return &Relay{
		config: config, transport: transport, semaphore: make(chan struct{}, config.MaxConcurrent), errors: make(chan error, 1),
		client: &http.Client{
			Transport: transport, Timeout: config.RequestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("tool relay redirects are disabled") },
		},
	}, nil
}

func (r *Relay) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listener != nil {
		return fmt.Errorf("tool relay already started")
	}
	listener, err := net.Listen("tcp", r.config.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen tool relay: %w", err)
	}
	server := &http.Server{
		Handler:           r,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       r.config.RequestTimeout,
		WriteTimeout:      r.config.RequestTimeout,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	r.listener = listener
	r.server = server
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			select {
			case r.errors <- fmt.Errorf("tool relay serve: %w", serveErr):
			default:
			}
		}
	}()
	return nil
}

func (r *Relay) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if !validBridgeRequest(request) {
		writeRelayError(writer, http.StatusNotFound)
		return
	}
	select {
	case r.semaphore <- struct{}{}:
		defer func() { <-r.semaphore }()
	default:
		writeRelayError(writer, http.StatusServiceUnavailable)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, r.config.MaxRequestBytes)
	upstream, err := http.NewRequestWithContext(request.Context(), request.Method, "http://broker"+request.URL.Path, request.Body)
	if err != nil {
		writeRelayError(writer, http.StatusBadRequest)
		return
	}
	for _, header := range []string{"Authorization", "Accept", "Content-Type"} {
		if value := request.Header.Get(header); value != "" {
			upstream.Header.Set(header, value)
		}
	}
	response, err := r.client.Do(upstream)
	if err != nil {
		writeRelayError(writer, http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.ContentLength > r.config.MaxResponseBytes {
		writeRelayError(writer, http.StatusBadGateway)
		return
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, r.config.MaxResponseBytes+1))
	if err != nil || int64(len(payload)) > r.config.MaxResponseBytes {
		writeRelayError(writer, http.StatusBadGateway)
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if contentType != "application/json" {
		writeRelayError(writer, http.StatusBadGateway)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(response.StatusCode)
	_, _ = writer.Write(payload)
}

func validBridgeRequest(request *http.Request) bool {
	if request.URL.RawQuery != "" || request.URL.Fragment != "" || request.Header.Get("Content-Encoding") != "" ||
		len(request.Header.Values("Authorization")) != 1 || request.Header.Get("Authorization") == "" {
		return false
	}
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/v1/manifest":
		return request.ContentLength <= 0 && len(request.TransferEncoding) == 0
	case request.Method == http.MethodPost && request.URL.Path == "/v1/execute":
		return len(request.Header.Values("Content-Type")) == 1 &&
			strings.ToLower(strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0])) == "application/json"
	default:
		return false
	}
}

func (r *Relay) Endpoint() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listener == nil {
		return ""
	}
	return "http://" + r.listener.Addr().String()
}

func (r *Relay) Errors() <-chan error { return r.errors }

func (r *Relay) Close(ctx context.Context) error {
	r.mu.Lock()
	server := r.server
	listener := r.listener
	r.server = nil
	r.listener = nil
	r.mu.Unlock()
	r.transport.CloseIdleConnections()
	if server == nil {
		return nil
	}
	var result error
	if listener != nil {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			result = errors.Join(result, err)
		}
	}
	if err := server.Shutdown(ctx); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
		result = errors.Join(result, err)
	}
	return result
}

func writeRelayError(writer http.ResponseWriter, status int) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_, _ = io.WriteString(writer, `{"error":"tool broker relay request failed"}`)
}
