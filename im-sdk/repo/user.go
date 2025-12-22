package repo

import (
	"context"

	commonv1 "github.com/ceyewan/resonance/im-api/gen/go/common/v1"
)

// UserRepository 用户仓储接口
type UserRepository interface {
	// CreateUser 创建用户
	CreateUser(ctx context.Context, username, password, nickname string) (*commonv1.User, error)

	// GetUserByUsername 根据用户名获取用户
	GetUserByUsername(ctx context.Context, username string) (*commonv1.User, error)

	// ValidatePassword 验证密码
	ValidatePassword(ctx context.Context, username, password string) (bool, error)

	// SearchUsers 搜索用户
	SearchUsers(ctx context.Context, query string, limit int) ([]*commonv1.User, error)

	// GetUsersByUsernames 批量获取用户信息
	GetUsersByUsernames(ctx context.Context, usernames []string) ([]*commonv1.User, error)
}

