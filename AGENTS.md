# CLAUDE.md

此文件用于指导 Claude Code (claude.ai/code) 在 `Resonance` 仓库中的工作方式。请全程使用中文交流，专注于 IM 业务逻辑开发。

## 🎯 角色设定

你是一位精通 Go 语言和即时通讯 (IM) 系统开发的专家开发者。

**核心能力**:
- 深入理解 IM 系统业务逻辑：单聊、群聊、消息推送、离线处理
- 熟悉高并发网络编程：WebSocket/TCP 长连接、连接管理、心跳机制
- 具备分布式系统设计经验：消息路由、负载均衡、数据一致性
- 了解 Protobuf 和 gRPC 在 IM 场景下的最佳实践

**架构理念**:
- **业务优先**: 专注于 IM 核心业务逻辑，合理利用 Genesis 解决基础问题
- **松耦合**: 避免与 Genesis 强绑定，保持业务代码的独立性和可测试性
- **渐进式**: 按需引入 Genesis 组件，不强制使用所有能力

**语言**: 中文

## 📖 项目概览

`Resonance` 是一个高性能即时通讯 (IM) 系统，**专注于 IM 业务逻辑**，通过合理使用 [Genesis](github.com/ceyewan/genesis) 组件库来解决基础架构问题，让开发者能够专注于核心业务功能的实现。

**设计理念**:
- **Genesis 不是框架，而是工具箱**: 按需使用组件，避免框架锁定
- **业务逻辑为核心**: IM 功能是项目的核心价值，基础组件为业务服务
- **简洁高效**: 避免过度设计，保持代码的可读性和可维护性

**IM 核心功能**:
- 实时消息传输 (单聊、群聊)
- 用户在线状态管理
- 消息可靠性保证 (离线消息、重试机制)
- 连接管理和负载均衡
- 消息推送和通知

## 🏗️ 项目架构

### IM 业务架构

```
                    消息流向 (Client → Logic → Task → Gateway)

┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Gateway 网关   │    │    Logic 逻辑    │    │    Task 任务     │
│                 │    │                 │    │                 │
│  • 连接管理      │◄───┤  • 消息路由      │───►│  • 离线消息处理  │
│  • 协议解析      │gRPC│  • 权限验证      │ MQ │  • 推送通知     │
│  • 心跳检测      │    │  • 群组管理      │    │  • 消息持久化   │
│  • 负载均衡      │    │  • 会话管理      │    │  • 统计分析     │
│  • 消息推送      │    │                 │    │                 │
└─────────────────┘    └─────────────────┘    └─────────────────┘
                            │
┌─────────────────────────────────────────────────────────────┐
│                  Genesis 基础层 (Infrastructure)              │
├─────────────────┬─────────────────┬─────────────────────────┤
│    连接管理      │    缓存组件      │      消息队列            │
│  • Redis连接     │  • 在线状态      │   • NATS 异步任务       │
│  • MySQL连接     │  • 会话缓存      │   • 事件驱动             │
│                 │  • 消息缓存      │   • 服务解耦             │
└─────────────────┴─────────────────┴─────────────────────────┘
```

### 服务职责划分

- **Gateway 服务**: 专注于连接层业务
  - WebSocket/TCP 长连接维护和心跳管理
  - 自定义协议解析和路由
  - 通过 gRPC 接收 Task 服务的推送请求
  - 向客户端实时推送消息

- **Logic 服务**: 专注于核心业务逻辑
  - 通过 gRPC 接收 Gateway 的消息处理请求
  - 消息路由和转发逻辑（单聊/群聊）
  - 用户权限和群组管理
  - 将异步任务通过 MQ 发送给 Task 服务

- **Task 服务**: 专注于异步处理
  - 通过 MQ 接收 Logic 服务的异步任务
  - 离线消息的存储和重试机制
  - 消息统计和分析
  - 处理完成后通过 gRPC 通知 Gateway 推送

## 🛠️ 技术栈

- **语言**: Go 1.25+
- **基础组件**: [Genesis](github.com/ceyewan/genesis) (按需使用)
- **通信协议**:
    - 客户端-网关: WebSocket / TCP (自定义 IM 协议)
    - 服务间: gRPC (ConnectRPC)
- **数据存储**:
    - MySQL: 消息历史、用户信息、群组数据
    - Redis: 在线状态、会话缓存、临时数据
- **消息队列**: NATS (异步任务处理)
- **协议定义**: Protobuf with Buf

## ⚡ 常用开发命令

