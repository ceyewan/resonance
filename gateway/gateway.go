package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/ceyewan/genesis/clog"
	gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
	"github.com/ceyewan/resonance/gateway/api"
	"github.com/ceyewan/resonance/gateway/client"
	"github.com/ceyewan/resonance/gateway/connection"
	"github.com/ceyewan/resonance/gateway/protocol"
	"github.com/ceyewan/resonance/gateway/push"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
)

// Gateway 网关服务
type Gateway struct {
	config      *Config
	logger      clog.Logger
	logicClient *client.LogicClient
	connMgr     *connection.Manager
	apiHandler  *api.Handler
	pushService *push.Service
	httpServer  *http.Server
	wsServer    *http.Server
	grpcServer  *grpc.Server
	ctx         context.Context
	cancel      context.CancelFunc
}

// New 创建 Gateway 实例
func New(cfg *Config) (*Gateway, error) {
	// 初始化日志
	logger, err := clog.New(&cfg.Log)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	g := &Gateway{
		config: cfg,
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
	}

	// 初始化 Logic 客户端
	var logicClient *client.LogicClient
	logicClient, err = client.New(cfg.LogicAddr, cfg.GatewayID, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create logic client: %w", err)
	}
	g.logicClient = logicClient

	// 初始化 WebSocket 升级器
	upgrader := &websocket.Upgrader{
		ReadBufferSize:  cfg.WSConfig.ReadBufferSize,
		WriteBufferSize: cfg.WSConfig.WriteBufferSize,
		CheckOrigin: func(r *http.Request) bool {
			return true // 生产环境需要严格检查
		},
	}

	// 初始化连接管理器
	g.connMgr = connection.NewManager(
		logger,
		upgrader,
		g.onUserConnect,
		g.onUserDisconnect,
	)

	// 初始化 API Handler
	g.apiHandler = api.NewHandler(logicClient, logger)

	// 初始化 Push Service
	g.pushService = push.NewService(g.connMgr, logger)

	return g, nil
}

// Run 启动 Gateway 服务
func (g *Gateway) Run() error {
	g.logger.Info("starting gateway service",
		clog.String("gateway_id", g.config.GatewayID),
		clog.String("http_addr", g.config.HTTPAddr),
		clog.String("ws_addr", g.config.WSAddr),
		clog.String("grpc_addr", ":9091"))

	// 启动 gRPC 服务（PushService）
	go g.startGRPCServer()

	// 启动 HTTP 服务（RESTful API）
	go g.startHTTPServer()

	// 启动 WebSocket 服务
	go g.startWSServer()

	return nil
}

// startGRPCServer 启动 gRPC 服务
func (g *Gateway) startGRPCServer() {
	g.grpcServer = grpc.NewServer()

	// 注册 PushService
	g.pushService.RegisterGRPC(g.grpcServer)

	// 创建监听器
	lis, err := net.Listen("tcp", ":9091")
	if err != nil {
		g.logger.Error("failed to listen on grpc port", clog.Error(err))
		return
	}

	g.logger.Info("grpc server started", clog.String("addr", ":9091"))
	if err := g.grpcServer.Serve(lis); err != nil {
		g.logger.Error("grpc server error", clog.Error(err))
	}
}

// startHTTPServer 启动 HTTP 服务
func (g *Gateway) startHTTPServer() {
	router := gin.Default()

	// 注册 RESTful API 路由（ConnectRPC）
	g.apiHandler.RegisterRoutes(router)

	// 健康检查
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":       "ok",
			"gateway_id":   g.config.GatewayID,
			"online_count": g.connMgr.OnlineCount(),
		})
	})

	g.httpServer = &http.Server{
		Addr:    g.config.HTTPAddr,
		Handler: router,
	}

	g.logger.Info("http server started", clog.String("addr", g.config.HTTPAddr))
	if err := g.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		g.logger.Error("http server error", clog.Error(err))
	}
}

// startWSServer 启动 WebSocket 服务
func (g *Gateway) startWSServer() {
	mux := http.NewServeMux()

	// WebSocket 连接端点
	mux.HandleFunc("/ws", g.handleWebSocket)

	g.wsServer = &http.Server{
		Addr:    g.config.WSAddr,
		Handler: mux,
	}

	g.logger.Info("websocket server started", clog.String("addr", g.config.WSAddr))
	if err := g.wsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		g.logger.Error("websocket server error", clog.Error(err))
	}
}

