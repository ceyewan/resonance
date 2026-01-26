package api

import (
	"github.com/ceyewan/resonance/api/gen/go/gateway/v1/gatewayv1connect"
	"github.com/gin-gonic/gin"
)

// RouteConfig 路由配置
type RouteConfig struct {
	RecoveryMiddleware        gin.HandlerFunc
	LoggerMiddleware          gin.HandlerFunc
	SlowQueryMiddleware       gin.HandlerFunc
	GlobalRateLimitMiddleware gin.HandlerFunc
	IPRateLimitMiddleware     gin.HandlerFunc
	UserRateLimitMiddleware   gin.HandlerFunc
}

// RouteOption 路由选项函数
type RouteOption func(*RouteConfig)

// WithRecovery 设置 Recovery 中间件
func WithRecovery(middleware gin.HandlerFunc) RouteOption {
	return func(cfg *RouteConfig) {
		cfg.RecoveryMiddleware = middleware
	}
}

// WithLogger 设置 Logger 中间件
func WithLogger(middleware gin.HandlerFunc) RouteOption {
	return func(cfg *RouteConfig) {
		cfg.LoggerMiddleware = middleware
	}
}

// WithSlowQuery 设置慢查询检测中间件
func WithSlowQuery(middleware gin.HandlerFunc) RouteOption {
	return func(cfg *RouteConfig) {
		cfg.SlowQueryMiddleware = middleware
	}
}

// WithGlobalRateLimit 设置全局限流中间件
func WithGlobalRateLimit(middleware gin.HandlerFunc) RouteOption {
	return func(cfg *RouteConfig) {
		cfg.GlobalRateLimitMiddleware = middleware
	}
}

// WithIPRateLimit 设置 IP 限流中间件
func WithIPRateLimit(middleware gin.HandlerFunc) RouteOption {
	return func(cfg *RouteConfig) {
		cfg.IPRateLimitMiddleware = middleware
	}
}

// WithUserRateLimit 设置用户限流中间件
func WithUserRateLimit(middleware gin.HandlerFunc) RouteOption {
	return func(cfg *RouteConfig) {
		cfg.UserRateLimitMiddleware = middleware
	}
}

// RegisterRoutes 注册路由到 Gin，使用路由分组和中间件
func (h *HTTPHandler) RegisterRoutes(router *gin.Engine, opts ...RouteOption) {
	cfg := &RouteConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// 创建公共路由组（不需要认证）
	publicGroup := router.Group("")
	if cfg.RecoveryMiddleware != nil {
		publicGroup.Use(cfg.RecoveryMiddleware)
	}
	if cfg.LoggerMiddleware != nil {
		publicGroup.Use(cfg.LoggerMiddleware)
	}
	if cfg.SlowQueryMiddleware != nil {
		publicGroup.Use(cfg.SlowQueryMiddleware)
	}
	if cfg.GlobalRateLimitMiddleware != nil {
		publicGroup.Use(cfg.GlobalRateLimitMiddleware)
	}
	if cfg.IPRateLimitMiddleware != nil {
		publicGroup.Use(cfg.IPRateLimitMiddleware)
	}

	// 注册公开路由（不需要认证）
	h.registerPublicRoutes(publicGroup)

	// 创建认证路由组（需要认证）
	authGroup := router.Group("")
	if cfg.RecoveryMiddleware != nil {
		authGroup.Use(cfg.RecoveryMiddleware)
	}
	if cfg.LoggerMiddleware != nil {
		authGroup.Use(cfg.LoggerMiddleware)
	}
	if cfg.SlowQueryMiddleware != nil {
		authGroup.Use(cfg.SlowQueryMiddleware)
	}
	if cfg.GlobalRateLimitMiddleware != nil {
		authGroup.Use(cfg.GlobalRateLimitMiddleware)
	}
	// 认证中间件
	authGroup.Use(h.authConfig.RequireAuth())
	if cfg.UserRateLimitMiddleware != nil {
		authGroup.Use(cfg.UserRateLimitMiddleware)
	}

	// 注册需要认证的路由
	h.registerAuthRoutes(authGroup)
}

// registerPublicRoutes 注册公开路由（不需要认证）
func (h *HTTPHandler) registerPublicRoutes(group *gin.RouterGroup) {
	// AuthService: Login, Register
	path, handler := gatewayv1connect.NewAuthServiceHandler(h)
	group.Any(path+"*any", gin.WrapH(handler))
}

// registerAuthRoutes 注册需要认证的路由
func (h *HTTPHandler) registerAuthRoutes(group *gin.RouterGroup) {
	// SessionService: 所有接口都需要认证
	path, handler := gatewayv1connect.NewSessionServiceHandler(h)
	group.Any(path+"*any", gin.WrapH(handler))
}