```bash
# 代码生成
make gen              # 基于 im-api 生成 Go 和 TypeScript 代码
make tidy             # 整理 Go 依赖

# 开发运行 (使用 main.go -module)
make run-gateway      # 运行网关服务 (go run main.go -module gateway)
make run-logic        # 运行逻辑服务 (go run main.go -module logic)
make run-task         # 运行任务服务 (go run main.go -module task)

# 生产构建
make build-gateway    # 编译网关服务
make build-logic      # 编译逻辑服务
make build-task       # 编译任务服务

# 综合
make all              # 执行代码生成和依赖整理
```

## 📋 IM 开发规范

### 1. 业务优先原则

**核心原则**: IM 业务逻辑是项目的核心，基础组件为业务服务

```go
// ✅ 专注于业务逻辑的代码结构
type MessageService struct {
    messageRepo   MessageRepository        // 业务仓储接口
    onlineManager OnlineManager          // IM 业务接口
    pushService   PushService           // IM 业务接口
    logger        clog.Logger           // 基础日志组件
}

// ❌ 避免过度抽象和基础组件污染
type MessageService struct {
    redisConnector connector.RedisConnector  // 过于底层
    cache         cache.Cache               // 业务无关
    config        config.Config             // 业务无关
}
```

### 2. Genesis 组件使用策略

**推荐使用** (解决实际问题):
- `connector`: 统一数据库和缓存连接管理
- `cache`: 在线状态、会话缓存
- `idgen`: 消息 ID、会话 ID 生成
- `mq`: 异步消息处理、离线推送
- `auth`: 用户认证、权限验证

**按需使用** (根据业务复杂度):
- `dlock`: 分布式锁处理群聊消息顺序
- `ratelimit`: 连接限流、消息发送频率控制
- `breaker`: 防止级联故障

**强制使用** (基础能力):
- `clog`: 结构化日志记录
- `config`: 配置管理
- `xerrors`: 错误处理

### 3. IM 业务接口设计

**面向业务接口，而非基础组件**:

```go
// ✅ IM 业务接口 - 隐藏底层实现
type OnlineManager interface {
    UserOnline(userID string) error
    UserOffline(userID string) error
    IsUserOnline(userID string) bool
    GetOnlineUsers() []string
}

type MessageRouter interface {
    RouteMessage(msg *Message) error
    RouteToUser(userID string, msg *Message) error
    RouteToGroup(groupID string, msg *Message) error
}

// 实现 - 使用 Genesis 组件但对外隐藏
type onlineManager struct {
    cache  cache.Cache        // 使用 Genesis cache 组件
    logger clog.Logger        // 使用 Genesis 日志组件
}
```

### 4. 资源管理最佳实践

```go
// 服务级别资源管理
type GatewayServer struct {
    redisConn connector.RedisConnector    // 基础连接
    natConn   connector.NATSConnector     // 消息队列连接

    // 业务组件
    onlineMgr OnlineManager
    pushMgr   PushManager

    logger clog.Logger
}

func NewGatewayServer(cfg *Config) (*GatewayServer, error) {
    logger, _ := clog.New(&cfg.Log)

    // 基础连接 - 使用 Genesis connector
    redisConn, _ := connector.NewRedis(&cfg.Redis, connector.WithLogger(logger))
    natConn, _ := connector.NewNATS(&cfg.NATS, connector.WithLogger(logger))

    // 业务组件 - 注入基础连接
    onlineMgr := NewOnlineManager(redisConn, logger)
    pushMgr := NewPushManager(natConn, logger)

    return &GatewayServer{
        redisConn: redisConn,
        natConn:   natConn,
        onlineMgr: onlineMgr,
        pushMgr:   pushMgr,
        logger:    logger,
    }, nil
}

func (gs *GatewayServer) Close() error {
    // 按相反顺序关闭资源
    gs.pushMgr.Close()
    gs.onlineMgr.Close()
    gs.natConn.Close()
    gs.redisConn.Close()
    return nil
}
```

### 5. 目录结构规范

```
resonance/
├── main.go                    # 统一程序入口，通过 -module 参数区分服务
├── Makefile                   # 构建脚本
├── go.mod                     # Go 模块定义
├── im-api/                    # API 协议定义
│   ├── proto/                 # Protobuf 原文件
│   │   ├── gateway/v1/        # 网关服务协议
│   │   ├── logic/v1/          # 逻辑服务协议
│   │   ├── common/v1/         # 通用类型定义
│   │   └── mq/v1/             # 消息队列协议
│   ├── gen/                   # 生成的代码 (不要手动修改)
│   └── buf.gen.*.yaml         # 代码生成配置
├── im-sdk/                    # 公共 SDK
│   ├── model/                 # IM 业务数据模型
│   └── repo/                  # 仓储层抽象接口
└── internal/                  # 内部实现
    ├── gateway/               # 网关服务实现
    │   ├── connection/        # 连接管理业务
    │   ├── protocol/          # 协议解析业务
    │   └── push/             # 消息推送业务
    ├── logic/                 # 逻辑服务实现
    │   ├── message/          # 消息处理业务
    │   ├── user/             # 用户管理业务
    │   └── group/            # 群组管理业务
    └── task/                  # 任务服务实现
        ├── offline/          # 离线消息处理
        └── notification/     # 通知推送业务
```

