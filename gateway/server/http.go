package server

import (
	"context"
	"net/http"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/resonance/gateway/config"
	"github.com/ceyewan/resonance/gateway/handler"
	"github.com/ceyewan/resonance/gateway/socket"
	"github.com/gin-gonic/gin"
)

// HTTPServer HTTP 服务包装器
type HTTPServer struct {
	config      *config.Config
	logger      clog.Logger
	handler     *handler.Handler
	middlewares *handler.Middlewares
	wsHandler   *socket.Handler
	server      *http.Server
}

// NewHTTPServer 创建 HTTP 服务
func NewHTTPServer(cfg *config.Config, logger clog.Logger, h *handler.Handler, m *handler.Middlewares, wsHandler *socket.Handler) *HTTPServer {
	return &HTTPServer{
		config:      cfg,
		logger:      logger,
		handler:     h,
		middlewares: m,
		wsHandler:   wsHandler,
	}
}

// Start 启动 HTTP 服务
func (s *HTTPServer) Start() error {
	router := gin.New()

	// 应用中间件（CORS 必须在最前）
	router.Use(s.middlewares.CORS)
	router.Use(s.middlewares.Recovery)

	// 注册 API 路由
	s.handler.RegisterRoutes(router, s.middlewares.RouteOptions()...)

	// WebSocket 路由（复用 HTTP 端口，使用精简中间件）
	wsGroup := router.Group("")
	wsGroup.Use(s.middlewares.Logger)
	wsGroup.Use(s.middlewares.GlobalIP)
	wsGroup.Use(s.handler.RequireAuthMiddleware())
	wsGroup.GET("/ws", func(c *gin.Context) {
		s.wsHandler.HandleWebSocket(c.Writer, c.Request)
	})

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
