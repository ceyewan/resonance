# IM SDK Repository 实现说明

本文档说明 IM SDK 中各个 Repository 接口的实现，特别是基于 Genesis 组件的 Redis 实现。

## 📁 文件结构

```
im-sdk/repo/
├── repo.go              # Repository 接口定义
├── router_repo.go       # RouterRepo 的 Redis 实现
├── example_usage.go     # 使用示例和集成方式
├── router_repo_test.go  # 单元测试和集成测试
└── README.md           # 本文档
```

## 🔧 RouterRepo 实现

### 概述

`RouterRepo` 负责管理用户与网关实例的映射关系，通常存储在 Redis 中以支持快速的读写操作。实现基于 Genesis 框架的 `cache` 和 `connector` 组件，确保了高性能和可靠性。

### 核心特性

- **依赖注入设计**: 支持灵活的依赖注入，调用方提供 logger、redisConn 等依赖
- **基于 Genesis 组件**: 使用 `cache.Cache` 和 `connector.RedisConnector` 统一接口
- **自动过期机制**: 用户网关映射 24 小时自动过期，防止僵尸连接
- **批量操作支持**: 支持批量获取用户网关映射，提高群发消息性能
- **完善的日志记录**: 集成结构化日志，便于调试和监控
- **错误处理**: 完整的参数验证和错误处理

### 数据结构

```go
type Router struct {
    Username  string `json:"username"`   // 用户名
    GatewayID string `json:"gateway_id"` // 网关实例 ID
    RemoteIP  string `json:"remote_ip"`  // 客户端 IP 地址
    Timestamp int64  `json:"timestamp"`  // 建立连接的时间戳
}
```

### Redis Key 设计

- **Key 格式**: `resonance:router:user:{username}`
- **TTL**: 24 小时
- **序列化**: JSON
- **前缀**: `resonance:router:` (可配置)

## 🚀 使用方法

### 1. 基本使用

```go
import (
    "github.com/ceyewan/genesis/clog"
    "github.com/ceyewan/genesis/connector"
    "github.com/ceyewan/resonance/im-sdk/repo"
)

// 创建 Redis 连接器
redisConfig := &connector.RedisConfig{
    Addr:     "localhost:6379",
    Password: "",
    DB:       0,
    PoolSize: 10,
}

redisConn, err := connector.NewRedis(redisConfig)
if err != nil {
    return err
}
defer redisConn.Close()

// 创建日志记录器
logger, err := clog.New(&clog.Config{
    Level:  "info",
    Format: "json",
    Output: "stdout",
})
if err != nil {
    return err
}

// 创建 RouterRepo 实例
routerRepo, err := repo.NewRouterRepo(redisConn, repo.WithLogger(logger))
if err != nil {
    return err
}
defer routerRepo.Close()
```

### 2. 在 Logic 服务中使用

```go
type LogicService struct {
    routerRepo repo.RouterRepo
    logger     clog.Logger
}

func NewLogicService(redisConn connector.RedisConnector, logger clog.Logger) (*LogicService, error) {
    routerRepo, err := repo.NewRouterRepo(redisConn, repo.WithLogger(logger))
    if err != nil {
        return nil, err
    }

    return &LogicService{
        routerRepo: routerRepo,
        logger:     logger.WithNamespace("logic"),
    }, nil
}

func (s *LogicService) HandleUserLogin(ctx context.Context, username, gatewayID, remoteIP string) error {
    router := &model.Router{
        Username:  username,
        GatewayID: gatewayID,
        RemoteIP:  remoteIP,
        Timestamp: time.Now().Unix(),
    }

    return s.routerRepo.SetUserGateway(ctx, router)
}
```

### 3. 在 Task 服务中使用

```go
type TaskService struct {
    routerRepo repo.RouterRepo
    logger     clog.Logger
}

func (s *TaskService) PushMessageToUser(ctx context.Context, username string, message string) error {
    // 获取用户网关位置
    router, err := s.routerRepo.GetUserGateway(ctx, username)
    if err != nil {
        return err
    }

    // 根据 router.GatewayID 调用对应的 Gateway 服务
    // gatewayClient.PushMessage(ctx, gatewayID, message)

    return nil
}
```

## 🧪 测试

运行测试：