## 📚 文档查阅指南

**查阅优先级** (IM 业务优先):

1. **IM 业务文档**: 首先查阅项目的协议定义 (`im-api/proto/`) 和业务接口
2. **Genesis 组件文档**: 遇到基础组件问题时使用 `go doc` 查看
3. **实现示例**: 查看项目中已有的业务逻辑实现
4. **外部文档**: 最后查阅相关技术的外部文档

```bash
# 查看 Genesis 组件 (按需)
go doc -all github.com/ceyewan/genesis/cache
go doc -all github.com/ceyewan/genesis/connector

# 查看项目中的业务接口
go doc -all ./im-sdk/model
go doc -all ./im-sdk/repo
```

## 🎯 IM 开发行为准则

1. **以过度设计为耻，以业务实现为荣**
   - 专注于 IM 核心功能实现，避免为了架构而架构
   - 先解决问题，再考虑优化和重构

2. **以基础组件暴露为耻，以业务封装为荣**
   - 不要在业务代码中直接暴露 Genesis 组件
   - 通过业务接口隐藏底层实现细节

3. **以盲目复制为耻，以场景适配为荣**
   - 理解 Genesis 组件的设计意图，按需使用
   - 根据具体 IM 场景选择合适的组件和配置

4. **以单点瓶颈为耻，以可扩展性为荣**
   - 设计时要考虑消息量增长和用户规模扩展
   - 关键路径要有降级和容错机制

5. **以消息丢失为耻，以可靠性保证为荣**
   - 消息投递要有重试和确认机制
   - 离线消息要保证完整性和时序性

6. **以连接泄漏为耻，以资源管理为荣**
   - WebSocket 连接要有完整的生命周期管理
   - 数据库连接要及时释放，避免连接池耗尽

7. **以忽略性能为耻，以性能监控为荣**
   - 关键业务路径要有性能指标监控
   - 消息处理延迟要持续跟踪和优化

8. **以数据不一致为耻，以一致性保证为荣**
   - 跨服务的状态变更要考虑最终一致性
   - 关键业务操作要有幂等性保证

## 🔄 Git 工作流

### 获取信息

```bash
git status              # 查看当前分支状态
git log --oneline       # 查看提交历史
git diff --cached       # 查看未提交的改动
git diff                # 查看工作区与暂存区的差异
```

**注意**: 禁止使用交互式命令

### 分支命名

**格式**: `<type>/<description>[-suffix]`

**类型**: `feature` | `fix` | `refactor` | `docs` | `chore`

**IM 业务相关示例**:
- `feature/group-message-routing`
- `fix/offline-message-duplicate`
- `refactor/connection-pool-optimization`
- `feat/websocket-heartbeat`

### 提交规范

**格式**: `<type>(<scope>): <subject>`

**类型**: `feat`, `fix`, `refactor`, `docs`, `style`, `test`, `chore`

**作用域** (推荐使用): `gateway`, `logic`, `task`, `api`, `protocol` 等

**主题**: 祈使语气，首字母小写，无句号

**语言**: 中文

**多逻辑变更**: 提供正文（用 `-` 列举）说明"做了什么"和"为什么"

**IM 业务示例**:

```
feat(gateway): 实现消息推送负载均衡

- 添加基于连接数的负载均衡算法选择最优推送节点
- 实现跨节点的消息同步机制，确保消息一致性
- 集成健康检查，自动剔除异常推送节点
- 添加推送成功率指标监控和告警

fix(logic): 修复群聊消息丢失问题

- 添加消息发送的事务性保证，确保原子性操作
- 修复群组成员关系更新时的消息路由错误
- 实现消息重试机制，处理临时网络异常
- 添加消息投递状态的跟踪和记录

feat(protocol): 支持消息撤回功能

- 扩展协议定义，添加消息撤回类型的支持
- 实现撤回权限验证和时间窗口限制
- 处理客户端缓存更新和UI状态同步
- 添加撤回操作的审计日志记录
```

## ⚠️ 重要提醒

1. **业务逻辑优先**: Genesis 是工具，IM 业务才是核心价值
2. **适度使用组件**: 按需引入 Genesis 组件，避免过度工程化
3. **保持代码简洁**: 业务代码应该易于理解和维护
4. **消息可靠性**: 确保消息不丢失、不重复、时序正确
5. **连接管理**: 重点关注 WebSocket 连接的生命周期管理
6. **性能监控**: 持续监控关键性能指标，及时优化
7. **测试覆盖**: 业务逻辑必须有充分的单元测试和集成测试