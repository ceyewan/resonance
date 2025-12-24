# Logic 服务

Logic 是 Resonance IM 系统的核心业务逻辑服务，处理所有业务相关的请求。

## 📐 架构设计

### 核心职责

**业务处理流程**:

1. **接收请求** - 通过 gRPC 接收来自 Gateway 的请求
2. **业务处理** - 验证权限、查询数据、执行业务逻辑
3. **消息发布** - 将需要异步处理的任务发布到 MQ
4. **返回响应** - 将处理结果返回给 Gateway

### 目录结构 (重构后)

```
logic/
├── logic.go                # 【极简入口】负责组件组装与生命周期管理
├── config/                 # 配置管理定义与加载逻辑
├── server/                 # gRPC Server 封装
│   └── grpc.go             # gRPC Server 启动逻辑与拦截器配置
├── service/                # 业务服务实现 (Auth, Session, Chat, GatewayOps)
└── README.md               # 服务文档
```

## ⚙️ 配置说明

使用 `logic/config` 包加载配置：

```go
type Config struct {
    // 服务基础配置
    Service struct {
        Name       string // 服务名称
        ServerAddr string // gRPC 服务地址
    }

    // 基础组件配置
    Log   clog.Config
    MySQL connector.MySQLConfig
    Redis connector.RedisConfig
    NATS  connector.NATSConfig
    Etcd  connector.EtcdConfig

    // 服务注册
    Registry RegistryConfig

    // ID 生成器
    IDGen idgen.SnowflakeConfig

    // 认证配置
    Auth auth.Config
}
```

## 🚀 使用示例

```go
package main

import (
    "github.com/ceyewan/resonance/logic"
)

func main() {
    // 创建 Logic 实例 (自动加载配置)
    l, err := logic.New()
    if err != nil {
        panic(err)
    }

    // 启动服务
    if err := l.Run(); err != nil {
        panic(err)
    }

    // 优雅关闭
    defer l.Close()
}
```

## 🔑 关键组件

### 1. AuthService (认证服务)

- 使用 `genesis/auth` (JWT) 进行 Token 签发与验证。
- 依赖 `UserRepo` 进行用户数据存取。

### 2. SessionService (会话服务)

- 依赖 `SessionRepo` 管理会话。
- 使用 `idgen` (UUID) 生成 GroupID。

### 3. ChatService (聊天服务)

- 使用 `idgen` (Snowflake) 生成全局唯一 MsgID。
- 消息持久化到 MySQL (`MessageRepo`)。
- 消息投递到 NATS (`mqClient`)。

### 4. GatewayOpsService (网关操作服务)

- 接收 Gateway 上报的用户在线状态。
- 更新 Redis 中的路由表 (`RouterRepo`)。

## 📝 待完善功能

- [x] Token 实现和验证 (已接入 genesis/auth)
- [ ] 密码加密（bcrypt）
- [x] 群聊 ID 生成（使用 UUID）
- [ ] 离线消息处理
- [ ] 消息撤回
- [ ] 消息编辑
- [ ] 群成员管理
- [ ] 单元测试和集成测试
