# 📐 IM API 架构说明

## 🎯 设计原则

### 1. Gateway（对外 - 客户端访问）

**协议文件**: `proto/gateway/v1/api.proto`  
**生成代码**:

- ✅ gRPC Server/Client (`api_grpc.pb.go`)
- ✅ ConnectRPC Server/Client (`gatewayv1connect/api.connect.go`)
- ✅ TypeScript Client (`gen/ts/gateway/v1/`)

**使用场景**: 浏览器/移动端客户端通过 HTTP/1.1 + JSON 访问

**服务端代码示例**:

```go
import (
    gatewayv1 "resonance/api/gen/go/gateway/v1"
    "resonance/api/gen/go/gateway/v1/gatewayv1connect"
    "connectrpc.com/connect"
)

// 使用 ConnectRPC Handler
handler := gatewayv1connect.NewAuthServiceHandler(&authHandler{})
http.Handle(gatewayv1connect.NewAuthServiceHandler(handler))
```

**前端代码示例**:

```typescript
import { createPromiseClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AuthService } from "./gen/gateway/v1/api_connect";

const transport = createConnectTransport({
  baseUrl: "http://localhost:8080",
  // 默认使用 Connect Protocol (HTTP/1.1 + JSON)
});
const client = createPromiseClient(AuthService, transport);

// 调用方法（实际发送: POST /resonance.gateway.v1.AuthService/Login）
const response = await client.login({
  username: "user123",
  password: "pass456",
});
```

---

### 2. Gateway Push（内部 - Task → Gateway）

**协议文件**: `proto/gateway/v1/push.proto`  
**生成代码**:

- ✅ gRPC Server/Client (`push_grpc.pb.go`)
- ❌ 不生成 ConnectRPC
- ❌ 不生成 TypeScript

**使用场景**: Task 服务推送消息给 Gateway（服务间调用）

**代码示例**:

```go
import (
    gatewayv1 "resonance/api/gen/go/gateway/v1"
    "google.golang.org/grpc"
)

// Task 服务调用 Gateway
conn, _ := grpc.Dial("gateway:9090", grpc.WithInsecure())
client := gatewayv1.NewPushServiceClient(conn)
```

---

### 3. Logic（内部 - 服务间调用）

**协议文件**:

- `proto/logic/v1/auth.proto`
- `proto/logic/v1/session.proto`
- `proto/logic/v1/chat.proto`
- `proto/logic/v1/gateway_ops.proto`

**生成代码**:

- ✅ gRPC Server/Client (`*_grpc.pb.go`)
- ❌ 不生成 ConnectRPC
- ❌ 不生成 TypeScript

**使用场景**: Gateway → Logic 服务间调用（使用原生 gRPC，性能更好）

**代码示例**:

```go
import (
    logicv1 "resonance/api/gen/go/logic/v1"
    "google.golang.org/grpc"
)

// Gateway 调用 Logic
conn, _ := grpc.Dial("logic:9091", grpc.WithInsecure())
authClient := logicv1.NewAuthServiceClient(conn)
sessionClient := logicv1.NewSessionServiceClient(conn)
```

---

## 📦 代码生成配置

### buf.gen.go.yaml

为所有 proto 文件生成基础 protobuf 和 gRPC 代码

### buf.gen.connect.yaml

仅为 `gateway/v1/api.proto` 生成 ConnectRPC 代码（对外 API）

### buf.gen.ts.yaml

为 `gateway/v1/api.proto` 和 `common/*` 生成 TypeScript 代码

---

## 🔧 构建命令

```bash
# 生成所有代码
make gen

# 分步说明：
# 1. 生成 Go base + gRPC (所有 proto)
# 2. 生成 ConnectRPC (仅 gateway/v1/api.proto)
# 3. 生成 TypeScript (仅 gateway/v1/api.proto + common)
```

---

## ✅ 验证结果

```bash
# Gateway API - 应该有 ConnectRPC
ls api/gen/go/gateway/v1/gatewayv1connect/
# ✅ api.connect.go

# Gateway Push - 没有 ConnectRPC
ls api/gen/go/gateway/v1/
# ✅ push_grpc.pb.go (只有 gRPC)

# Logic - 没有 ConnectRPC
ls api/gen/go/logic/v1/
# ✅ auth_grpc.pb.go, session_grpc.pb.go, chat_grpc.pb.go
# ✅ gateway_ops_grpc.pb.go (都是纯 gRPC)

# TypeScript - 只有 Gateway API
ls api/gen/ts/
# ✅ gateway/v1/, common/
```

---

## 🚀 服务调用关系

```
┌─────────────────┐
│   前端客户端     │
│  (Browser/App)  │
└────────┬────────┘
         │ ConnectRPC (HTTP/JSON)
         │ 使用: gatewayv1connect
         ▼
┌─────────────────┐
│     Gateway     │
│   (对外服务)     │
└────┬───────┬────┘
     │       │
     │ gRPC  │ gRPC
     │       │
     ▼       ▼
┌────────┐ ┌────────┐
│ Logic  │ │  Task  │
│(业务层) │ │(任务层) │
└────────┘ └───┬────┘
                │
                │ gRPC
                │ 使用: gatewayv1.PushServiceClient
                ▼
           ┌─────────────────┐
           │     Gateway     │
           │  (Push Service) │
           └─────────────────┘
```

---

## 📝 总结

| 协议文件              | gRPC | ConnectRPC | TypeScript | 用途                |
| --------------------- | ---- | ---------- | ---------- | ------------------- |
| gateway/v1/api.proto  | ✅   | ✅         | ✅         | 客户端访问 API      |
| gateway/v1/push.proto | ✅   | ❌         | ❌         | Task → Gateway 推送 |
| logic/v1/\*.proto     | ✅   | ❌         | ❌         | 服务间调用          |
| common/\*.proto       | ✅   | ❌         | ✅         | 共享数据类型        |

**关键点**：

1. **只有** `gateway/v1/api.proto` 使用 ConnectRPC（面向客户端）
2. **所有服务间调用**都使用原生 gRPC（性能更好）
3. **前端只需要** `gateway/v1/api.proto` 的 TypeScript 代码
