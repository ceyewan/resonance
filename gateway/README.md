# Gateway 服务

Gateway 是 Resonance IM 系统的网关服务，负责处理客户端连接、消息转发和状态同步。

## 📐 架构设计

### 核心职责

```
┌─────────────┐                    ┌─────────────┐
│     Web     │                    │     Task    │
│  (React)    │                    │             │
└──────┬──────┘                    └──────▲──────┘
       │                                  │ gRPC
       │ HTTP/WS                          │ Push
       ▼                                  │
┌──────────────────────────────────────────────────┐
│                    Gateway                       │
├──────────────────────────────────────────────────┤
│  ┌─────────┐  ┌─────────┐  ┌─────────────────┐  │
│  │ HTTP    │  │ WebSocket│  │ gRPC Push       │  │
│  │ Server  │  │ Upgrader │  │ Server          │  │
│  └────┬────┘  └────┬─────┘  └────────┬────────┘  │
│       │            │                  │           │
│  ┌────▼─────┐  ┌───▼────────┐   ┌────▼──────┐    │
│  │  HTTP    │  │ Dispatcher │   │ Presence  │    │
│  │  API     │  │ (Chat/Pulse)│   │ Batcher   │    │
│  └────┬─────┘  └────────────┘   └────┬──────┘    │
└───────┼───────────────────────────────┼──────────┘
        │                               │
        │ gRPC                          │ gRPC
        ▼                               ▼
┌─────────────┐                   ┌─────────────┐
│    Logic    │                   │    Etcd     │
└─────────────┘                   │   Registry  │
                                   └─────────────┘
```

**对外接口**：

- **RESTful API** (Gin) - 认证、会话管理接口
- **WebSocket** - 实时消息通道（Protobuf 序列化）

**对内功能**：

- **转发 HTTP 请求** - 通过 gRPC 客户端转发到 Logic 服务
- **上报状态** - 批量同步用户上下线到 Logic PresenceService
- **接收推送** - 接收 Task 服务的 Push 请求并转发给 WebSocket 客户端

### 目录结构

```
gateway/
├── gateway.go             # 生命周期管理、组件组装、优雅关闭
├── config/                # 配置定义与加载
│   └── config.go          # Config 结构体 + StatusBatcherConfig
├── server/                # 服务层封装
│   ├── http.go            # HTTP Server (Gin 路由 + WS 入口)
│   └── grpc.go            # gRPC Server (Push 推送服务)
├── api/                   # RESTful API 实现
│   ├── httpapi.go         # AuthService/SessionService 处理器
│   ├── routes.go          # 路由注册
│   └── middleware.go      # 中间件 (CORS/Logger/Recovery)
├── middleware/            # 独立中间件包
│   ├── auth.go            # JWT 认证
│   ├── cors.go            # 跨域处理
│   ├── logger.go          # 日志记录
│   ├── ratelimit.go       # 限流
│   ├── recovery.go        # 恢复 panic
│   └── trace.go           # OpenTelemetry Trace
├── ws/                    # WebSocket 核心逻辑
│   ├── upgrader.go        # 连接握手、鉴权
│   └── dispatcher.go      # 消息分发 (Chat/Pulse/Ack)
├── connection/            # WebSocket 连接管理
│   ├── manager.go         # 连接池管理器
│   ├── conn.go            # 单个连接封装 (Read/Write Loop)
│   └── callback.go        # 状态同步回调接口
├── push/                  # gRPC Push 服务实现
│   └── service.go         # PushMessage 推送给客户端
├── client/                # Logic RPC 客户端
│   ├── client.go          # gRPC 连接管理
│   ├── batcher.go         # StatusBatcher 状态批量同步
│   ├── services.go        # Logic 服务封装
│   └── config.go          # 客户端配置
├── protocol/              # 协议编解码
│   └── codec.go           # Protobuf 编解码
├── observability/         # 可观测性
│   ├── observability.go   # Trace/Metrics 初始化、记录函数
│   └── config.go          # 可观测性配置
└── README.md
```

## ⚙️ 配置说明

配置加载顺序：环境变量 > `.env` > `gateway.{env}.yaml` > `gateway.yaml`

### 核心配置项

```yaml
service:
    name: gateway-service
    http_port: 8080 # HTTP/WebSocket 服务端口
    grpc_port: 15091 # gRPC Push 服务端口

# Logic 服务名称（用于服务发现）
logic_service_name: logic-service

# WebSocket 配置
ws_config:
    max_message_size: 1048576 # 1MB
    ping_interval: 30 # 秒
    pong_timeout: 60 # 秒

# 可观测性配置
observability:
    trace:
        disable: false # 是否禁用 Trace 上报
        endpoint: localhost:4317 # OTLP Collector 地址
        sampler: 1.0 # 采样率
    metrics:
        port: 9092 # Prometheus 端口
        path: /metrics

# StatusBatcher 配置
status_batcher:
    batch_size: 50 # 批量大小阈值
    flush_interval: 100ms # 刷新间隔
```

## 🔌 接口说明

### 1. RESTful API (HTTP)

