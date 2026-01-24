package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/ceyewan/genesis/cache"
	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/resonance/internal/model"
)

// 确保 routerRepo 实现了 RouterRepo 接口
var _ RouterRepo = (*routerRepo)(nil)

// routerRepo RouterRepo 的 Redis 实现
type routerRepo struct {
	cache  cache.Cache // Genesis cache 组件
	logger clog.Logger // Genesis 日志组件
}

// RouterRepoOption 配置选项
type RouterRepoOption func(*routerRepoOptions)

type routerRepoOptions struct {
	logger clog.Logger
}

// WithLogger 设置日志记录器
func WithLogger(logger clog.Logger) RouterRepoOption {
	return func(opts *routerRepoOptions) {
		opts.logger = logger
	}
}

// NewRouterRepo 创建 RouterRepo 实例
// 参数：
//   - redisConn: Redis 连接器，由调用方提供
//   - opts: 可选配置项
func NewRouterRepo(redisConn connector.RedisConnector, opts ...RouterRepoOption) (RouterRepo, error) {
	// 默认配置
	options := &routerRepoOptions{
		logger: nil, // 将由 cache 组件创建默认 logger
	}

	// 应用选项
	for _, opt := range opts {
		opt(options)
	}

	// 创建 cache 实例，使用 JSON 序列化
	cacheInstance, err := cache.New(&cache.Config{
		Driver:     cache.DriverRedis,
		Prefix:     "resonance:router:", // 路由表前缀
		Serializer: "json",              // 使用 JSON 序列化
	}, cache.WithRedisConnector(redisConn), cache.WithLogger(options.logger))
	if err != nil {
		return nil, fmt.Errorf("failed to create cache instance: %w", err)
	}

	// 创建带有命名空间的子 logger
	// 如果没有提供 logger，创建一个默认的（输出到 /dev/null 或使用 NOP logger）
	var logger clog.Logger
	if options.logger != nil {
		logger = options.logger.WithNamespace("router")
	} else {
		// 创建一个默认的 logger，避免 nil 指针
		logger, _ = clog.New(&clog.Config{
			Level:  "info",
			Format: "json",
			Output: "/dev/null", // 默认不输出日志
		})
		logger = logger.WithNamespace("router")
	}

	repo := &routerRepo{
		cache:  cacheInstance,
		logger: logger,
	}

	return repo, nil
}

// SetUserGateway 设置用户的网关映射关系
func (r *routerRepo) SetUserGateway(ctx context.Context, router *model.Router) error {
	if router == nil {
		return fmt.Errorf("router cannot be nil")
	}

	if router.Username == "" {
		return fmt.Errorf("username cannot be empty")
	}

	// 设置过期时间为 24 小时，防止僵尸连接
	ttl := 24 * time.Hour

	key := r.buildUserKey(router.Username)

	err := r.cache.Set(ctx, key, router, ttl)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to set user gateway mapping",
			clog.String("username", router.Username),
			clog.String("gateway_id", router.GatewayID),
			clog.Error(err),
		)
		return fmt.Errorf("failed to set user gateway: %w", err)
	}

	r.logger.InfoContext(ctx, "User gateway mapping set successfully",
		clog.String("username", router.Username),
		clog.String("gateway_id", router.GatewayID),
		clog.String("remote_ip", router.RemoteIP),
		clog.Int64("timestamp", router.Timestamp),
	)

	return nil
}

// GetUserGateway 获取用户的网关映射关系
func (r *routerRepo) GetUserGateway(ctx context.Context, username string) (*model.Router, error) {
	if username == "" {
		return nil, fmt.Errorf("username cannot be empty")
	}

	key := r.buildUserKey(username)

	var router model.Router
	err := r.cache.Get(ctx, key, &router)
	if err != nil {
		// cache.Get 返回错误时，可能是 key 不存在
		r.logger.DebugContext(ctx, "Failed to get user gateway mapping",
			clog.String("username", username),
			clog.Error(err),
		)
		return nil, fmt.Errorf("failed to get user gateway: %w", err)
	}

	r.logger.DebugContext(ctx, "User gateway mapping retrieved successfully",
		clog.String("username", username),
		clog.String("gateway_id", router.GatewayID),
	)

	return &router, nil
}

// DeleteUserGateway 删除用户的网关映射关系
func (r *routerRepo) DeleteUserGateway(ctx context.Context, username string) error {
	if username == "" {
		return fmt.Errorf("username cannot be empty")
	}

	key := r.buildUserKey(username)

	err := r.cache.Delete(ctx, key)
	if err != nil {
		r.logger.ErrorContext(ctx, "Failed to delete user gateway mapping",
			clog.String("username", username),
			clog.Error(err),
		)
		return fmt.Errorf("failed to delete user gateway: %w", err)
	}

	r.logger.InfoContext(ctx, "User gateway mapping deleted successfully",
		clog.String("username", username),
	)

	return nil
}

// BatchGetUsersGateway 批量获取用户的网关映射关系
func (r *routerRepo) BatchGetUsersGateway(ctx context.Context, usernames []string) ([]*model.Router, error) {
	if len(usernames) == 0 {
		return []*model.Router{}, nil
	}

	// 批量获取，这里使用并行方式提高性能
	results := make([]*model.Router, 0, len(usernames))
	errors := make([]error, 0, len(usernames))

	for _, username := range usernames {
		router, err := r.GetUserGateway(ctx, username)
		if err != nil {
			// 记录错误但继续处理其他用户
			errors = append(errors, fmt.Errorf("username %s: %w", username, err))
			continue
		}
		results = append(results, router)
	}

	// 如果有部分失败，记录警告日志
	if len(errors) > 0 {
		r.logger.WarnContext(ctx, "Some user gateway mappings failed to retrieve",
			clog.Int("success_count", len(results)),
			clog.Int("error_count", len(errors)),
		)
	}

	r.logger.DebugContext(ctx, "Batch get user gateway mappings completed",
		clog.Int("requested", len(usernames)),
		clog.Int("successful", len(results)),
		clog.Int("failed", len(errors)),
	)

	return results, nil
}

// buildUserKey 构建用户在 Redis 中的 key
func (r *routerRepo) buildUserKey(username string) string {
	return fmt.Sprintf("user:%s", username)
}

// Close 关闭资源
func (r *routerRepo) Close() error {
	if r.cache != nil {
		return r.cache.Close()
	}
	return nil
}

// RouterRepoWithDependencies 提供完整的依赖注入方式创建 RouterRepo
// 适用于需要完全控制依赖的场景
func RouterRepoWithDependencies(
	redisConn connector.RedisConnector,
	logger clog.Logger,
) (RouterRepo, error) {
	return NewRouterRepo(redisConn, WithLogger(logger))
}