// handleWebSocket 处理 WebSocket 连接请求
func (g *Gateway) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// 从查询参数获取 token
	token := r.URL.Query().Get("token")
	if token == "" {
		g.logger.Warn("websocket connection rejected: missing token")
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}

	// 验证 token
	validateResp, err := g.logicClient.ValidateToken(r.Context(), token)
	if err != nil || !validateResp.Valid {
		g.logger.Warn("websocket connection rejected: invalid token", clog.Error(err))
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	username := validateResp.Username

	// 升级为 WebSocket 连接
	wsConn, err := g.connMgr.Upgrader().Upgrade(w, r, nil)
	if err != nil {
		g.logger.Error("failed to upgrade websocket", clog.Error(err))
		return
	}

	// 创建消息处理器
	handler := g.createMessageHandler()

	// 创建连接对象
	conn := connection.NewConn(
		username,
		wsConn,
		g.logger,
		handler,
		int64(g.config.WSConfig.MaxMessageSize),
		time.Duration(g.config.WSConfig.PingInterval)*time.Second,
		time.Duration(g.config.WSConfig.PongTimeout)*time.Second,
	)

	// 添加到连接管理器
	if err := g.connMgr.AddConnection(username, conn); err != nil {
		g.logger.Error("failed to add connection", clog.Error(err))
		conn.Close()
		return
	}

	// 启动连接的读写协程
	conn.Run()

	g.logger.Info("websocket connection established",
		clog.String("username", username),
		clog.String("remote_addr", r.RemoteAddr))
}

// createMessageHandler 创建消息处理器
func (g *Gateway) createMessageHandler() protocol.Handler {
	return protocol.NewDefaultHandler(
		g.logger,
		g.onPulse,
		g.onChat,
		g.onAck,
	)
}

// onPulse 处理心跳消息
func (g *Gateway) onPulse(ctx context.Context, conn protocol.Connection) error {
	// 回复心跳
	packet := protocol.CreatePulseResponse("")
	return conn.Send(packet)
}

// onChat 处理聊天消息
func (g *Gateway) onChat(ctx context.Context, conn protocol.Connection, chat *gatewayv1.ChatRequest) error {
	g.logger.Debug("received chat message",
		clog.String("username", conn.Username()),
		clog.String("session_id", chat.SessionId))

	// 填充发送者信息
	if chat.FromUsername == "" {
		chat.FromUsername = conn.Username()
	}

	// 填充时间戳
	if chat.Timestamp == 0 {
		chat.Timestamp = time.Now().Unix()
	}

	// 转发到 Logic 服务
	_, err := g.logicClient.SendMessage(ctx, chat)
	if err != nil {
		g.logger.Error("failed to send message to logic",
			clog.String("username", conn.Username()),
			clog.Error(err))
		return err
	}

	return nil
}

// onAck 处理确认消息
func (g *Gateway) onAck(ctx context.Context, conn protocol.Connection, ack *gatewayv1.Ack) error {
	g.logger.Debug("received ack",
		clog.String("username", conn.Username()),
		clog.String("ref_seq", ack.RefSeq))
	// 这里可以添加消息确认的业务逻辑
	return nil
}

// onUserConnect 用户上线回调
func (g *Gateway) onUserConnect(username string, remoteIP string) error {
	g.logger.Info("user online", clog.String("username", username))
	return g.logicClient.SyncUserOnline(g.ctx, username, remoteIP)
}

// onUserDisconnect 用户下线回调
func (g *Gateway) onUserDisconnect(username string) error {
	g.logger.Info("user offline", clog.String("username", username))
	return g.logicClient.SyncUserOffline(g.ctx, username)
}

// Close 关闭 Gateway 服务
func (g *Gateway) Close() error {
	g.logger.Info("shutting down gateway service")

	g.cancel()

	// 关闭 gRPC 服务器
	if g.grpcServer != nil {
		g.grpcServer.GracefulStop()
	}

	// 关闭 HTTP 服务器
	if g.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := g.httpServer.Shutdown(ctx); err != nil {
			g.logger.Error("failed to shutdown http server", clog.Error(err))
		}
	}

	// 关闭 WebSocket 服务器
	if g.wsServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := g.wsServer.Shutdown(ctx); err != nil {
			g.logger.Error("failed to shutdown ws server", clog.Error(err))
		}
	}

	// 关闭所有连接
	if g.connMgr != nil {
		g.connMgr.Close()
	}

	// 关闭 Logic 客户端
	if g.logicClient != nil {
		g.logicClient.Close()
	}

	g.logger.Info("gateway service stopped")
	return nil
}
