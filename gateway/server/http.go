package server

import (
	"context"
	"net/http"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/resonance/gateway/config"
	"github.com/ceyewan/resonance/gateway/handler"
	"github.com/gin-gonic/gin"
)

// HTTPServer HTTP 服务包装器
type HTTPServer struct {
	config      *config.Config
	logger      clog.Logger
	handler     *handler.Handler
	middlewares *handler.Middlewares
	server      *http.Server
}

// NewHTTPServer 创建 HTTP 服务
func NewHTTPServer(cfg *config.Config, logger clog.Logger, h *handler.Handler, m *handler.Middlewares) *HTTPServer {
	return &HTTPServer{
		config:      cfg,
		logger:      logger,
		handler:     h,
		middlewares: m,
	}
}

// Start 启动 HTTP 服务
func (s *HTTPServer) Start() error {
	router := gin.New()

	// 应用中间件（CORS 必须在最前）
	router.Use(s.middlewares.CORS)
	router.Use(s.middlewares.Recovery)
	router.Use(s.middlewares.Logger)
	router.Use(s.middlewares.SlowQuery)
	router.Use(s.middlewares.GlobalIP)

	// 注册 API 路由
	s.handler.RegisterRoutes(router, s.middlewares.RouteOptions()...)

	// 健康检查
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	})

	s.server = &http.Server{
		Addr:    s.config.GetHTTPAddr(),
		Handler: router,
	}

	s.logger.Info("http server started", clog.String("addr", s.config.GetHTTPAddr()))
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Stop 停止 HTTP 服务
func (s *HTTPServer) Stop(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}
