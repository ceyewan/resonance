package repo

import (
	"context"
	"fmt"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/db"
	"github.com/ceyewan/resonance/im-sdk/model"
	"gorm.io/gorm"
)

// UserRepoOption 配置 UserRepo 的选项
type UserRepoOption func(*userRepoOptions)

type userRepoOptions struct {
	logger clog.Logger
}

// WithLogger 设置日志记录器
func WithUserRepoLogger(logger clog.Logger) UserRepoOption {
	return func(o *userRepoOptions) {
		o.logger = logger
	}
}

// userRepo 实现 UserRepo 接口
type userRepo struct {
	db     db.DB
	logger clog.Logger
}

// NewUserRepo 创建 UserRepo 实例
func NewUserRepo(database db.DB, opts ...UserRepoOption) (UserRepo, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}

	options := &userRepoOptions{}
	for _, opt := range opts {
		opt(options)
	}

	// 提供默认 logger
	var logger clog.Logger
	if options.logger != nil {
		logger = options.logger.WithNamespace("user_repo")
	} else {
		var err error
		logger, err = clog.New(&clog.Config{
			Level:  "info",
			Format: "json",
			Output: "/dev/null",
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create default logger: %w", err)
		}
		logger = logger.WithNamespace("user_repo")
	}

	return &userRepo{
		db:     database,
		logger: logger,
	}, nil
}

// CreateUser 创建新用户
func (r *userRepo) CreateUser(ctx context.Context, user *model.User) error {
	if user == nil {
		return fmt.Errorf("user cannot be nil")
	}
	if user.Username == "" {
		return fmt.Errorf("username cannot be empty")
	}

	gormDB := r.db.DB(ctx)
	if err := gormDB.Create(user).Error; err != nil {
		r.logger.Error("创建用户失败",
			clog.String("username", user.Username),
			clog.Error(err))
		return fmt.Errorf("failed to create user: %w", err)
	}

	r.logger.Info("创建用户成功", clog.String("username", user.Username))
	return nil
}

// GetUserByUsername 根据用户名获取用户
func (r *userRepo) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	if username == "" {
		return nil, fmt.Errorf("username cannot be empty")
	}

	var user model.User
	gormDB := r.db.DB(ctx)
	if err := gormDB.Where("username = ?", username).First(&user).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("user not found: %s", username)
		}
		r.logger.Error("获取用户失败",
			clog.String("username", username),
			clog.Error(err))
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return &user, nil
}

// SearchUsers 搜索用户（模糊匹配昵称或用户名）
func (r *userRepo) SearchUsers(ctx context.Context, query string) ([]*model.User, error) {
	if query == "" {
		return []*model.User{}, nil
	}

	var users []*model.User
	gormDB := r.db.DB(ctx)

	// 模糊匹配用户名或昵称
	searchPattern := "%" + query + "%"
	if err := gormDB.Where("username LIKE ? OR nickname LIKE ?", searchPattern, searchPattern).
		Limit(50). // 限制搜索结果数量
		Find(&users).Error; err != nil {
		r.logger.Error("搜索用户失败",
			clog.String("query", query),
			clog.Error(err))
		return nil, fmt.Errorf("failed to search users: %w", err)
	}

	return users, nil
}

// UpdateUser 更新用户信息
func (r *userRepo) UpdateUser(ctx context.Context, user *model.User) error {
	if user == nil {
		return fmt.Errorf("user cannot be nil")
	}
	if user.Username == "" {
		return fmt.Errorf("username cannot be empty")
	}

	gormDB := r.db.DB(ctx)
	result := gormDB.Model(&model.User{}).Where("username = ?", user.Username).Updates(user)

	if result.Error != nil {
		r.logger.Error("更新用户失败",
			clog.String("username", user.Username),
			clog.Error(result.Error))
		return fmt.Errorf("failed to update user: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return fmt.Errorf("user not found: %s", user.Username)
	}

	r.logger.Info("更新用户成功", clog.String("username", user.Username))
	return nil
}

// Close 释放资源
func (r *userRepo) Close() error {
	r.logger.Info("关闭 UserRepo")
	// db 实例由外部管理，这里不需要关闭
	return nil
}
