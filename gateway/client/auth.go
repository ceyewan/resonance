package client

import (
	"context"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
)

// Login 调用 Logic 的登录接口
func (c *Client) Login(ctx context.Context, req *logicv1.LoginRequest) (*logicv1.LoginResponse, error) {
	return c.authSvc().Login(ctx, req)
}

// Register 调用 Logic 的注册接口
func (c *Client) Register(ctx context.Context, req *logicv1.RegisterRequest) (*logicv1.RegisterResponse, error) {
	return c.authSvc().Register(ctx, req)
}

// ValidateToken 验证 Token
func (c *Client) ValidateToken(ctx context.Context, token string) (*logicv1.ValidateTokenResponse, error) {
	return c.authSvc().ValidateToken(ctx, &logicv1.ValidateTokenRequest{
		AccessToken: token,
	})
}
