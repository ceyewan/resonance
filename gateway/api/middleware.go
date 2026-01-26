package api

import (
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/idgen"
	"github.com/ceyewan/genesis/ratelimit"
	"github.com/ceyewan/resonance/gateway/middleware"
	"github.com/gin-gonic/gin"
)

// Middlewares HTTP 中间件集合
type Middlewares struct {
	Recovery     gin.HandlerFunc
	CORS         gin.HandlerFunc
	Logger       gin.HandlerFunc
	SlowQuery    gin.HandlerFunc
	GlobalIP     gin.HandlerFunc
	IPBased      gin.HandlerFunc
	UserBased    gin.HandlerFunc
	limiter      ratelimit.Limiter
	rateLimitCfg *middleware.RateLimitConfig
}

// NewMiddlewares 创建所有 HTTP 中间件
func NewMiddlewares(logger clog.Logger, limiter ratelimit.Limiter, idgen idgen.Generator) *Middlewares {
	rateLimitCfg := middleware.NewRateLimitConfig(limiter, logger)

	return &Middlewares{
		Recovery:     middleware.Recovery(logger),
		CORS:         middleware.CORS(),
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
		WithSlowQuery(m.SlowQuery),
		WithGlobalRateLimit(m.GlobalIP),
		WithIPRateLimit(m.IPBased),
		WithUserRateLimit(m.UserBased),
	}
}
