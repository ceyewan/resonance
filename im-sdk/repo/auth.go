package repo

import (
	"context"
)

// TokenRepository Token 仓储接口
type TokenRepository interface {
	// CreateToken 创建 Token
	CreateToken(ctx context.Context, username string) (string, error)

	// ValidateToken 验证 Token 并返回用户名
	ValidateToken(ctx context.Context, token string) (string, bool, error)

	// RevokeToken 撤销 Token
	RevokeToken(ctx context.Context, token string) error
}

