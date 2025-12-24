package api

import (
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/ratelimit"
	"github.com/ceyewan/resonance/gateway/client"
	"github.com/ceyewan/resonance/gateway/connection"
	"github.com/ceyewan/resonance/gateway/middleware"
	"github.com/gin-gonic/gin"
)

// Middlewares HTTP 中间件集合
type Middlewares struct {
	Recovery       gin.HandlerFunc
	Logger         gin.HandlerFunc
	SlowQuery      gin.HandlerFunc
	GlobalIP       gin.HandlerFunc
	IPBased        gin.HandlerFunc
	UserBased      gin.HandlerFunc
	limiter        ratelimit.Limiter
	rateLimitCfg   *middleware.RateLimitConfig
}

// NewMiddlewares 创建所有 HTTP 中间件
func NewMiddlewares(logger clog.Logger, limiter ratelimit.Limiter, idgen idgen.Generator) *Middlewares {
	rateLimitCfg := middleware.NewRateLimitConfig(limiter, logger)

	return &Middlewares{
		Recovery:     middleware.Recovery(logger),
		Logger:       middleware.Logger(logger, idgen),
		SlowQuery:    middleware.SlowQueryDetector(logger, 2*time.Second),
		limiter:      limiter,
		rateLimitCfg: rateLimitCfg,
		GlobalIP:     rateLimitCfg.GlobalIP(ratelimit.Limit{Rate: 1000, Burst: 2000}),
		IPBased: rateLimitCfg.IPBased(
			middleware.PredefinedRateLimits.AuthIPLimits,
			middleware.PredefinedRateLimits.DefaultLimit,
		),
		UserBased: rateLimitCfg.UserBased(
			middleware.PredefinedRateLimits.SessionUserLimits,
			middleware.PredefinedRateLimits.DefaultLimit,
		),
	}
}

// RouteOptions 返回 RegisterRoutes 所需的选项
func (m *Middlewares) RouteOptions() []RouteOption {
	return []RouteOption{
		WithRecovery(m.Recovery),
		WithLogger(m.Logger),
		WithGlobalRateLimit(m.GlobalIP),
		WithIPRateLimit(m.IPBased),
		WithUserRateLimit(m.UserBased),
	}
}

// NewHandlerWithMiddlewares 创建 Handler 并同时初始化中间件
func NewHandlerWithMiddlewares(logicClient *client.Client, logger clog.Logger, idgen idgen.Generator) (*Handler, *Middlewares, error) {
	// 复用 client 中的 limiter
	limiter, err := ratelimit.NewStandalone(nil, ratelimit.WithLogger(logger))
	if err != nil {
		return nil, nil, err
	}

	m := NewMiddlewares(logger, limiter, idgen)
	h := NewHandler(logicClient, logger)

	return h, m, nil
}

// NewWebSocketComponents 创建 WebSocket 相关组件
// 返回: WebSocket 处理器、连接管理器
func NewWebSocketComponents(logicClient *client.Client, logger clog.Logger, idgen idgen.Generator) (*WebSocket, *connection.Manager) {
	// 创建上下线回调
	presence := connection.NewPresenceCallback(logicClient, logger)

	// 创建连接管理器（带上下线回调）
	connMgr := connection.NewManager(
		logger,
		nil, // Upgrader 稍后由 WebSocket 设置
		presence.OnUserOnline,
		presence.OnUserOffline,
	)

	// 创建 WebSocket 处理器
	wsHandler := NewWebSocket(logicClient, logger, connMgr, DefaultWSConfig(), idgen)

	// 设置 Upgrader 到 Manager
	connMgr.SetUpgrader(wsHandler.Upgrader())

	return wsHandler, connMgr
}
