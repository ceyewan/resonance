# Gateway 服务框架

Gateway 是 Resonance IM 系统的网关服务，负责处理客户端连接、消息转发和状态同步。

## 📐 架构设计

### 核心职责

**对外接口**:

1. **RESTful API** (Gin + ConnectRPC) - 提供认证和会话管理接口
2. **WebSocket 接口** - 使用 Protobuf 序列化的实时消息通道

**对内功能**:

1. **转发 RESTful API** - 通过 Logic RPC 客户端转发 HTTP 请求到 Logic 服务
2. **上报用户状态** - 同步用户上下线状态到 Logic (GatewayOpsService)
3. **推送消息到 Logic** - 将客户端消息通过双向流转发到 Logic (ChatService)
4. **接收 Task 推送** - 接收 Task 服务下发的消息并推送给 WebSocket 客户端 (PushService)

### 目录结构

```
gateway/
├── config.go              # 配置管理
├── gateway.go             # 主服务入口
├── client/                # RPC 客户端
│   └── logic.go           # Logic gRPC 客户端封装
├── api/                   # HTTP API 处理
│   └── handler.go         # RESTful API Handler (AuthService, SessionService)
├── connection/            # WebSocket 连接管理
│   ├── manager.go         # 连接池管理器
│   └── conn.go            # 单个连接封装
├── protocol/              # 协议处理
│   └── handler.go         # WsPacket 序列化/反序列化和消息分发
└── push/                  # 推送服务
    └── service.go         # PushService 实现 (接收 Task 推送)
```

## 🔌 接口说明

### 1. RESTful API (HTTP)

**端口**: 配置的 `http_addr` (默认 `:8080`)

**服务**:

- `AuthService` - 用户认证
  - `POST /resonance.gateway.v1.AuthService/Login` - 登录
  - `POST /resonance.gateway.v1.AuthService/Register` - 注册
  - `POST /resonance.gateway.v1.AuthService/Logout` - 登出

- `SessionService` - 会话管理
  - `POST /resonance.gateway.v1.SessionService/GetSessionList` - 获取会话列表
  - `POST /resonance.gateway.v1.SessionService/CreateSession` - 创建会话
  - `POST /resonance.gateway.v1.SessionService/GetRecentMessages` - 获取历史消息
  - `POST /resonance.gateway.v1.SessionService/GetContactList` - 获取联系人列表
  - `POST /resonance.gateway.v1.SessionService/SearchUser` - 搜索用户

**实现**: 所有请求都会转发到 Logic 服务处理

### 2. WebSocket 接口

**端口**: 配置的 `ws_addr` (默认 `:8081`)

**连接**: `ws://host:port/ws?token=<access_token>`

**协议**: 使用 `WsPacket` (Protobuf) 封装所有消息

**消息类型**:

- `Pulse` - 心跳消息
- `ChatRequest` - 客户端发送的聊天消息
- `PushMessage` - 服务端推送的消息
- `Ack` - 消息确认

**流程**:

1. 客户端携带 token 建立 WebSocket 连接
2. Gateway 验证 token 并建立连接
3. 客户端发送 `ChatRequest`，Gateway 转发到 Logic
4. Task 通过 `PushService` 推送消息，Gateway 转发给客户端

### 3. Push RPC 接口 (内部)

**端口**: 配置的 `http_addr` (与 RESTful API 共用)

**服务**: `PushService`

- `POST /resonance.gateway.v1.PushService/PushMessage` - 双向流推送消息

**调用方**: Task 服务

## 🔄 消息流转

### 上行消息 (客户端 → Logic)

```
Client (WS) → Gateway (protocol.Handler) → Logic (ChatService 双向流)
```

1. 客户端通过 WebSocket 发送 `ChatRequest`
2. Gateway 的 `protocol.Handler` 解析消息
3. Gateway 调用 `onChat` 回调，填充发送者和时间戳
4. 通过 `LogicClient.SendMessage` 转发到 Logic 的 `ChatService`

### 下行消息 (Task → 客户端)

```
Task (gRPC) → Gateway (PushService) → Client (WS)
```

1. Task 通过 `PushService` 双向流推送 `PushMessageRequest`
2. Gateway 的 `push.Service` 接收请求
3. 通过 `connection.Manager` 查找目标用户连接
4. 将 `PushMessage` 封装为 `WsPacket` 推送给客户端

### 状态同步 (Gateway → Logic)

```
Gateway (连接事件) → Logic (GatewayOpsService 双向流)
```

1. 用户建立/断开 WebSocket 连接
2. `connection.Manager` 触发 `onConnect`/`onDisconnect` 回调
3. 通过 `LogicClient.SyncUserOnline`/`SyncUserOffline` 上报状态
4. Logic 的 `GatewayOpsService` 接收并处理状态变更

## ⚙️ 配置说明

```go
type Config struct {
    GatewayID string // 网关实例 ID (用于多网关部署)
    HTTPAddr  string // HTTP 服务地址 (RESTful API + Push RPC)
    WSAddr    string // WebSocket 服务地址
    LogicAddr string // Logic 服务地址

    Log   clog.Config          // 日志配置
    Redis connector.RedisConfig // Redis 配置 (预留)
    NATS  connector.NATSConfig  // NATS 配置 (预留)

    WSConfig WSConfig // WebSocket 配置
}

type WSConfig struct {
    ReadBufferSize  int // 读缓冲区大小
    WriteBufferSize int // 写缓冲区大小
    MaxMessageSize  int // 最大消息大小
    PingInterval    int // 心跳间隔 (秒)
    PongTimeout     int // 心跳超时 (秒)
}
```

## 🚀 使用示例

```go
package main

import (
    "github.com/ceyewan/resonance/gateway"
)

func main() {
    // 创建配置
    cfg := gateway.DefaultConfig()
    cfg.GatewayID = "gateway-1"
    cfg.HTTPAddr = ":8080"
    cfg.WSAddr = ":8081"
    cfg.LogicAddr = "localhost:9090"

    // 创建 Gateway 实例
    gw, err := gateway.New(cfg)
    if err != nil {
        panic(err)
    }

    // 启动服务
    if err := gw.Run(); err != nil {
        panic(err)
    }

    // 等待退出信号...

    // 优雅关闭
    gw.Close()
}
```

## 🔑 关键组件

### LogicClient

封装与 Logic 服务的所有 RPC 调用，维护双向流连接：

- `chatStream` - 转发客户端消息到 Logic
- `gatewayOpsStream` - 同步用户状态到 Logic

### connection.Manager

管理所有 WebSocket 连接：

- 连接池管理 (username → Conn)
- 上下线回调触发
- 消息推送路由

### protocol.Handler

处理 WebSocket 消息分发：

- 解析 `WsPacket`
- 根据消息类型调用对应回调 (onPulse/onChat/onAck)

### push.Service

实现 `PushService` 接口：

- 接收 Task 的推送请求
- 查找目标用户连接并转发消息
- 返回推送结果

## 📝 待完善功能

- [ ] 配置文件加载 (目前使用硬编码配置)
- [ ] Redis 集成 (用于跨网关的在线状态同步)
- [ ] NATS 集成 (用于跨网关的消息路由)
- [ ] 消息确认机制完善 (Ack 处理)
- [ ] 连接限流和防护
- [ ] 监控指标上报
- [ ] 单元测试和集成测试
