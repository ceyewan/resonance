# CLAUDE.md

此文件用于指导 Claude Code (claude.ai/code) 在 `Resonance` 仓库中的工作方式。**全程使用中文交流**，专注于 IM 业务逻辑开发。

## 项目定位

`Resonance` 是一个高性能即时通讯 (IM) 系统，**专注于 IM 业务逻辑实现**，通过合理使用 [Genesis](github.com/ceyewan/genesis) 组件库解决基础架构问题。

**设计理念**：业务优先，按需使用基础组件，避免框架锁定。

## 架构概览

```
                    消息流向与服务通信

┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│     Web 前端     │    │   Gateway 网关   │    │    Logic 逻辑    │
│                 │◄───┤                 │◄───┤                 │
│  • React + TS   │HTTP│  • Gin HTTP     │gRPC│  • 消息路由      │
│  • ConnectRPC   │WS  │  • WebSocket    │    │  • 会话管理      │
│  • WebSocket    │    │  • gRPC Server  │    │  • 用户认证      │
└─────────────────┘    └────────┬────────┘    └────────┬────────┘
                                │                       │
                                │gRPC                   │ MQ Publish
                                │                       │
                            ┌───▼──────┐          ┌─────▼─────┐
                            │  Task    │◄────────-┤   NATS    │
                            │          │Subscribe │    MQ     │
                            │ • 离线消息│           └───────────┘
                            │ • 持久化  │
                            │ • gRPC推送│
                            └──────────┘

                    ┌───────┴─────────────────────┐
                    │      Genesis 组件层          │
                    │  connector / db / mq / ...  │
                    └─────────────────────────────┘
```

**服务职责**：
- **Web**：React 前端，ConnectRPC 调用 Gateway，WebSocket 接收消息
- **Gateway**：WebSocket 长连接、心跳检测、消息推送、协议解析
- **Logic**：消息路由、权限验证、会话管理
- **Task**：离线消息、持久化、统计分析

**通信方式**：
- Web → Gateway：ConnectRPC (HTTP)、WebSocket
- Gateway → Logic：gRPC
- Logic → NATS → Task：MQ (异步消息)
- Task → Gateway (gRPC) → Web (WebSocket)：消息推送

## 技术栈

**后端**：
- 语言：Go 1.25+
- 基础组件：[Genesis v0.2.0](github.com/ceyewan/genesis) (本地子模块)
- 服务间：gRPC
- 消息队列：NATS

**前端**：
- 框架：React 18 + TypeScript
- 构建：Vite
- 状态：Zustand
- 通信：ConnectRPC + WebSocket

**存储**：
- MySQL：消息历史、用户信息、会话数据
- Redis：路由映射、在线状态、缓存

## Genesis 组件使用指南

### 组件分类

| 层级 | 组件 | 用途 | 是否必要 |
|------|------|------|----------|
| L0 | `clog` | 结构化日志 (基于 slog) | ✅ 必要 |
| L0 | `config` | 配置管理 (支持多源加载) | ✅ 必要 |
| L0 | `xerrors` | 增强型错误处理 | ✅ 必要 |
| L0 | `metrics` | OpenTelemetry 指标收集 | 按需 |
| L1 | `connector` | MySQL/Redis/NATS/Etcd 连接 | ✅ 必要 |
| L1 | `db` | GORM 数据库操作 | ✅ 必要 |
| L2 | `idgen` | Snowflake/UUID/Sequence 生成 | 推荐 |
| L2 | `mq` | NATS 消息队列 | 推荐 |
| L2 | `cache` | 统一缓存接口 (Redis) | 推荐 |
| L2 | `dlock` | 分布式锁 (Redis/Etcd) | 按需 |
| L2 | `idempotency` | 幂等性组件 | 按需 |
| L3 | `auth` | JWT 认证授权 | 推荐 |
| L3 | `ratelimit` | 限流组件 | 按需 |
| L3 | `breaker` | 熔断器组件 | 按需 |
| L3 | `registry` | Etcd 服务注册发现 | 按需 |

### 查看文档

