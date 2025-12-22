# ConnectRPC 协议说明

## 🔍 问题：TypeScript 客户端如何调用服务器？

**答案**：ConnectRPC 使用 **HTTP/1.1 或 HTTP/2 + JSON**（默认），**不是传统的 RESTful API**。

---

## 📡 ConnectRPC 支持的三种协议

ConnectRPC 实际上支持三种协议格式，客户端和服务器可以自动协商：

### 1. **Connect Protocol**（默认，推荐用于浏览器）
- **传输**: HTTP/1.1 或 HTTP/2
- **格式**: JSON（默认）或 Binary (Protobuf)
- **路径**: `/package.service/Method`
- **特点**: 
  - ✅ 完全兼容浏览器（支持 HTTP/1.1）
  - ✅ 人类可读的 JSON 格式
  - ✅ 支持流式传输（Server Streaming）
  - ✅ 不需要 gRPC-web proxy

**示例请求**：
```http
POST /resonance.gateway.v1.AuthService/Login HTTP/1.1
Host: localhost:8080
Content-Type: application/json
Accept: application/json

{
  "username": "user123",
  "password": "pass456"
}
```

**示例响应**：
```http
HTTP/1.1 200 OK
Content-Type: application/json

{
  "token": "eyJhbGc...",
  "user": {
    "id": "123",
    "username": "user123"
  }
}
```

---

### 2. **gRPC-Web Protocol**
- **传输**: HTTP/1.1 或 HTTP/2
- **格式**: Binary (Protobuf) + Base64 编码
- **路径**: `/package.service/Method`
- **特点**: 兼容 gRPC-Web 生态

---

### 3. **gRPC Protocol**
- **传输**: HTTP/2 only
- **格式**: Binary (Protobuf)
- **特点**: 原生 gRPC，最高性能

---

## 🎯 实际使用（TypeScript 客户端）

### 代码示例

```typescript
import { createPromiseClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AuthService } from "./gen/gateway/v1/api_connect";

// 1. 创建 Transport（配置协议）
const transport = createConnectTransport({
  baseUrl: "http://localhost:8080",
  // 默认使用 Connect Protocol + JSON
  // 可选配置:
  // useHttpGet: true,  // GET 请求（对于幂等操作）
  // useBinaryFormat: true,  // 使用 Binary 而不是 JSON
});

// 2. 创建客户端
const client = createPromiseClient(AuthService, transport);

// 3. 调用方法（看起来像本地函数调用）
const response = await client.login({
  username: "user123",
  password: "pass456"
});

console.log(response.token);
```

### 实际发送的 HTTP 请求

```bash
# 使用 curl 模拟 ConnectRPC 调用
curl -X POST http://localhost:8080/resonance.gateway.v1.AuthService/Login \
  -H "Content-Type: application/json" \
  -H "Accept: application/json" \
  -d '{
    "username": "user123",
    "password": "pass456"
  }'
```

---

## 🆚 ConnectRPC vs RESTful API

| 特性 | ConnectRPC | RESTful API |
|-----|-----------|------------|
| **URL 格式** | `/package.Service/Method` | `/api/v1/users/login` |
| **HTTP 方法** | 总是 POST（除非配置 GET） | GET/POST/PUT/DELETE |
| **数据格式** | JSON/Binary (由 protobuf 定义) | 自定义 JSON |
| **类型安全** | ✅ 强类型（从 .proto 生成） | ❌ 需要手动定义 |
| **代码生成** | ✅ 自动生成客户端/服务端 | ❌ 需要手动编写 |
| **协议** | HTTP/1.1 或 HTTP/2 | HTTP/1.1 |
| **流式传输** | ✅ 支持（Server/Client/Bidirectional） | ❌ 通常不支持 |
| **向后兼容** | ✅ Protobuf 内置版本管理 | 需要手动管理 |

---

## 🏗️ Go 服务端配置

### 使用 Connect Protocol（推荐）

```go
import (
    "net/http"
    "connectrpc.com/connect"
    gatewayv1 "resonance/im-api/gen/go/gateway/v1"
    "resonance/im-api/gen/go/gateway/v1/gatewayv1connect"
)

// 1. 实现服务
type authServer struct{}

func (s *authServer) Login(
    ctx context.Context,
    req *connect.Request[gatewayv1.LoginRequest],
) (*connect.Response[gatewayv1.LoginResponse], error) {
    // 业务逻辑...
    res := connect.NewResponse(&gatewayv1.LoginResponse{
        Token: "...",
    })
    return res, nil
}

// 2. 注册 Handler（支持 Connect、gRPC-Web、gRPC 三种协议）
func main() {
    mux := http.NewServeMux()
    
    path, handler := gatewayv1connect.NewAuthServiceHandler(&authServer{})
    mux.Handle(path, handler)
    
    // 启动服务器
    http.ListenAndServe(":8080", mux)
}
```

### 协议自动协商

服务端会根据 `Content-Type` 自动识别协议：
- `application/json` → Connect Protocol (JSON)
- `application/proto` → Connect Protocol (Binary)
- `application/grpc-web+proto` → gRPC-Web
- `application/grpc+proto` → gRPC

---

## 📊 性能对比

| 协议 | 格式 | 大小 | 解析速度 | 浏览器兼容 |
|-----|------|------|---------|-----------|
| Connect (JSON) | JSON | 100% | 中等 | ✅ 完美 |
| Connect (Binary) | Protobuf | ~30% | 快 | ✅ 完美 |
| gRPC-Web | Protobuf | ~35% | 快 | ✅ 需要 polyfill |
| gRPC | Protobuf | ~30% | 最快 | ❌ 不支持 |

---

## 🎓 总结

### ConnectRPC 的实际调用方式：

1. **传输协议**: HTTP/1.1 或 HTTP/2（浏览器自动选择）
2. **数据格式**: JSON（默认）或 Binary Protobuf
3. **请求方式**: POST 到 `/package.Service/Method`
4. **不是 RESTful**: 
   - 不使用 REST 的 URL 设计（如 `/users/:id`）
   - 不使用多种 HTTP 方法（GET/PUT/DELETE）
   - 使用类似 RPC 的调用方式

### 为什么选择 ConnectRPC？

- ✅ **类型安全**: 从 protobuf 自动生成代码
- ✅ **浏览器友好**: 支持 HTTP/1.1，不需要额外的 proxy
- ✅ **向后兼容**: 支持三种协议（Connect/gRPC-Web/gRPC）
- ✅ **开发体验**: 调用远程方法就像调用本地函数
- ✅ **流式传输**: 支持服务器推送（Server Streaming）

### 与传统 gRPC 的区别：

| 特性 | ConnectRPC | 传统 gRPC |
|-----|-----------|----------|
| 浏览器支持 | ✅ 原生支持 | ❌ 需要 gRPC-Web + proxy |
| HTTP/1.1 | ✅ 支持 | ❌ 只支持 HTTP/2 |
| JSON 格式 | ✅ 支持 | ❌ 只支持 Binary |
| 服务间调用 | ✅ 可以 | ✅ 推荐 |

---

## 🔗 相关链接

- [ConnectRPC 官方文档](https://connectrpc.com/)
- [Connect Protocol 规范](https://connectrpc.com/docs/protocol/)
- [与 gRPC 的对比](https://connectrpc.com/docs/go/deployment#grpc-and-grpc-web)
