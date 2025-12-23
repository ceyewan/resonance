# Logic 服务

Logic 是 Resonance IM 系统的核心业务逻辑服务，处理所有业务相关的请求。

## 📐 架构设计

### 核心职责

**业务处理流程**:

1. **接收请求** - 通过 gRPC 接收来自 Gateway 的请求
2. **业务处理** - 验证权限、查询数据、执行业务逻辑
3. **消息发布** - 将需要异步处理的任务发布到 MQ
4. **返回响应** - 将处理结果返回给 Gateway

### 目录结构

```
logic/
├── config.go              # 配置管理
├── logic.go                # 主服务入口
├── README.md               # 服务文档
└── service/                # 业务服务实现
    ├── auth.go             # AuthService - 用户认证
    ├── session.go          # SessionService - 会话管理
    ├── chat.go             # ChatService - 消息处理
    └── gateway_ops.go      # GatewayOpsService - 网关状态同步
```

## 🔄 请求流转

### 完整流程

```
Gateway (gRPC Client)
  ↓
Logic (gRPC Server)
  ↓
[业务服务]
  ├── AuthService    → 验证身份
  ├── SessionService → 会话/联系人管理
  ├── ChatService    → 消息处理 → MQ (PushEvent)
  └── GatewayOpsService → 用户在线状态同步
  ↓
[仓储层]
  ├── UserRepo    → MySQL
  ├── SessionRepo → MySQL
  ├── MessageRepo → MySQL
  └── RouterRepo  → Redis
```

### 消息发送流程

```
Gateway → Logic.ChatService.SendMessage
  ↓
1. 验证会话成员权限
2. 生成 MsgID (Snowflake)
3. 保存消息到 MySQL
4. 更新会话 MaxSeqID
5. 发布 PushEvent 到 MQ
  ↓
Task 服务消费 MQ → 写扩散推送
```

## ⚙️ 配置说明

### 配置结构

```go
type Config struct {
    // 服务基础配置
    ServerAddr string `mapstructure:"server_addr"` // gRPC 服务地址（默认: :9090）

    // 基础组件配置
    Log   clog.Config           // 日志配置
    MySQL connector.MySQLConfig // MySQL 配置
    Redis connector.RedisConfig // Redis 配置
    NATS  connector.NATSConfig  // NATS 配置

    // ID 生成器配置
    IDGen idgen.SnowflakeConfig // Snowflake ID 生成器配置
}
```

### 配置文件示例

```yaml
# config/logic.yaml
server_addr: ":9090"

log:
  level: debug
  format: console

mysql:
  host: 127.0.0.1
  port: 3306
  database: resonance

redis:
  addr: 127.0.0.1:6379

nats:
  url: nats://127.0.0.1:4222

idgen:
  worker_id: 1
  datacenter_id: 1
```

## 🚀 使用示例

```go
package main

import (
    "os"
    "os/signal"
    "syscall"

    "github.com/ceyewan/resonance/logic"
    "github.com/ceyewan/resonance/im-sdk/repo"
)

func main() {
    // 创建配置
    cfg := logic.DefaultConfig()

    // 创建 Logic 实例
    l, err := logic.New(cfg)
    if err != nil {
        panic(err)
    }

    // 注入 Repo 实现（必须）
    l.SetRepositories(userRepo, sessionRepo, messageRepo, routerRepo)

    // 启动服务
    go func() {
        if err := l.Run(); err != nil {
            panic(err)
        }
    }()

    // 等待退出信号
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    <-sigChan

    // 优雅关闭
    l.Close()
}
```

## 🔑 关键组件

### 1. AuthService (认证服务)

**职责**:

- 用户登录验证
- 用户注册
- Token 验证

**RPC 方法**:

- `Login(ctx, LoginRequest) → LoginResponse`
- `Register(ctx, RegisterRequest) → RegisterResponse`
- `ValidateToken(ctx, ValidateTokenRequest) → ValidateTokenResponse`

### 2. SessionService (会话服务)

**职责**:

- 会话列表查询
- 创建会话（单聊/群聊）
- 历史消息拉取
- 联系人管理
- 用户搜索

**RPC 方法**:

- `GetSessionList(ctx, GetSessionListRequest) → GetSessionListResponse`
- `CreateSession(ctx, CreateSessionRequest) → CreateSessionResponse`
- `GetRecentMessages(ctx, GetRecentMessagesRequest) → GetRecentMessagesResponse`
- `GetContactList(ctx, GetContactListRequest) → GetContactListResponse`
- `SearchUser(ctx, SearchUserRequest) → SearchUserResponse`

### 3. ChatService (聊天服务)

**职责**:

- 接收上行消息（双向流）
- 验证会话权限
- 生成消息 ID
- 保存消息到数据库
- 发布 PushEvent 到 MQ

**RPC 方法**:

- `SendMessage(stream) → (stream)` - 双向流，持续接收和响应

**消息处理流程**:

```go
// 1. 验证会话成员
members := sessionRepo.GetMembers(sessionID)

// 2. 生成 MsgID (Snowflake)
msgID, _ := idGen.NextInt64()

// 3. 保存消息
messageRepo.SaveMessage(&MessageContent{
    MsgID:          msgID,
    SessionID:      sessionID,
    SenderUsername: from,
    SeqID:          seqID,
    Content:        content,
    MsgType:        msgType,
})

// 4. 发布到 MQ（Task 服务消费）
eventData := proto.Marshal(&mqv1.PushEvent{...})
mqClient.Publish(ctx, "resonance.push.event.v1", eventData)
```

### 4. GatewayOpsService (网关操作服务)

**职责**:

- 同步用户上线状态
- 同步用户下线状态
- 维护用户路由信息（RouterRepo）

**RPC 方法**:

- `SyncState(stream) → (stream)` - 双向流，持续接收状态更新

**状态同步流程**:

```go
// 用户上线
routerRepo.SetUserGateway(ctx, &model.Router{
    Username:  username,
    GatewayID: gatewayID,
    RemoteIP:  remoteIP,
    Timestamp: timestamp,
})

// 用户下线
routerRepo.DeleteUserGateway(ctx, username)
```

## 📊 设计要点

### 消息可靠性

1. **消息存储** - 消息先保存到 MySQL，再发布到 MQ
2. **幂等性** - 使用 (MsgID, SeqID) 作为唯一标识
3. **异步处理** - 写扩散由 Task 服务异步处理，Logic 不阻塞

### 会话 ID 生成

- **单聊**: `single:user1:user2`（按字母排序保证唯一性）
- **群聊**: `group:{UUID}`（待完善）

### SeqID 管理

- 每个会话维护一个 MaxSeqID
- 每条消息的 SeqID = MaxSeqID + 1
- 未读数 = MaxSeqID - User.LastReadSeq

## 📝 待完善功能

- [ ] Token 实现和验证（当前简化实现）
- [ ] 密码加密（bcrypt）
- [ ] 群聊 ID 生成（使用 UUID 或 ID 生成器）
- [ ] 离线消息处理
- [ ] 消息撤回
- [ ] 消息编辑
- [ ] 群成员管理
- [ ] 单元测试和集成测试
