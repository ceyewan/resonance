# 📐 IM API 架构说明

## 🎯 设计原则

- **对外（Web/客户端）**：走 Connect 协议（HTTP/1.1 + JSON 即可），生成 Connect-Go handler + Connect-ES TypeScript client。
- **对内（服务间）**：一律走原生 gRPC（HTTP/2 + protobuf），不生成 Connect-Go、不生成 TypeScript。
- 结论：**TS 只生成"Web 会调用或收到的"那部分 proto**，其余内部协议只生成 Go。

---

### 1. Gateway 对外 API（客户端访问）

**协议文件**：

- `proto/gateway/v1/auth.proto`
- `proto/gateway/v1/session.proto`

**生成代码**：

- ✅ gRPC Server/Client (`auth_grpc.pb.go`, `session_grpc.pb.go`) — 保留，方便调试和兼容。
- ✅ Connect-Go handler (`gatewayv1connect/auth.connect.go`, `session.connect.go`) — 对 Web 暴露。
- ✅ TypeScript client (`gen/ts/gateway/v1/auth_pb.ts`, `session_pb.ts`)。

**使用场景**：浏览器/移动端客户端通过 HTTP/1.1 + JSON 访问。

**服务端代码示例**：

```go
import (
    "net/http"

    gatewayv1connect "github.com/ceyewan/resonance/api/gen/go/gateway/v1/gatewayv1connect"
)

path, handler := gatewayv1connect.NewAuthServiceHandler(&authHandler{})
http.Handle(path, handler)
```

**前端代码示例**（Connect-ES v2：Service 在 `*_pb.ts` 里）：

```typescript
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AuthService } from "@gen/gateway/v1/auth_pb";

const transport = createConnectTransport({
    baseUrl: "http://localhost:8080",
});
const client = createClient(AuthService, transport);

// 发送: POST /resonance.gateway.v1.AuthService/Login
const response = await client.login({
    username: "user123",
    password: "pass456",
});
```

---

### 2. Gateway Packet（WebSocket 载荷结构）

**协议文件**：`proto/gateway/v1/packet.proto`

**生成代码**：

- ✅ Go message（在 Gateway 内部处理 WS 帧时使用）。
- ✅ TypeScript message（前端序列化 / 反序列化 WS 帧）。
- ❌ 无 RPC（WebSocket 不走 Connect RPC）。

---

### 3. Gateway Push（内部 Task → Gateway）

**协议文件**：`proto/gateway/v1/push.proto`

**生成代码**：

- ✅ gRPC Server/Client (`push_grpc.pb.go`)。
- ❌ 不生成 Connect-Go。
- ❌ 不生成 TypeScript。

**使用场景**：Task 服务推送消息给 Gateway（服务间调用）。

```go
import (
    gatewayv1 "github.com/ceyewan/resonance/api/gen/go/gateway/v1"
    "google.golang.org/grpc"
)

conn, _ := grpc.NewClient("gateway:9090", grpc.WithTransportCredentials(insecure.NewCredentials()))
client := gatewayv1.NewPushServiceClient(conn)
```

---

### 4. Logic（内部服务间调用）

**协议文件**：

- `proto/logic/v1/auth.proto`
- `proto/logic/v1/session.proto`
- `proto/logic/v1/chat.proto`
- `proto/logic/v1/presence.proto`

**生成代码**：

- ✅ gRPC Server/Client (`*_grpc.pb.go`)。
- ❌ 不生成 Connect-Go。
- ❌ 不生成 TypeScript。

**使用场景**：Gateway → Logic 服务间调用（原生 gRPC，性能更好）。

```go
import (
    logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
    "google.golang.org/grpc"
)

conn, _ := grpc.NewClient("logic:9091", grpc.WithTransportCredentials(insecure.NewCredentials()))
authClient := logicv1.NewAuthServiceClient(conn)
sessionClient := logicv1.NewSessionServiceClient(conn)
```

---

### 5. Common / MQ 共享类型

- `proto/common/*.proto`：ChatEvent、Message、Session 等跨服务共享的数据模型。Go 全量生成，TS 按需生成（Web 需要反序列化 `ChatEvent` 等结构）。
- `proto/mq/v1/*.proto`：MQ 事件载体。仅生成 Go（NATS 消费方全在后端）。

---

