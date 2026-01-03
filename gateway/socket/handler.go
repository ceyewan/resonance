package socket

import (
	"net/http"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/resonance/gateway/config"
	"github.com/ceyewan/resonance/gateway/connection"
	"github.com/ceyewan/resonance/gateway/middleware"
	"github.com/ceyewan/resonance/gateway/protocol"
	"github.com/gorilla/websocket"
)

// Handler 处理 WebSocket 连接握手和生命周期
type Handler struct {
	logger      clog.Logger
	connMgr     *connection.Manager
	dispatcher  *Dispatcher
	upgrader    *websocket.Upgrader
	idgen       idgen.Generator
	config      config.WSConfig
}

// NewHandler 创建 WebSocket 处理器
func NewHandler(
	logger clog.Logger,
	connMgr *connection.Manager,
	dispatcher *Dispatcher,
	idgen idgen.Generator,
	cfg config.WSConfig,
) *Handler {
	upgrader := &websocket.Upgrader{
		ReadBufferSize:  cfg.ReadBufferSize,
		WriteBufferSize: cfg.WriteBufferSize,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	return &Handler{
		logger:      logger,
		connMgr:     connMgr,
		dispatcher:  dispatcher,
		upgrader:    upgrader,
		idgen:       idgen,
		config:      cfg,
	}
}

// HandleWebSocket 处理握手请求
func (h *Handler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	username, _ := r.Context().Value(middleware.UsernameKey).(string)
	if username == "" {
		h.logger.Warn("websocket connection rejected: missing username", clog.String("remote_addr", r.RemoteAddr))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	traceID, _ := r.Context().Value(middleware.TraceIDKey).(string)
	if traceID == "" {
		traceID = r.Header.Get(middleware.TraceIDHeader)
	}
	if traceID == "" && h.idgen != nil {
		traceID = h.idgen.Next()
	}

	// 升级连接
	wsConn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("failed to upgrade websocket", clog.String("username", username), clog.Error(err))
		return
	}

	// 封装协议处理器 (使用分发器)
	protoHandler := protocol.NewDefaultHandler(
		h.logger,
		h.dispatcher.HandlePulse,
		h.dispatcher.HandleChat,
		h.dispatcher.HandleAck,
	)

	// 创建连接对象
	conn := connection.NewConn(
		username,
		traceID,
		wsConn,
		h.logger,
		protoHandler,
		int64(h.config.MaxMessageSize*1024),
		time.Duration(h.config.PingInterval)*time.Second,
		time.Duration(h.config.PongTimeout)*time.Second,
	)

	// 管理连接
	if err := h.connMgr.AddConnection(username, conn); err != nil {
		h.logger.Error("failed to add connection", clog.String("username", username), clog.Error(err))
		conn.Close()
		return
	}

	conn.Run()
	h.logger.Info("websocket connection established", clog.String("username", username), clog.String("trace_id", traceID))
}

// Upgrader 获取升级器
func (h *Handler) Upgrader() *websocket.Upgrader {
	return h.upgrader
}
