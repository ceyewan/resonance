# Genesis Framework API Reference

Resonance IM 基于 Genesis 微服务基座构建。本文档简要总结 Genesis 提供的核心组件与常用 API，供开发参考。

> **注意**: 详细实现请查阅 `genesis/pkg` 源码或 `genesis/docs`。

## 1. Core (核心组件)

### 1.1 Container (DI 容器 & 生命周期)
`genesis/pkg/container`
负责应用程序的初始化、依赖注入和生命周期管理。

```go
import "genesis/pkg/container"

// 初始化容器
// 自动加载 Config, Logger, 并初始化 DB, Redis 等连接器
app, err := container.New(appCfg, 
    container.WithLogger(logger), 
    container.WithConfigManager(mgr),
)

// 访问组件
db := app.DB.DB(ctx)       // *gorm.DB
rdb := app.Redis.Client()  // *redis.Client

// 优雅停机
defer app.Close()
```

### 1.2 Config (配置中心)
`genesis/pkg/config`
统一配置加载，支持从文件、环境变量、远程配置中心加载。

```go
import "genesis/pkg/config"

// 创建管理器
mgr := config.NewManager(config.WithPaths("./config"))

// 加载配置
err := mgr.Load(ctx)

// 解析到结构体
var cfg AppConfig
err := mgr.Unmarshal(&cfg)

// 监听热更新 (可选)
mgr.Watch(func(e config.Event) {
    // handle update
})
```

### 1.3 Clog (日志库)
`genesis/pkg/clog`
基于 `slog` 的结构化日志，自动注入 TraceID。

```go
import "genesis/pkg/clog"

// 初始化
logger := clog.New(cfg.Log).WithNamespace("logic")

// 使用 (自动关联 Context 中的 TraceID)
logger.InfoContext(ctx, "user login", "username", "alice")
logger.ErrorContext(ctx, "db error", "err", err)
```

## 2. Storage (存储组件)

### 2.1 DB (Database)
增强型 GORM 组件，支持分库分表与事务。

```go
// 获取 GORM 实例
db := app.DB.DB(ctx)

// 基础查询
db.First(&user, 1)

// 统一事务 (解决 im-sdk Transaction Gap)
err := app.DB.Transaction(ctx, func(ctx context.Context) error {
    // 在事务闭包内，使用传入的 ctx 操作 DB，会自动使用同一个 Tx
    if err := repo.CreateUser(ctx, user); err != nil {
        return err
    }
    if err := repo.CreateProfile(ctx, profile); err != nil {
        return err
    }
    return nil
})
```

### 2.2 Redis
基于 `go-redis/v9`。

```go
rdb := app.Redis.Client()
val, err := rdb.Get(ctx, "key").Result()
```

### 2.3 DLock (分布式锁)
提供统一的分布式锁接口，支持 Redis/Etcd 后端。

```go
// 获取锁
if err := app.DLock.Lock(ctx, "resource_key", dlock.WithTTL(5*time.Second)); err != nil {
    return err // 锁定失败
}
defer app.DLock.Unlock(ctx, "resource_key")

// 业务逻辑 (Watchdog 会自动续期)
```

## 3. Telemetry (可观测性)
无需手动干预，框架层已自动集成。
- **Metrics**: 自动暴露 Prometheus 指标。
- **Tracing**: gRPC/HTTP 请求自动透传 TraceContext。

## 4. Integration Analysis for Resonance

### Solved Gaps (已解决的缺口)
*   **Transactions**: `app.DB.Transaction` 提供了完美的事务闭包支持，可直接用于 `im-sdk` 的原子操作实现。
*   **Infrastructure**: 连接管理 (MySQL/Redis/Etcd) 开箱即用，无需手动维护连接池。

### Remaining Gaps (仍需实现的缺口)
*   **ID Generation**: Genesis Roadmap 显示 "Middleware: ID Gen" 尚未完成 (`[ ]`)。
    *   **Action**: 仍需在 `im-sdk` 或 `Logic` 层实现 Snowflake (MsgID) 和 Redis Incr (SeqID) 生成器。