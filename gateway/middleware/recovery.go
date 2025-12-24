package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/ceyewan/genesis/clog"
	"github.com/gin-gonic/gin"
)

// Recovery 返回一个恢复中间件，捕获 panic 并防止服务崩溃
func Recovery(logger clog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// 获取堆栈信息
				stack := debug.Stack()

				// 记录错误日志
				logger.Error("panic recovered",
					clog.Any("error", err),
					clog.String("path", c.Request.URL.Path),
					clog.String("method", c.Request.Method),
					clog.String("client_ip", c.ClientIP()),
					clog.String("stack", string(stack)),
				)

				// 返回 500 错误
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"error": "internal server error",
				})
			}
		}()

		c.Next()
	}
}

// RecoveryWithWriter 返回一个自定义响应的恢复中间件
func RecoveryWithWriter(logger clog.Logger, customResponse func(c *gin.Context, err interface{})) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				stack := debug.Stack()

				logger.Error("panic recovered",
					clog.Any("error", err),
					clog.String("path", c.Request.URL.Path),
					clog.String("method", c.Request.Method),
					clog.String("client_ip", c.ClientIP()),
					clog.String("stack", string(stack)),
				)

				if customResponse != nil {
					customResponse(c, err)
				} else {
					c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
						"error": fmt.Sprintf("%v", err),
					})
				}
			}
		}()

		c.Next()
	}
}