## 📦 代码生成配置

| 配置文件 | 作用 | 覆盖范围 |
|---------|------|--------|
| `buf.gen.go.yaml` | Go message + 原生 gRPC | 全量（由 Makefile 直接 `buf generate`） |
| `buf.gen.connect.yaml` | Connect-Go handler | 仅 `gateway/v1/auth.proto` + `gateway/v1/session.proto` |
| `buf.gen.ts.yaml` | TypeScript（Connect-ES v2） | `gateway/v1/{auth,session,packet}.proto` + `common/*` |

拆分而非合并 `buf.gen.go.yaml`：Logic / Push 走纯 gRPC 更轻，不必为内部调用生成 Connect handler。

---

## 🔧 构建命令

```bash
# 生成所有代码
make gen

# 分步说明（由 Makefile 的 gen target 依次执行）：
# 1. buf generate --template buf.gen.go.yaml            # 全量 Go
# 2. buf generate --template buf.gen.connect.yaml --path proto/gateway/v1/auth.proto --path proto/gateway/v1/session.proto
# 3. buf generate --template buf.gen.ts.yaml  --path proto/gateway/v1/auth.proto --path proto/gateway/v1/session.proto --path proto/gateway/v1/packet.proto --path proto/common
```

---

## ✅ 生成结果速查

```bash
# 对外 Gateway API — 同时有 gRPC 与 Connect-Go
ls api/gen/go/gateway/v1/                      # auth.pb.go / auth_grpc.pb.go / session.pb.go / session_grpc.pb.go / push*.go / packet.pb.go
ls api/gen/go/gateway/v1/gatewayv1connect/     # auth.connect.go / session.connect.go

# Logic — 纯 gRPC
ls api/gen/go/logic/v1/                        # *.pb.go + *_grpc.pb.go

# TypeScript — Connect-ES v2，service 已并入 *_pb.ts
ls api/gen/ts/                                 # common/ gateway/
ls api/gen/ts/gateway/v1/                      # auth_pb.ts / session_pb.ts / packet_pb.ts
```

---

## 🚀 服务调用关系

```
┌─────────────────┐
│   前端客户端     │
│  (Browser/App)  │
└────────┬────────┘
         │ Connect (HTTP/1.1 + JSON)
         ▼
┌─────────────────┐
│     Gateway     │
│   (对外服务)     │
└────────┬────────┘
         │ gRPC
         ▼
┌─────────────────┐
│      Logic      │
│     (业务层)     │
└────────┬────────┘
         │ MQ Publish (NATS / ChatEvent / PushEvent)
         ▼
┌─────────────────┐
│       Task      │
│     (任务层)     │
└────────┬────────┘
         │ gRPC (PushServiceClient)
         ▼
┌─────────────────┐
│     Gateway     │
│  (Push Service) │
└────────┬────────┘
         │ WebSocket Push
         ▼
┌─────────────────┐
│   前端客户端     │
│  (Browser/App)  │
└─────────────────┘
```

---

## 📝 总结表

| 协议文件 | gRPC | Connect-Go | TypeScript | 用途 |
|---------|:----:|:----------:|:----------:|------|
| `gateway/v1/auth.proto`    | ✅ | ✅ | ✅ | 客户端鉴权 API |
| `gateway/v1/session.proto` | ✅ | ✅ | ✅ | 客户端会话 API |
| `gateway/v1/packet.proto`  | — | — | ✅ | WebSocket 载荷结构（仅 message，无 RPC） |
| `gateway/v1/push.proto`    | ✅ | — | — | Task → Gateway 推送 |
| `logic/v1/*.proto`         | ✅ | — | — | 服务间调用 |
| `common/*.proto`           | ✅ | — | ✅ | 跨服务共享数据模型 |
| `mq/v1/*.proto`            | ✅ | — | — | MQ 事件载体 |

**关键点**：

1. **仅** `gateway/v1/auth.proto` 和 `gateway/v1/session.proto` 生成 Connect-Go handler（面向客户端）。
2. **所有服务间调用**使用原生 gRPC（性能更好，依赖更稳）。
3. **前端只需要** 对外 Gateway API + Packet + Common 的 TypeScript 代码。
4. Connect-ES v2 起已不需要 `connectrpc/es` 插件，`bufbuild/es:v2.x` 一次性产出 message + service。
