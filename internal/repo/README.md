# Repository

`internal/repo` 提供 Resonance 的仓储接口与默认实现。

- `UserRepo` / `SessionRepo` / `MessageRepo`：基于 PostgreSQL
- `RouterRepo`：基于 Redis

## 文件结构

```text
internal/repo/
├── repo.go           # 接口定义
├── user.go           # UserRepo 实现
├── session.go        # SessionRepo 实现
├── message.go        # MessageRepo 实现
├── router.go         # RouterRepo 实现
├── testutil.go       # 测试容器与测试基建
├── *_test.go         # 仓储测试
└── README.md
```

## 接口总览

| Repo | 存储 | 主要能力 |
| --- | --- | --- |
| `UserRepo` | PostgreSQL | 用户创建、查询、搜索、更新 |
| `SessionRepo` | PostgreSQL | 会话管理、成员管理、联系人查询、已读位点 |
| `MessageRepo` | PostgreSQL | 消息落库、信箱写扩散、历史拉取、Outbox |
| `RouterRepo` | Redis | 用户与网关映射、批量路由查询 |

## 使用方式

```go
// 1) 创建连接器（示例：PostgreSQL + Redis）
postgresConn, _ := connector.NewPostgreSQL(&connector.PostgreSQLConfig{/* ... */})
_ = postgresConn.Connect(ctx)

redisConn, _ := connector.NewRedis(&connector.RedisConfig{/* ... */})
_ = redisConn.Connect(ctx)

// 2) 创建 DB 组件
database, _ := db.New(
    &db.Config{Driver: "postgresql"},
    db.WithPostgreSQLConnector(postgresConn),
)

// 3) 创建 Repo
userRepo, _ := repo.NewUserRepo(database)
sessionRepo, _ := repo.NewSessionRepo(database)
messageRepo, _ := repo.NewMessageRepo(database)
routerRepo, _ := repo.NewRouterRepo(redisConn)
```

## 测试

仓储测试默认依赖 Testcontainers 自动拉起：

- PostgreSQL：`postgres:17-alpine`
- Redis：`redis:7.2-alpine`

运行方式：

```bash
go test ./internal/repo/... -v
```

说明：

- 需要本机可访问 Docker daemon。
- 若 Docker 不可用，测试会按当前 `testutil.go` 逻辑跳过或失败并给出明确原因。
- 测试数据会在用例前后自动清理。

## 注意事项

- `NewRouterRepo` 要求传入“已连接”的 `RedisConnector`。
- Repository 仅负责数据访问，不负责连接生命周期管理。
