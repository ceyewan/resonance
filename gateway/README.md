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
3. **推送消息到 Logic** - 将客户端消息通过分发器转发到 Logic (ChatService)
4. **接收 Task 推送** - 接收 Task 服务下发的消息并推送给 WebSocket 客户端 (PushService)

### 目录结构 (重构后)

```
gateway/
├── gateway.go             # 【极简入口】负责组件组装与生命周期管理
├── config/                # 配置管理定义与加载逻辑
├── server/                # 服务层封装 (HTTP, gRPC)
│   ├── http.go            # HTTP Server (Gin 路由与中间件 + WS 入口)
│   └── grpc.go            # gRPC Server (Push 推送服务)
├── handler/               # 业务逻辑处理器 (原 api 目录)
│   ├── handler.go         # RESTful API 实现 (AuthService, SessionService)
│   └── middleware.go      # HTTP 中间件 (限流、日志、恢复)
├── socket/                # WebSocket 核心逻辑
│   ├── handler.go         # 连接握手、鉴权与 Conn 生命周期管理
│   └── dispatcher.go      # 业务消息分发 (Pulse, Chat, Ack)
├── connection/            # WebSocket 连接底层管理
│   ├── manager.go         # 连接池管理器 (Pool)
│   ├── conn.go            # 单个连接封装 (Read/Write Loop)
│   └── presence.go        # 用户上下线状态同步回调
├── client/                # 外部 RPC 客户端
├── protocol/              # 协议编解码与 Handler 接口定义
├── push/                  # 推送服务端实现
└── utils/                 # 通用工具函数 (网络 IP 获取等)
```

## 🔌 接口说明

### 1. RESTful API (HTTP)

**端口**: 配置的 `http_addr` (默认 `:8080`)

**服务**:

- `AuthService` - 用户认证
- `SessionService` - 会话管理

**实现**: 由 `gateway/handler` 处理并转发到 Logic 服务。

### 2. WebSocket 接口

**端口**: 复用 `http_addr` (默认 `:8080`)

**连接**: `ws://host:port/ws?token=<access_token>`

**处理流程**:
1. `server/http.go` 中注册 `/ws` 路由并接受请求。
2. `socket/handler.go` 处理握手、Token 鉴权、创建 `connection.Conn`。
3. `socket/dispatcher.go` 处理业务层 packet 分发。

### 3. Push RPC 接口 (内部)

**端口**: 固定端口 `:15091` (gRPC)

**服务**: `PushService`

**调用方**: Task 服务。

## 🔄 核心机制

### 消息分发 (Dispatcher)

重构后引入了 `socket.Dispatcher` 结构，取代了原有的闭包回调机制。它负责：
- 处理心跳 (`HandlePulse`)
- 处理聊天请求 (`HandleChat`) 并调用 Logic 服务。
- 处理确认包 (`HandleAck`)。

### 服务化启动 (Server)

`Gateway` 结构体通过持有 `server.HTTPServer` 和 `server.GRPCServer` 实例，实现了高层次的解耦。每个 Server 负责其特有的启动细节、超时设置和优雅关闭。

## ⚙️ 配置说明

使用 `gateway/config` 包进行加载：
- 支持环境变量、`.env` 文件和 YAML 配置文件。
- 核心配置包括 `Service`, `LogicAddr`, `Log`, `Etcd`, `WSConfig` 等。

## 🚀 使用示例

```go
package main

import (
    "github.com/ceyewan/resonance/gateway"
)

func main() {
    // 创建 Gateway 实例 (自动加载配置)
    gw, err := gateway.New()
    if err != nil {
        panic(err)
    }

    // 启动所有服务 (HTTP, gRPC)
    if err := gw.Run(); err != nil {
        panic(err)
    }

    // 优雅关闭
    defer gw.Close()
}
```

## 📝 待完善功能

- [x] 配置文件加载与结构化
- [x] 获取本机 IP 的工具函数提取
- [x] 服务的模块化重构
- [ ] Redis 集成 (用于跨网关的在线状态同步)
- [ ] 监控指标上报 (Prometheus)
- [ ] 单元测试覆盖
