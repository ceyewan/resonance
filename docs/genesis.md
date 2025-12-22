# Genesis

> 一个轻量级、标准化、高可扩展的 Go 微服务组件库。

Genesis 旨在为 Go 微服务开发提供一套**统一的架构规范**和**开箱即用的组件集合**。它通过显式依赖注入和扁平化设计，帮助开发者快速构建健壮、可维护的微服务应用。

**Genesis 不是框架**——我们提供积木，用户自己搭建。

## ✨ 核心特性

- **四层扁平化架构:** 清晰的分层设计，职责明确
- **Go Native DI:** 显式依赖注入，依赖关系一目了然
- **标准化组件:** 统一的 API 设计和使用模式
- **生产级就绪:** 完整的错误处理、日志、指标和可观测性

## 🏗️ 架构概览

| 层次                        | 核心组件                                   | 职责                         |
| :-------------------------- | :----------------------------------------- | :--------------------------- |
| **Level 3: Governance**     | `auth`, `ratelimit`, `breaker`, `registry` | 流量治理，身份认证，切面能力 |
| **Level 2: Business**       | `cache`, `idgen`, `dlock`, `mq`            | 业务能力封装                 |
| **Level 1: Infrastructure** | `connector`, `db`                          | 连接管理，底层 I/O           |
| **Level 0: Base**           | `clog`, `config`, `metrics`, `xerrors`     | 框架基石                     |

## 📚 文档

- [架构设计](docs/genesis-design.md) - 总体架构和设计理念
- [组件开发规范](docs/component-spec.md) - 组件开发规范
- [文档索引](docs/) - 查看所有设计文档

## 🚀 快速开始

```go
package main

import (
    "context"
    "os/signal"
    "syscall"

    "github.com/ceyewan/genesis/clog"
    "github.com/ceyewan/genesis/config"
    "github.com/ceyewan/genesis/connector"
    "github.com/ceyewan/genesis/db"
    "github.com/ceyewan/genesis/dlock"
)

func main() {
    ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer cancel()

    // 1. 加载配置
    cfg, _ := config.Load("config.yaml")

    // 2. 初始化 Logger
    logger, _ := clog.New(&cfg.Log)

    // 3. 创建连接器 (defer 自动释放资源)
    redisConn, _ := connector.NewRedis(&cfg.Redis, connector.WithLogger(logger))
    defer redisConn.Close()

    mysqlConn, _ := connector.NewMySQL(&cfg.MySQL, connector.WithLogger(logger))
    defer mysqlConn.Close()

    // 4. 初始化组件 (显式注入依赖)
    database, _ := db.New(mysqlConn, &cfg.DB, db.WithLogger(logger))
    locker, _ := dlock.New(redisConn, &cfg.DLock, dlock.WithLogger(logger))

    // 5. 使用组件
    logger.InfoContext(ctx, "service started")

    var user struct{ ID int64 }
    database.DB(ctx).First(&user, 1)

    if err := locker.Lock(ctx, "my_resource"); err == nil {
        defer locker.Unlock(ctx, "my_resource")
        // do business logic...
    }
}
```

## 🔧 组件列表

### Level 0 - 基础设施

- **[clog](./clog)** - 标准化日志库，基于 slog，支持 Context 和 Namespace
- **[config](./config)** - 统一配置管理，支持多源加载
- **[metrics](./metrics)** - 基于 OpenTelemetry 的指标收集
- **[xerrors](./xerrors)** - 增强型错误处理

### Level 1 - 连接管理

- **[connector](./connector)** - 统一连接管理器，支持 MySQL/Redis/Etcd/NATS
- **[db](./db)** - 基于 GORM 的数据库组件，支持分库分表

### Level 2 - 业务组件

- **[cache](./cache)** - 统一缓存接口，支持 Redis
- **[dlock](./dlock)** - 分布式锁，支持 Redis/Etcd，内置自动续期
- **[idgen](./idgen)** - ID 生成器，支持 Snowflake/UUID/Sequence
- **[idempotency](./idempotency)** - 幂等性组件，支持手动调用、Gin、gRPC
- **[mq](./mq)** - 消息队列组件，支持 NATS

### Level 3 - 流量治理

- **[auth](./auth)** - 认证授权组件
- **[ratelimit](./ratelimit)** - 限流组件
- **[breaker](./breaker)** - 熔断器组件
- **[registry](./registry)** - 服务注册发现

## 📖 使用示例

```bash
# 查看所有可用示例
make examples

# 运行特定组件示例
make example-cache
make example-dlock

# 运行所有示例
make example-all
```

## 🗺️ 版本状态

### v0.1.0 (已发布)

- **Base (L0):** clog, config, metrics, xerrors
- **Infrastructure (L1):** connector, db
- **Business (L2):** cache, dlock, idgen, mq
- **Governance (L3):** auth, ratelimit, breaker, registry

## 📄 License

MIT