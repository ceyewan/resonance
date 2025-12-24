package middleware

import (
	"net/http"
	"strings"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/resonance/gateway/client"
	"github.com/gin-gonic/gin"
)

const (
	// UsernameKey 是上下文中存储用户名的键
	UsernameKey = "username"
)

// AuthConfig 认证中间件配置
type AuthConfig struct {
	logicClient *client.Client
	logger      clog.Logger
}

// NewAuthConfig 创建认证配置
func NewAuthConfig(logicClient *client.Client, logger clog.Logger) *AuthConfig {
	return &AuthConfig{
		logicClient: logicClient,
		logger:      logger,
	}
}

// RequireAuth 返回一个需要认证的中间件
// 从请求头或查询参数中获取 token 并验证
func (a *AuthConfig) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		username, err := a.extractAndValidate(c)
		if err != nil {
			a.logger.Warn("authentication failed",
				clog.String("client_ip", c.ClientIP()),
				clog.Error(err),
			)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "unauthorized: " + err.Error(),
			})
			return
		}

		// 将用户名存入上下文
		c.Set(UsernameKey, username)
		c.Next()
	}
}

// OptionalAuth 返回一个可选认证的中间件
// 如果提供了 token 则验证，没有则跳过
func (a *AuthConfig) OptionalAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		username, err := a.extractAndValidate(c)
		if err == nil && username != "" {
			c.Set(UsernameKey, username)
		}
		c.Next()
	}
}

// extractAndValidate 从请求中提取并验证 token
func (a *AuthConfig) extractAndValidate(c *gin.Context) (string, error) {
	// 从请求头获取 token
	token := c.GetHeader("Authorization")
	if token != "" {
		// 支持 "Bearer <token>" 格式
		if strings.HasPrefix(token, "Bearer ") {
			token = strings.TrimPrefix(token, "Bearer ")
		}
	} else {
		// 从查询参数获取 token
		token = c.Query("token")
	}

	if token == "" {
		return "", ErrMissingToken
	}

	// 调用 Logic 服务验证 token
	resp, err := a.logicClient.ValidateToken(c.Request.Context(), token)
	if err != nil {
		return "", ErrInvalidToken
	}

	if !resp.Valid {
		return "", ErrInvalidToken
	}

	return resp.Username, nil
}

// GetUsername 从上下文获取用户名
func GetUsername(c *gin.Context) (string, bool) {
	username, exists := c.Get(UsernameKey)
	if !exists {
		return "", false
	}
	return username.(string), true
}

// MustGetUsername 从上下文获取用户名，如果不存在则 panic
func MustGetUsername(c *gin.Context) string {
	username, exists := GetUsername(c)
	if !exists {
		panic("username not found in context")
	}
	return username
}

// 错误定义
var (
	ErrMissingToken = &AuthError{Message: "missing authentication token"}
	ErrInvalidToken = &AuthError{Message: "invalid authentication token"}
)

// AuthError 认证错误
type AuthError struct {
	Message string
}

func (e *AuthError) Error() string {
	return e.Message
}
