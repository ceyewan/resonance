package health

import (
	"context"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ceyewan/genesis/clog"
)

// Probe 维护健康检查状态，可挂载到任意 HTTP 路由。
type Probe struct {
	ready    atomic.Bool
	shutdown atomic.Bool
}

// NewProbe 创建健康探针状态。
func NewProbe() *Probe {
	return &Probe{}
}

// SetReady 设置服务就绪状态。
func (p *Probe) SetReady(ready bool) {
	p.ready.Store(ready)
}

// SetShutdown 设置服务关闭状态。
func (p *Probe) SetShutdown(shutdown bool) {
	p.shutdown.Store(shutdown)
}

// LivenessHandler 返回 liveness handler（/health）。
func (p *Probe) LivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	}
}

// ReadinessHandler 返回 readiness handler（/ready）。
func (p *Probe) ReadinessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if !p.ready.Load() || p.shutdown.Load() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"not_ready"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	}
}

// Server 轻量级独立健康检查 HTTP 服务（用于无业务 HTTP 端口的模块）。
type Server struct {
	logger clog.Logger
	probe  *Probe
	server *http.Server

	mu      sync.Mutex
	started bool
}

// NewServer 创建健康检查服务器。
func NewServer(addr string, logger clog.Logger) *Server {
	probe := NewProbe()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", probe.LivenessHandler())
	mux.HandleFunc("/ready", probe.ReadinessHandler())

	return &Server{
		logger: logger,
		probe:  probe,
		server: &http.Server{
			Addr:         addr,
			Handler:      mux,
			ReadTimeout:  2 * time.Second,
			WriteTimeout: 2 * time.Second,
			IdleTimeout:  5 * time.Second,
		},
	}
}

// Probe 返回探针状态对象。
func (s *Server) Probe() *Probe {
	return s.probe
}

// SetReady 设置服务就绪状态。
func (s *Server) SetReady(ready bool) {
	s.probe.SetReady(ready)
}

// Start 启动健康检查服务器。
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return nil
	}

	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return err
	}

	s.started = true
	s.logger.Info("health server starting", clog.String("addr", s.server.Addr))
	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error("health server failed", clog.Error(err))
		}
	}()

	return nil
}

// Stop 停止健康检查服务器。
func (s *Server) Stop(ctx context.Context) error {
	s.probe.SetShutdown(true)
	return s.server.Shutdown(ctx)
}