**端口**：`http_port` (默认 `8080`)

| 端点                       | 方法 | 说明         |
| -------------------------- | ---- | ------------ |
| `/api/v1/auth/login`       | POST | 用户登录     |
| `/api/v1/auth/register`    | POST | 用户注册     |
| `/api/v1/session/list`     | GET  | 获取会话列表 |
| `/api/v1/session/create`   | POST | 创建会话     |
| `/api/v1/session/messages` | GET  | 获取历史消息 |
| `/api/v1/session/search`   | GET  | 搜索用户     |

### 2. WebSocket 接口

**连接**：`ws://host:port/ws?token=<access_token>`

**消息格式**：Protobuf 二进制

| 消息类型 | 说明     |
| -------- | -------- |
| Pulse    | 心跳保活 |
| Chat     | 聊天消息 |
| Ack      | 消息确认 |

### 3. Push RPC 接口 (内部)

**端口**：`grpc_port` (默认 `15091`)

**服务**：`PushService` - 接收 Task 服务的推送请求

## 🔧 核心机制

### StatusBatcher 状态批量同步

**双重触发机制**：

- **数量触发**：当缓冲区达到 `batch_size` 时立即刷新
- **时间触发**：每隔 `flush_interval` 强制刷新

```
用户上线/下线 → 缓冲区 → 批量同步到 Logic PresenceService
                 (onlineBuf/offlineBuf)
                 ↓
              达到阈值 OR 超时
                 ↓
              SyncStatus RPC
```

**优势**：

- 减少 RPC 调用次数，提升性能
- 应对重连风暴（大量用户同时上线）

### WebSocket 连接管理

**连接生命周期**：

1. **握手**：`ws/upgrader.go` 验证 Token，升级协议
2. **创建连接**：`connection/conn.go` 启动 Read/Write Loop
3. **消息分发**：`ws/dispatcher.go` 根据 Packet Type 路由
4. **关闭**：清理资源，触发状态回调

**心跳机制**：

- 服务端定期发送 Ping
- 客户端回复 Pong (Pulse 消息)
- 超时未回复则断开连接

### gRPC Push 服务

**推送流程**：

```
Task 服务 → Gateway PushService → WebSocket 连接 → 客户端
```

**特点**：

- 支持批量推送（单次 RPC 推送多个用户）
- 查找本地连接，跨网关用户忽略
- 推送失败记录指标

## 📊 可观测性

### Trace（分布式追踪）

- OpenTelemetry OTLP 上报
- HTTP/WebSocket/gRPC 请求自动追踪
- 跨服务传播 Trace Context（通过 gRPC metadata）

### Metrics（业务指标）

| 指标名称                                | 类型      | 说明           |
| --------------------------------------- | --------- | -------------- |
| `gateway_websocket_connections_active`  | Gauge     | 当前活跃连接数 |
| `gateway_websocket_connections_total`   | Counter   | 累计连接数     |
| `gateway_messages_pulse_total`          | Counter   | 心跳消息数     |
| `gateway_messages_received_total`       | Counter   | 接收聊天消息数 |
| `gateway_messages_sent_total`           | Counter   | 推送消息数     |
| `gateway_push_duration_seconds`         | Histogram | 推送延迟分布   |
| `gateway_push_failed_total`             | Counter   | 推送失败数     |
| `gateway_http_requests_total`           | Counter   | HTTP 请求总数  |
| `gateway_http_request_duration_seconds` | Histogram | HTTP 请求延迟  |
| `gateway_http_errors_total`             | Counter   | HTTP 错误数    |
| `gateway_grpc_requests_total`           | Counter   | gRPC 请求总数  |
| `gateway_grpc_request_duration_seconds` | Histogram | gRPC 请求延迟  |
| `gateway_grpc_errors_total`             | Counter   | gRPC 错误数    |

访问 `http://localhost:9092/metrics` 查看 Prometheus 指标。

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
    defer gw.Close()

    // 启动服务
    if err := gw.Run(); err != nil {
        panic(err)
    }

    // 等待关闭信号
    <-gw.Done()
}
```

## 📝 已实现功能

- [x] RESTful API (认证、会话管理)
- [x] WebSocket 连接管理
- [x] Protobuf 消息编解码
- [x] 心跳保活机制
- [x] gRPC Push 服务
- [x] StatusBatcher 状态批量同步
- [x] Trace 链路追踪（OpenTelemetry）
- [x] Prometheus 业务指标
- [x] JWT 认证中间件
- [x] CORS 跨域处理
- [x] 服务注册发现（Etcd）

## 🚧 待完善功能

- [ ] 连接限流（防止连接数过多）
- [ ] IP 黑名单/白名单
- [ ] 消息压缩
- [ ] 单元测试覆盖
- [ ] 健康检查端点（/healthz）

## 📚 相关文档

- [项目整体 CLAUDE.md](../CLAUDE.md)
- [Logic 服务文档](../logic/README.md)
- [Task 服务文档](../task/README.md)
- [Genesis 组件文档](https://github.com/ceyewan/genesis)