```bash
# 查看组件文档
go doc -all github.com/ceyewan/genesis/connector
go doc -all github.com/ceyewan/genesis/auth

# 查看本地 genesis 源码
ls genesis/
cat genesis/connector/redis.go

# 查看项目中的使用示例
cat logic/logic.go         # 资源初始化
cat internal/repo/repo.go    # 业务接口定义
```

### 初始化模式（显式依赖注入）

```go
// 1. 创建连接
redisConn, _ := connector.NewRedis(&cfg.Redis, connector.WithLogger(logger))
defer redisConn.Close()

// 2. 创建组件（注入连接）
dbInstance, _ := db.New(mysqlConn, &cfg.DB, db.WithLogger(logger))

// 3. 创建业务层（注入组件）
userRepo := repo.NewUserRepo(dbInstance)
authSvc := service.NewAuthService(userRepo, authenticator, logger)
```

## 目录结构

```
resonance/
├── main.go                # 统一入口 (go run main.go -module logic)
├── api/                   # Protobuf 协议定义
│   ├── proto/             # .proto 原文件
│   └── gen/               # 生成代码 (勿修改)
├── internal/                # 公共 SDK
│   ├── model/             # 数据模型 (User/Session/Message...)
│   └── repo/              # 仓储接口 (业务层抽象)
├── logic/                 # Logic 服务
│   ├── config/            # 配置加载
│   ├── server/            # gRPC 服务器封装
│   ├── service/           # 业务服务实现
│   └── logic.go           # 生命周期管理
├── gateway/               # Gateway 服务
├── task/                  # Task 服务
└── web/                   # React 前端
    ├── src/
    │   ├── api/           # ConnectRPC 客户端
    │   ├── hooks/         # WebSocket Hook
    │   ├── stores/        # Zustand 状态
    │   └── pages/         # 页面组件
    └── package.json
```

## 业务开发规范

### 1. 面向业务接口，隐藏基础组件

```go
// ✅ 业务接口 - internal/repo/repo.go
type UserRepo interface {
    CreateUser(ctx context.Context, user *model.User) error
    GetUserByUsername(ctx context.Context, username string) (*model.User, error)
}

// ❌ 避免在业务层直接暴露基础组件
type Service struct {
    redisConn connector.RedisConnector  // 过于底层
}
```

### 2. 资源管理最佳实践

```go
type Logic struct {
    mysqlConn connector.MySQLConnector
    redisConn connector.RedisConnector
    // ...
}

func (l *Logic) Close() error {
    // 按相反顺序释放资源
    l.redisConn.Close()
    l.mysqlConn.Close()
    return nil
}
```

## 常用命令

```bash
# 后端 - 代码生成
make gen              # 生成 protobuf 代码
make tidy             # 整理依赖

# 后端 - 开发运行
make run-logic        # go run main.go -module logic
make run-gateway
make run-task

# 前端 - 开发运行
cd web && npm run dev

# 前端 - 构建
cd web && npm run build
```

## Git 工作流

### 分支命名

`<type>/<description>`

- 类型：`feature`, `fix`, `refactor`, `docs`
- 示例：`feature/offline-message`, `fix/router-cache`

### 提交格式

`<type>(<scope>): <subject>`

- 类型：`feat`, `fix`, `refactor`, `docs`, `chore`
- 作用域：`logic`, `gateway`, `task`, `web`, `api`
- 语言：中文，祈使语气

```
feat(logic): 实现离线消息推送

- 添加离线消息存储逻辑
- 实现推送重试机制
- 添加推送状态跟踪
```

## IM 开发八荣八耻

1. **以过度设计为耻，以业务实现为荣**
2. **以基础组件暴露为耻，以业务封装为荣**
3. **以盲目复制为耻，以场景适配为荣**
4. **以单点瓶颈为耻，以可扩展性为荣**
5. **以消息丢失为耻，以可靠性保证为荣**
6. **以连接泄漏为耻，以资源管理为荣**
7. **以忽略性能为耻，以性能监控为荣**
8. **以数据不一致为耻，以一致性保证为荣**
