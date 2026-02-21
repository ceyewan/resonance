package webserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/resonance/pkg/health"
	webcfg "github.com/ceyewan/resonance/webserver/config"
)

// Web 负责托管静态前端资源
type Web struct {
	config    *webcfg.Config
	logger    clog.Logger
	server    *http.Server
	health    *health.Probe
	distDir   string
	indexPath string
}

// New 创建 Web 模块实例
func New() (*Web, error) {
	cfg, err := webcfg.Load()
	if err != nil {
		return nil, err
	}

	logger, _ := clog.New(&cfg.Log, clog.WithTraceContext())

	dist := cfg.Static.GetDistDir()
	if err := validateDistDir(dist); err != nil {
		return nil, err
	}

	w := &Web{
		config:    cfg,
		logger:    logger,
		health:    health.NewProbe(),
		distDir:   dist,
		indexPath: filepath.Join(dist, cfg.Static.GetIndexFile()),
	}

	if _, err := os.Stat(w.indexPath); err != nil {
		return nil, fmt.Errorf("index file not found: %s", w.indexPath)
	}

	w.server = &http.Server{
		Addr:         cfg.Service.GetHTTPAddr(),
		Handler:      w.loggingMiddleware(w.staticHandler()),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return w, nil
}

// Run 启动 HTTP 服务
func (w *Web) Run() error {
	w.logger.Info("web server listening",
		clog.String("addr", w.server.Addr),
		clog.String("dist", w.distDir),
	)

	ln, err := net.Listen("tcp", w.server.Addr)
	if err != nil {
		return fmt.Errorf("listen tcp %s: %w", w.server.Addr, err)
	}
	w.health.SetShutdown(false)
	w.health.SetReady(true)

	go func() {
		if err := w.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			w.logger.Error("web server stopped unexpectedly", clog.Error(err))
		}
	}()
	return nil
}

// Close 优雅退出
func (w *Web) Close() error {
	w.health.SetReady(false)
	w.health.SetShutdown(true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return w.server.Shutdown(ctx)
}

func (w *Web) staticHandler() http.Handler {
	fileSystem := http.Dir(w.distDir)
	fileServer := http.FileServer(fileSystem)
	cacheControl := w.config.Static.GetCacheControl()

	serveIndex := func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(rw, r, w.indexPath)
	}

	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		// 健康检查端点
		if r.URL.Path == "/health" {
			w.health.LivenessHandler()(rw, r)
			return
		}
		if r.URL.Path == "/ready" {
			w.health.ReadinessHandler()(rw, r)
			return
		}
		if r.URL.Path == "/runtime-config.js" {
			if r.Method != http.MethodGet {
				http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			rw.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			rw.Header().Set("Cache-Control", "no-store")
			writeRuntimeConfig(rw)
			return
		}

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		requestPath := sanitizePath(r.URL.Path)
		if requestPath == "" {
			serveIndex(rw, r)
			return
		}

		if exists(fileSystem, requestPath) {
			if !strings.HasSuffix(requestPath, ".html") {
				rw.Header().Set("Cache-Control", cacheControl)
			} else {
				rw.Header().Set("Cache-Control", "no-cache")
			}
			fileServer.ServeHTTP(rw, r)
			return
		}

		if w.config.Static.EnableSPAFallback {
			serveIndex(rw, r)
			return
		}

		http.NotFound(rw, r)
	})
}

func writeRuntimeConfig(rw http.ResponseWriter) {
	payload := map[string]string{
		"apiBaseUrl": os.Getenv("RESONANCE_WEB_API_BASE_URL"),
		"wsBaseUrl":  os.Getenv("RESONANCE_WEB_WS_BASE_URL"),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		http.Error(rw, "failed to encode runtime config", http.StatusInternalServerError)
		return
	}
	_, _ = rw.Write([]byte("window.__RESONANCE_RUNTIME_CONFIG__ = "))
	_, _ = rw.Write(data)
	_, _ = rw.Write([]byte(";\n"))
}

func sanitizePath(requestPath string) string {
	clean := path.Clean(requestPath)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "." {
		return ""
	}
	return clean
}

func exists(fs http.FileSystem, requestPath string) bool {
	f, err := fs.Open(requestPath)
	if err != nil {
		return false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return false
	}

	// 对于目录，请尝试附加 index.html
	if info.IsDir() {
		_, err := fs.Open(path.Join(requestPath, "index.html"))
		return err == nil
	}

	return true
}

func validateDistDir(dist string) error {
	info, err := os.Stat(dist)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("dist directory does not exist: %s (请先运行 make web-build)", dist)
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("dist path must be a directory: %s", dist)
	}
	return nil
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.status = code
	rr.ResponseWriter.WriteHeader(code)
}

func (w *Web) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecorder{ResponseWriter: rw, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		w.logger.Info("http request",
			clog.String("method", r.Method),
			clog.String("path", r.URL.Path),
			clog.Int("status", rec.status),
			clog.Duration("duration", time.Since(start)),
			clog.String("remote_addr", r.RemoteAddr),
		)
	})
}
