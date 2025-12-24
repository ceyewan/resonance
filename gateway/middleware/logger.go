package middleware

import (
	"context"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/idgen"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Logger 返回一个请求日志中间件
// 记录请求方法、路径、状态码、耗时、客户端 IP 等
// 同时负责 trace_id 的生成和注入
func Logger(logger clog.Logger, idgen idgen.Generator) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 处理 trace_id
		traceID := c.GetHeader(TraceIDHeader)
		if traceID == "" {
			traceID = idgen.Next()
		}
		c.Set("trace_id", traceID)
		c.Header(TraceIDHeader, traceID)

		// 2. 注入 trace_id 到 Context
		ctx := context.WithValue(c.Request.Context(), TraceIDKey, traceID)
		c.Request = c.Request.WithContext(ctx)

		// 3. 生成请求 ID
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}
		c.Set("RequestID", requestID)
		c.Header("X-Request-ID", requestID)

		// 4. 记录开始时间
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		// 5. 处理请求
		c.Next()

		// 6. 计算耗时
		latency := time.Since(start)

		// 7. 构建日志字段
		fields := []clog.Field{
			clog.String("request_id", requestID),
			clog.String("method", c.Request.Method),
			clog.String("path", path),
			clog.String("query", query),
			clog.Int("status", c.Writer.Status()),
			clog.String("client_ip", c.ClientIP()),
			clog.String("user_agent", c.Request.UserAgent()),
			clog.Duration("latency", latency),
		}

		// 8. 如果有用户信息，记录用户名
		if username, exists := c.Get("username"); exists {
			fields = append(fields, clog.String("username", username.(string)))
		}

		// 9. 根据状态码选择日志级别
		// 使用 InfoContext 以便自动提取 Context 中的 trace_id
		switch {
		case c.Writer.Status() >= 500:
			logger.ErrorContext(ctx, "server error", fields...)
		case c.Writer.Status() >= 400:
			logger.WarnContext(ctx, "client error", fields...)
		default:
			logger.InfoContext(ctx, "request", fields...)
		}
	}
}

// SkipLogger 返回一个可以跳过某些路径的日志中间件
func SkipLogger(logger clog.Logger, idgen idgen.Generator, skipPaths map[string]struct{}) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 检查是否需要跳过
		if _, ok := skipPaths[c.Request.URL.Path]; ok {
			c.Next()
			return
		}

		// 使用普通日志中间件
		Logger(logger, idgen)(c)
	}
}

// SlowQueryDetector 慢查询检测中间件
// 当请求超过指定阈值时，记录警告日志
func SlowQueryDetector(logger clog.Logger, threshold time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		latency := time.Since(start)
		if latency > threshold {
			logger.Warn("slow request detected",
				clog.String("path", c.Request.URL.Path),
				clog.String("method", c.Request.Method),
				clog.Duration("latency", latency),
				clog.Int("status", c.Writer.Status()),
			)
		}
	}
}
