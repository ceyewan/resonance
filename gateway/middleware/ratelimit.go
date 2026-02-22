package middleware

import (
	"fmt"
	"net/http"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/ratelimit"
	"github.com/gin-gonic/gin"
)

// RateLimitConfig 限流中间件配置
type RateLimitConfig struct {
	limiter ratelimit.Limiter
	logger  clog.Logger
}

// NewRateLimitConfig 创建限流配置
func NewRateLimitConfig(limiter ratelimit.Limiter, logger clog.Logger) *RateLimitConfig {
	return &RateLimitConfig{
		limiter: limiter,
		logger:  logger,
	}
}

// IPBased 基于路径的 IP 限流中间件
// 不同路径有不同的限流规则，适用于注册登录等公开接口
func (r *RateLimitConfig) IPBased(pathLimits map[string]ratelimit.Limit, defaultLimit ratelimit.Limit) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取当前路径的限流规则
		limit, ok := pathLimits[c.FullPath()]
		if !ok {
			limit = defaultLimit
		}

		// 使用 IP 作为限流键
		key := fmt.Sprintf("ip:%s:path:%s", c.ClientIP(), c.FullPath())

		allowed, err := r.limiter.Allow(c.Request.Context(), key, limit)
		if err != nil {
			r.logger.Error("ratelimit check failed", clog.Error(err))
			// 降级：限流器出错时放行
			c.Next()
			return
		}

		if !allowed {
			r.logger.Warn("rate limit exceeded (IP-based)",
				clog.String("client_ip", c.ClientIP()),
				clog.String("path", c.FullPath()),
			)
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
			})
			return
		}

		c.Next()
	}
}

// UserBased 基于用户的限流中间件
// 必须在 Auth 中间件之后使用，从上下文获取用户名进行限流
// 适用于 Session 操作等需要认证的接口
func (r *RateLimitConfig) UserBased(pathLimits map[string]ratelimit.Limit, defaultLimit ratelimit.Limit) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取用户名
		username, ok := GetUsername(c)
		if !ok {
			// 如果没有用户信息，说明认证中间件没有通过或不应该使用此中间件
			r.logger.Warn("user-based ratelimit used without auth middleware",
				clog.String("path", c.FullPath()),
			)
			c.Next()
			return
		}

		// 获取当前路径的限流规则
		limit, ok := pathLimits[c.FullPath()]
		if !ok {
			limit = defaultLimit
		}

		// 使用用户名作为限流键
		key := fmt.Sprintf("user:%s:path:%s", username, c.FullPath())

		allowed, err := r.limiter.Allow(c.Request.Context(), key, limit)
		if err != nil {
			r.logger.Error("ratelimit check failed", clog.Error(err))
			// 降级：限流器出错时放行
			c.Next()
			return
		}

		if !allowed {
			r.logger.Warn("rate limit exceeded (User-based)",
				clog.String("username", username),
				clog.String("path", c.FullPath()),
			)
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
			})
			return
		}

		c.Next()
	}
}

// GlobalIP 全局 IP 限流中间件
// 防止 DDoS 攻击，所有请求共享一个限流池
func (r *RateLimitConfig) GlobalIP(limit ratelimit.Limit) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := fmt.Sprintf("global_ip:%s", c.ClientIP())

		allowed, err := r.limiter.Allow(c.Request.Context(), key, limit)
		if err != nil {
			r.logger.Error("global ratelimit check failed", clog.Error(err))
			c.Next()
			return
		}

		if !allowed {
			r.logger.Warn("global rate limit exceeded",
				clog.String("client_ip", c.ClientIP()),
			)
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
			})
			return
		}

		c.Next()
	}
}

// PredefinedRateLimits 预定义的限流规则
var PredefinedRateLimits = struct {
	// 认证相关接口（IP 级别限流）
	AuthIPLimits map[string]ratelimit.Limit
	// Session 相关接口（用户级别限流）
	SessionUserLimits map[string]ratelimit.Limit
	// 默认限流规则
	DefaultLimit ratelimit.Limit
}{
	AuthIPLimits: map[string]ratelimit.Limit{
		// ConnectRPC 路径
		"/gateway.v1.AuthService/Login": {
			Rate:  10, // 登录：10 QPS (防暴力破解)
			Burst: 20,
		},
		"/gateway.v1.AuthService/Register": {
			Rate:  5, // 注册：5 QPS (防刷注册)
			Burst: 10,
		},
	},
	SessionUserLimits: map[string]ratelimit.Limit{
		// Session 操作（用户级别限流）
		"/gateway.v1.SessionService/GetSessionList": {
			Rate:  50, // 获取会话列表
			Burst: 100,
		},
		"/gateway.v1.SessionService/CreateSession": {
			Rate:  10, // 创建会话
			Burst: 20,
		},
		"/gateway.v1.SessionService/GetHistoryMessages": {
			Rate:  100, // 获取历史消息
			Burst: 200,
		},
		"/gateway.v1.SessionService/GetContactList": {
			Rate:  50, // 获取联系人列表
			Burst: 100,
		},
		"/gateway.v1.SessionService/SearchUser": {
			Rate:  20, // 搜索用户
			Burst: 50,
		},
	},
	DefaultLimit: ratelimit.Limit{
		Rate:  100,
		Burst: 200,
	},
}
