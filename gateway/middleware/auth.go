package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/ceyewan/genesis/auth"
	"github.com/ceyewan/genesis/clog"
	"github.com/gin-gonic/gin"

	"github.com/ceyewan/resonance/pkg/userauth"
)

const (
	// UsernameKey 是上下文中存储用户名的键
	UsernameKey = "username"
	TenantIDKey = "tenant_id"
)

type usernameRequestCtxKey struct{}
type principalRequestCtxKey struct{}

type Principal struct {
	Username          string
	TenantID          string
	MembershipVersion int64
}

// AuthConfig 认证中间件配置
type AuthConfig struct {
	authenticator auth.Authenticator
	logger        clog.Logger
}

// NewAuthConfig 创建认证配置
func NewAuthConfig(authenticator auth.Authenticator, logger clog.Logger) *AuthConfig {
	return &AuthConfig{
		authenticator: authenticator,
		logger:        logger,
	}
}

// RequireAuth 返回一个需要认证的中间件
// 从请求头或查询参数中获取 token 并验证
func (a *AuthConfig) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, err := a.extractAndValidate(c)
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

		// 将用户名存入 Gin 上下文
		c.Set(UsernameKey, principal.Username)
		c.Set(TenantIDKey, principal.TenantID)

		// 将用户名注入 http.Request Context，以便 ConnectRPC Handler 获取
		ctx := WithPrincipal(c.Request.Context(), principal)
		ctx = userauth.WithPrincipal(ctx, &userauth.Principal{
			TenantID:          principal.TenantID,
			Username:          principal.Username,
			MembershipVersion: principal.MembershipVersion,
		})
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// OptionalAuth 返回一个可选认证的中间件
// 如果提供了 token 则验证，没有则跳过
func (a *AuthConfig) OptionalAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, err := a.extractAndValidate(c)
		if err == nil && principal != nil && principal.Username != "" {
			c.Set(UsernameKey, principal.Username)
			c.Set(TenantIDKey, principal.TenantID)
			// 同时注入 http.Context
			ctx := WithPrincipal(c.Request.Context(), principal)
			ctx = userauth.WithPrincipal(ctx, &userauth.Principal{
				TenantID:          principal.TenantID,
				Username:          principal.Username,
				MembershipVersion: principal.MembershipVersion,
			})
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	}
}

// extractAndValidate 从请求中提取并验证 token
func (a *AuthConfig) extractAndValidate(c *gin.Context) (*Principal, error) {
	token := tokenFromRequest(c)

	if token == "" {
		return nil, ErrMissingToken
	}

	if a.authenticator == nil {
		return nil, ErrInvalidToken
	}
	claims, err := a.authenticator.ValidateAccessToken(c.Request.Context(), token)
	if err != nil {
		return nil, ErrInvalidToken
	}
	verified, ok := userauth.FromClaims(claims)
	if !ok {
		return nil, ErrInvalidToken
	}
	return &Principal{
		Username:          verified.Username,
		TenantID:          verified.TenantID,
		MembershipVersion: verified.MembershipVersion,
	}, nil
}

func tokenFromRequest(c *gin.Context) string {
	token := c.GetHeader("Authorization")
	if token != "" {
		// 支持 "Bearer <token>" 格式
		if after, ok := strings.CutPrefix(token, "Bearer "); ok {
			token = after
		}
	} else {
		// 从查询参数获取 token
		token = c.Query("token")
	}

	return token
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

func WithUsername(ctx context.Context, username string) context.Context {
	return WithPrincipal(ctx, &Principal{Username: username})
}

func UsernameFromRequestContext(ctx context.Context) (string, bool) {
	username, ok := ctx.Value(usernameRequestCtxKey{}).(string)
	return username, ok && username != ""
}

func WithPrincipal(ctx context.Context, principal *Principal) context.Context {
	if principal == nil {
		return ctx
	}
	copy := &Principal{
		Username:          principal.Username,
		TenantID:          principal.TenantID,
		MembershipVersion: principal.MembershipVersion,
	}
	ctx = context.WithValue(ctx, principalRequestCtxKey{}, copy)
	return context.WithValue(ctx, usernameRequestCtxKey{}, copy.Username)
}

func PrincipalFromRequestContext(ctx context.Context) (*Principal, bool) {
	principal, ok := ctx.Value(principalRequestCtxKey{}).(*Principal)
	if !ok || principal == nil || principal.Username == "" {
		return nil, false
	}
	copy := &Principal{
		Username:          principal.Username,
		TenantID:          principal.TenantID,
		MembershipVersion: principal.MembershipVersion,
	}
	return copy, true
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
