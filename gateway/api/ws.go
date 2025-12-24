package api

import (
	"context"
	"net/http"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/idgen"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/gateway/client"
	"github.com/ceyewan/resonance/gateway/connection"
	"github.com/ceyewan/resonance/gateway/middleware"
	"github.com/ceyewan/resonance/gateway/protocol"
	"github.com/gorilla/websocket"
)

// Handler 处理 WebSocket 连接相关的逻辑
type WebSocket struct {
	logicClient *client.Client
	connMgr     *connection.Manager
	logger      clog.Logger
	upgrader    *websocket.Upgrader
	config      *WSConfig
	idgen       idgen.Generator
}

// WSConfig WebSocket 配置
type WSConfig struct {
	ReadBufferSize int
	WriteBufferSize int
	MaxMessageSize  int64
	PingInterval    int // 秒
	PongTimeout     int // 秒
}

// DefaultWSConfig 默认 WebSocket 配置
func DefaultWSConfig() *WSConfig {
	return &WSConfig{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		MaxMessageSize:  512,  // 512KB
		PingInterval:    30,   // 30秒
		PongTimeout:     60,   // 60秒
	}
}

// NewWebSocket 创建 WebSocket 处理器
func NewWebSocket(
	logicClient *client.Client,
	logger clog.Logger,
	connMgr *connection.Manager,
	cfg *WSConfig,
	idgen idgen.Generator,
) *WebSocket {
	if cfg == nil {
		cfg = DefaultWSConfig()
	}

	upgrader := &websocket.Upgrader{
		ReadBufferSize:  cfg.ReadBufferSize,
		WriteBufferSize: cfg.WriteBufferSize,
		CheckOrigin: func(r *http.Request) bool {
			// 生产环境需要严格检查 Origin
			return true
		},
	}

	return &WebSocket{
		logicClient: logicClient,
		logger:      logger,
		connMgr:     connMgr,
		upgrader:    upgrader,
		config:      cfg,
		idgen:       idgen,
	}
}

// HandleWebSocket 处理 WebSocket 连接请求
// 从 URL 参数中获取 token 进行认证
func (ws *WebSocket) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		ws.logger.Warn("websocket connection rejected: missing token",
			clog.String("remote_addr", r.RemoteAddr))
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}

	// 验证 token
	validateResp, err := ws.logicClient.ValidateToken(r.Context(), token)
	if err != nil || !validateResp.Valid {
		ws.logger.Warn("websocket connection rejected: invalid token",
			clog.String("remote_addr", r.RemoteAddr),
			clog.Error(err))
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	username := validateResp.Username

	// 获取或生成 trace_id（会话级，整个 WebSocket 连接共用）
	traceID := r.Header.Get(middleware.TraceIDHeader)
	if traceID == "" && ws.idgen != nil {
		traceID = ws.idgen.Next()
	}

	// 升级为 WebSocket 连接
	wsConn, err := ws.upgrader.Upgrade(w, r, nil)
	if err != nil {
		ws.logger.Error("failed to upgrade websocket",
			clog.String("username", username),
			clog.String("remote_addr", r.RemoteAddr),
			clog.Error(err))
		return
	}

	// 创建协议处理器
	handler := protocol.NewDefaultHandler(
		ws.logger,
		ws.onPulse,
		ws.onChat,
		ws.onAck,
	)

	// 创建连接
	conn := connection.NewConn(
		username,
		traceID,
		wsConn,
		ws.logger,
		handler,
		int64(ws.config.MaxMessageSize*1024),
		time.Duration(ws.config.PingInterval)*time.Second,
		time.Duration(ws.config.PongTimeout)*time.Second,
	)

	// 添加到连接管理器（会触发上线回调）
	if err := ws.connMgr.AddConnection(username, conn); err != nil {
		ws.logger.Error("failed to add connection",
			clog.String("username", username),
			clog.Error(err))
		conn.Close()
		return
	}

	// 启动连接的读写协程
	conn.Run()

	ws.logger.Info("websocket connection established",
		clog.String("username", username),
		clog.String("trace_id", traceID),
		clog.String("remote_addr", r.RemoteAddr))
}

// onPulse 处理心跳消息
func (ws *WebSocket) onPulse(ctx context.Context, conn protocol.Connection) error {
	packet := protocol.CreatePulseResponse("")
	return conn.Send(packet)
}

// onChat 处理聊天消息
func (ws *WebSocket) onChat(ctx context.Context, conn protocol.Connection, chat *gatewayv1.ChatRequest) error {
	// 填充发送者
	if chat.FromUsername == "" {
		chat.FromUsername = conn.Username()
	}
	// 填充时间戳
	if chat.Timestamp == 0 {
		chat.Timestamp = time.Now().Unix()
	}

	// 调用 Logic 服务处理消息
	_, err := ws.logicClient.SendMessage(ctx, chat)
	return err
}

// onAck 处理确认消息
func (ws *WebSocket) onAck(ctx context.Context, conn protocol.Connection, ack *gatewayv1.Ack) error {
	// TODO: 实现消息确认逻辑
	// 当前不需要特殊处理，心跳已经表明连接活跃
	return nil
}

// Upgrader 返回 WebSocket 升级器
// 用于 connection.Manager 初始化
func (ws *WebSocket) Upgrader() *websocket.Upgrader {
	return ws.upgrader
}