```bash
# 运行所有测试
go test ./im-sdk/repo/...

# 运行集成测试（需要 Redis 实例）
go test ./im-sdk/repo/... -v

# 运行并发测试
go test ./im-sdk/repo/... -run=Concurrency -v

# 跳过集成测试（快速模式）
go test ./im-sdk/repo/... -short
```

### 测试覆盖

- ✅ 基本 CRUD 操作
- ✅ 输入参数验证
- ✅ 错误处理
- ✅ 并发操作安全性
- ✅ 批量操作性能
- ✅ Redis 连接异常处理

## 📊 性能优化

### 1. 连接池配置

```go
redisConfig := &connector.RedisConfig{
    Addr:         "localhost:6379",
    PoolSize:     20,        // 根据并发量调整
    MinIdleConns: 5,         // 保持最小空闲连接
    DialTimeout:  5 * time.Second,
    ReadTimeout:  3 * time.Second,
    WriteTimeout: 3 * time.Second,
}
```

### 2. 批量操作优化

```go
// ✅ 推荐：使用批量获取
routers, err := routerRepo.BatchGetUsersGateway(ctx, usernames)

// ❌ 避免：循环单个获取
for _, username := range usernames {
    router, err := routerRepo.GetUserGateway(ctx, username)
    // ...
}
```

### 3. 网关分组推送

```go
// 按网关分组，减少网络调用
gatewayGroups := make(map[string][]*model.Router)
for _, router := range routers {
    gatewayGroups[router.GatewayID] = append(gatewayGroups[router.GatewayID], router)
}

// 分别向每个网关推送
for gatewayID, group := range gatewayGroups {
    gatewayClient.BroadcastMessage(ctx, gatewayID, group, message)
}
```

## 🔧 配置选项

### Redis 配置

```go
// 基础配置
redisConfig := &connector.RedisConfig{
    Addr:     "redis-cluster.example.com:6379",
    Password: "your-password",
    DB:       0,
    PoolSize: 50,
}

// 连接器选项（可选）
redisConn, err := connector.NewRedis(redisConfig,
    connector.WithLogger(logger),      // 注入日志
    connector.WithMeter(meter),        // 注入指标
)
```

### Cache 配置

```go
// RouterRepo 内部使用以下 cache 配置
cacheConfig := &cache.Config{
    Prefix:     "resonance:router:", // Key 前缀
    Serializer: "json",              // 序列化方式
}
```

## 📝 最佳实践

### 1. 依赖注入

- ✅ 由调用方（logic、task 服务）提供 `connector.RedisConnector`
- ✅ 注入 `clog.Logger` 用于结构化日志记录
- ✅ 支持可选的指标收集器注入

### 2. 错误处理

```go
// ✅ 推荐：完整的错误处理
router, err := s.routerRepo.GetUserGateway(ctx, username)
if err != nil {
    s.logger.ErrorContext(ctx, "Failed to get user gateway",
        clog.String("username", username),
        clog.Error(err),
    )
    return err
}

// ✅ 推荐：部分失败的批量操作
routers, err := s.routerRepo.BatchGetUsersGateway(ctx, usernames)
if err != nil {
    // 记录警告，但不中断整个流程
    s.logger.WarnContext(ctx, "Some user gateways failed to retrieve", clog.Error(err))
}
```

### 3. 资源管理

```go
// ✅ 推荐：正确关闭资源
func (s *Service) Close() error {
    if s.routerRepo != nil {
        return s.routerRepo.Close()
    }
    return nil
}

// ✅ 推荐：使用 defer 确保资源释放
routerRepo, err := repo.NewRouterRepo(redisConn, repo.WithLogger(logger))
if err != nil {
    return err
}
defer routerRepo.Close()
```

## 🚨 注意事项

1. **Redis 依赖**: 当前实现依赖 Redis，请确保 Redis 实例可用
2. **TTL 设置**: 用户网关映射有 24 小时 TTL，需要定期心跳更新
3. **并发安全**: 实现是并发安全的，支持多个 goroutine 同时操作
4. **序列化**: 使用 JSON 序列化，确保 `model.Router` 结构体字段可 JSON 化

## 🔄 未来扩展

- 支持其他缓存后端（如内存缓存）
- 支持多数据中心同步
- 支持用户位置变更事件通知
- 支持路由数据统计分析