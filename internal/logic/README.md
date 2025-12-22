# Logic 服务框架

Logic 是 Resonance IM 系统的核心业务逻辑服务，负责处理认证、会话管理、消息路由和状态同步。

## 📐 架构设计

### 核心职责

**对外提供 RPC 服务** (ConnectRPC over HTTP/2):
1. **AuthService** - 用户认证（登录、注册、Token 验证）
2. **SessionService** - 会话管理（会话列表、创建会话、历史消息、联系人、搜索）
3. **ChatService** - 消息处理（接收消息、生成 ID、转发到 MQ）
4. **GatewayOpsService** - 网关状态同步（用户上下线）

**对内依赖**:
1. **Repo 层** - 数据存储抽象接口（User/Token/Session/Contact/Message）
2. **Genesis 组件** - 日志、配置、数据库、缓存、MQ、ID 生成器
3. **MQ** - 消息队列（发送 PushEvent 到 Task 服务）

### 目录结构

```
internal/logic/
├── config.go              # 配置管理
├── logic.go               # 主服务入口
├── README.md              # 服务文档
└── service/               # 服务层实现
    ├── auth.go            # AuthService 实现
    ├── session.go         # SessionService 实现
    ├── chat.go            # ChatService 实现
    └── gateway_ops.go     # GatewayOpsService 实现
```

## 🔌 服务接口

### 1. AuthService

**用户认证服务**

- `Login(LoginRequest) -> LoginResponse`
  - 验证用户名密码
  - 生成访问 Token
  - 返回用户信息

- `Register(RegisterRequest) -> RegisterResponse`
  - 创建新用户
  - 生成访问 Token
  - 返回用户信息

- `ValidateToken(ValidateTokenRequest) -> ValidateTokenResponse`
  - 验证 Token 有效性
  - 返回用户信息

### 2. SessionService

**会话管理服务**

- `GetSessionList(GetSessionListRequest) -> GetSessionListResponse`
  - 获取用户的所有会话
  - 包含每个会话的最后一条消息和未读数

- `CreateSession(CreateSessionRequest) -> CreateSessionResponse`
  - 创建单聊或群聊会话
  - 返回会话 ID

- `GetRecentMessages(GetRecentMessagesRequest) -> GetRecentMessagesResponse`
  - 获取会话的历史消息
  - 支持分页（before_seq）

- `GetContactList(GetContactListRequest) -> GetContactListResponse`
  - 获取用户的联系人列表

- `SearchUser(SearchUserRequest) -> SearchUserResponse`
  - 搜索用户（用于发起新聊天）

### 3. ChatService

**消息处理服务（双向流）**

- `SendMessage(stream SendMessageRequest) -> stream SendMessageResponse`
  - 接收 Gateway 转发的客户端消息
  - 验证会话成员权限
  - 生成消息 ID (Snowflake) 和序列号 (Seq ID)
  - 保存消息到数据库
  - 发布 PushEvent 到 MQ（转发给 Task 服务）
  - 返回消息 ID 和序列号

### 4. GatewayOpsService

**网关状态同步服务（双向流）**

- `SyncState(stream SyncStateRequest) -> stream SyncStateResponse`
  - 接收 Gateway 上报的用户上下线事件
  - 将在线状态存储到 Redis (`user:online:{username}` -> `{gateway_id}`)
  - 返回确认响应

## 🔄 消息流转

### 上行消息处理

```
Gateway (ChatRequest) → Logic (ChatService) → [生成 ID] → [保存 DB] → MQ (PushEvent) → Task
```

1. Gateway 通过 `ChatService` 双向流发送消息
2. Logic 验证会话成员权限
3. 生成 `msg_id` (Snowflake) 和 `seq_id` (会话内递增)
4. 保存消息到数据库
5. 发布 `PushEvent` 到 NATS
6. 返回响应给 Gateway

### 状态同步

```
Gateway (UserOnline/UserOffline) → Logic (GatewayOpsService) → Redis (在线状态)
```

1. Gateway 通过 `GatewayOpsService` 双向流上报状态
2. Logic 更新 Redis 中的在线状态
3. 返回确认响应

## ⚙️ 配置说明

```go
type Config struct {
    ServerAddr string // gRPC 服务地址 (默认 :9090)

    Log   clog.Config           // 日志配置
    MySQL connector.MySQLConfig // MySQL 配置
    Redis connector.RedisConfig // Redis 配置
    NATS  connector.NATSConfig  // NATS 配置

    IDGen idgen.SnowflakeConfig // Snowflake ID 生成器配置
}
```

## 🚀 使用示例

```go
package main

import (
    "github.com/ceyewan/resonance/internal/logic"
    "github.com/ceyewan/resonance/im-sdk/repo"
)

func main() {
    // 创建配置
    cfg := logic.DefaultConfig()
    cfg.ServerAddr = ":9090"

    // 创建 Logic 实例
    l, err := logic.New(cfg)
    if err != nil {
        panic(err)
    }

    // 注入 Repo 实现（需要自己实现）
    l.SetRepositories(
        userRepo,    // repo.UserRepository
        tokenRepo,   // repo.TokenRepository
        sessionRepo, // repo.SessionRepository
        contactRepo, // repo.ContactRepository
        messageRepo, // repo.MessageRepository
    )

    // 启动服务
    if err := l.Run(); err != nil {
        panic(err)
    }

    // 等待退出信号...

    // 优雅关闭
    l.Close()
}
```

## 🔑 关键组件

### ID 生成器

使用 Snowflake 算法生成全局唯一的消息 ID：
- 64 位整数
- 包含时间戳、数据中心 ID、工作节点 ID、序列号
- 保证分布式环境下的唯一性和有序性

### 序列号管理

每个会话维护独立的递增序列号：
- 用于消息排序和去重
- 支持客户端增量同步
- 通过 `MessageRepository.GetNextSeqID` 获取

### 在线状态管理

使用 Redis 存储用户在线状态：
- Key: `user:online:{username}`
- Value: `{gateway_id}` (用户所在的网关实例)
- 用于消息路由和推送

### MQ 消息发布

将消息发布到 NATS：
- Topic: `resonance.push.event.v1` (定义在 `mq/v1/event.proto`)
- Payload: `PushEvent` (包含完整消息信息)
- Task 服务订阅并处理推送逻辑

## 📦 依赖的 Repo 接口

Logic 服务依赖以下仓储接口（需要外部实现）：

- `UserRepository` - 用户管理
- `TokenRepository` - Token 管理
- `SessionRepository` - 会话管理
- `ContactRepository` - 联系人管理
- `MessageRepository` - 消息存储

接口定义位于 `im-sdk/repo/` 目录。

## 📝 待完善功能

- [ ] 配置文件加载
- [ ] 群组权限管理
- [ ] 消息撤回功能
- [ ] 消息已读回执
- [ ] 用户黑名单
- [ ] 敏感词过滤
- [ ] 消息审计日志
- [ ] 性能监控和指标上报
- [ ] 单元测试和集成测试

