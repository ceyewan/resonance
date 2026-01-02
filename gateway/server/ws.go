package server

import (
	"context"
	"net/http"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/resonance/gateway/config"
	"github.com/ceyewan/resonance/gateway/socket"
)

// WSServer WebSocket 服务包装器
type WSServer struct {
	config    *config.Config
	logger    clog.Logger
	wsHandler *socket.Handler
	server    *http.Server
}

// NewWSServer 创建 WebSocket 服务
func NewWSServer(cfg *config.Config, logger clog.Logger, wsHandler *socket.Handler) *WSServer {
	return &WSServer{
		config:    cfg,
		logger:    logger,
		wsHandler: wsHandler,
	}
}

// Start 启动 WebSocket 服务
func (s *WSServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.wsHandler.HandleWebSocket)

	s.server = &http.Server{
		Addr:    s.config.GetWSAddr(),
		Handler: mux,
	}

	s.logger.Info("websocket server started", clog.String("addr", s.config.GetWSAddr()))
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Stop 停止 WebSocket 服务
func (s *WSServer) Stop(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}
