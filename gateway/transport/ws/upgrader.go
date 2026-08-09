package ws

import (
	"net/http"

	"github.com/ceyewan/genesis/clog"
	"github.com/gorilla/websocket"

	"github.com/ceyewan/resonance/gateway/config"
	"github.com/ceyewan/resonance/gateway/middleware"
	"github.com/ceyewan/resonance/pkg/userauth"
)

// Upgrader 处理 WebSocket 连接握手
type Upgrader struct {
	logger     clog.Logger
	connMgr    *Manager
	dispatcher *Dispatcher
	upgrader   *websocket.Upgrader
	config     config.WSConfig
}

// NewUpgrader 创建 WebSocket 升级器
func NewUpgrader(
	logger clog.Logger,
	connMgr *Manager,
	dispatcher *Dispatcher,
	cfg config.WSConfig,
) *Upgrader {
	upgrader := &websocket.Upgrader{
		ReadBufferSize:  cfg.ReadBufferSize,
		WriteBufferSize: cfg.WriteBufferSize,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	return &Upgrader{
		logger:     logger,
		connMgr:    connMgr,
		dispatcher: dispatcher,
		upgrader:   upgrader,
		config:     cfg,
	}
}

// HandleWebSocket 处理握手请求
func (h *Upgrader) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	username, _ := middleware.UsernameFromRequestContext(r.Context())
	if username == "" {
		h.logger.Warn("websocket connection rejected: missing username", clog.String("remote_addr", r.RemoteAddr))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	traceID, _ := middleware.TraceIDFromContext(r.Context())
	if traceID == "" {
		traceID = r.Header.Get(middleware.TraceIDHeader)
	}
	principal, ok := userauth.PrincipalFromContext(r.Context())
	if !ok {
		h.logger.Warn("websocket connection rejected: missing verified principal", clog.String("remote_addr", r.RemoteAddr))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if traceID == "" {
		// 使用 OTEL 生成的 TraceID
		traceID = middleware.GetTraceID(r.Context())
	}

	// 升级连接
	wsConn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("failed to upgrade websocket", clog.String("username", username), clog.Error(err))
		return
	}

	// 封装协议处理器 (使用分发器)
	protoHandler := NewDefaultHandler(
		h.logger,
		h.dispatcher.HandlePulse,
		h.dispatcher.HandleChat,
		h.dispatcher.HandleAck,
	)

	// 创建连接对象
	conn := NewConn(
		username,
		traceID,
		wsConn,
		h.logger,
		protoHandler,
		int64(h.config.MaxMessageSize*1024),
		h.config.GetPingInterval(),
		h.config.GetPongTimeout(),
		WithUserPrincipal(principal),
	)

	// 管理连接
	if err := h.connMgr.AddConnection(username, conn); err != nil {
		h.logger.Error("failed to add connection", clog.String("username", username), clog.Error(err))
		_ = conn.Close()
		return
	}

	conn.Run()
	h.logger.Info("websocket connection established", clog.String("username", username), clog.String("trace_id", traceID))
}

// Upgrader 获取升级器
func (h *Upgrader) Upgrader() *websocket.Upgrader {
	return h.upgrader
}
