# 认证与安全

> 本文档描述 Resonance 的认证流程、身份传递链路和权限模型。阅读完本文后，应该能回答三个问题：token 如何从客户端流转到业务层；Gateway 和 Logic 各自负责什么安全职责；以及系统如何防止身份伪造和越权访问。

---

## 1. 认证架构概述

系统采用 JWT 作为认证凭证，认证职责在 Gateway 层收敛。Gateway 负责验证 token 的有效性并提取用户身份，Logic 负责验证已认证用户是否有权限执行当前业务操作。这两层职责严格分离，不互相越界。

```text
Web ──Authorization: Bearer <token>──▶ Gateway（验证 token，提取 username）
                                            │
                                            ▼
                                       gRPC metadata: x-username
                                            │
                                            ▼
                                       Logic（验证业务权限）
```

---

## 2. JWT 约定

### 2.1 签发

JWT 由 Logic 的 `AuthService.Login` 接口签发，使用 `RESONANCE_AUTH_SECRET_KEY` 环境变量中的密钥（HMAC-SHA256）。Token 有效期默认 24 小时，配置在 `configs/logic.yaml` 的 `auth.access_token_ttl`。

Token payload 包含：

- `sub`：用户名（username）
- `iss`：`resonance-service`
- `exp`：过期时间

### 2.2 验证

Gateway 的认证中间件（`gateway/middleware/auth.go`）在每个需要认证的请求上执行验证：

1. 从 `Authorization: Bearer <token>` header 提取 token
2. WebSocket 连接场景兼容查询参数 `?token=<jwt>`（浏览器 WS API 不支持自定义 header）
3. 调用 Logic 的 `AuthService.ValidateToken` 验证 token 有效性并获取 username
4. 验证通过后，将 username 写入请求上下文

---

## 3. 身份传递链路

```text
Web
  │  Authorization: Bearer <jwt>
  ▼
Gateway（auth middleware）
  │  验证 token → 提取 username
  │  写入 Gin context / http.Request context
  ▼
Gateway handler
  │  从 context 读取 username
  │  gRPC metadata: x-username = <username>
  ▼
Logic（authUnaryInterceptor）
  │  从 gRPC metadata 读取 x-username
  │  写入 Go context（service.WithUsername）
  ▼
Logic service
  │  从 context 读取 username（MustUsernameFromCtx）
  └  执行业务权限校验
```

**关键约束：业务 body 里永远不携带 username 或 access_token。** 身份只通过 header/metadata 传递，不进入请求体。这保证了身份来源唯一，避免客户端通过构造请求体伪造身份。

---

## 4. 权限模型

### 4.1 Gateway 层权限

Gateway 只做一件事：验证 token 是否有效。它不判断用户是否有权限访问某个会话，也不判断用户是否可以发送某种类型的消息。这些判断属于业务规则，由 Logic 负责。

不需要认证的接口（登录、注册）通过路由配置跳过认证中间件。

### 4.2 Logic 层权限

Logic 在每个业务操作前执行权限校验，当前主要有两类：

**会话成员校验**：`ChatService.SendEvent` 在处理消息前，查询会话成员列表，验证发送者是否在会话中。非成员发送消息返回 `codes.PermissionDenied`。

```go
isMember := false
for _, m := range members {
    if m.Username == username {
        isMember = true
    }
}
if !isMember {
    return nil, status.Errorf(codes.PermissionDenied, "not a session member")
}
```

**需要认证的接口**：Logic 的 `authUnaryInterceptor` 对 `ChatService` 和 `SessionService` 下的所有方法要求 `x-username` 不为空，`AuthService` 的接口（登录、注册、验证 token）不要求。

### 4.3 当前权限边界

| 操作 | 权限要求 |
| ---- | -------- |
| 登录/注册 | 无 |
| 发送消息 | 已认证 + 会话成员 |
| 拉取历史消息 | 已认证 + 会话成员 |
| 更新已读位点 | 已认证 + 会话成员 |
| 拉取会话列表 | 已认证 |
| 拉取 Inbox 增量 | 已认证（只能拉自己的） |

---

## 5. WebSocket 鉴权

WebSocket 连接建立时，Gateway 的 WS handler 从查询参数中提取 token 并验证：

```text
ws://gateway:8080/ws?token=<jwt>
```

验证通过后，username 绑定到这条 WebSocket 连接上，后续所有上行消息都使用这个已认证身份，不需要在每条消息中重复携带 token。

连接断开后，Gateway 从 Redis 中删除该用户的在线路由记录，并通知 Logic 的 Presence 服务更新在线状态。

---

## 6. 错误处理约定

认证和权限失败通过 gRPC status 返回，不在响应体中携带自由文本 error 字段：

| 场景 | gRPC code | HTTP 映射 |
| ---- | --------- | --------- |
| token 缺失或无效 | `Unauthenticated` | 401 |
| 非会话成员 | `PermissionDenied` | 403 |
| 资源不存在 | `NotFound` | 404 |
| 业务规则违反 | `InvalidArgument` | 400 |
| 服务内部错误 | `Internal` | 500 |

---

## 7. 敏感配置

以下配置项不应硬编码，必须通过环境变量注入：

| 环境变量 | 用途 | 生产要求 |
| -------- | ---- | -------- |
| `RESONANCE_AUTH_SECRET_KEY` | JWT 签发密钥 | 强随机，至少 32 字节 |
| `RESONANCE_POSTGRES_PASSWORD` | 数据库密码 | 强密码 |
| `RESONANCE_ADMIN_PASSWORD` | 初始管理员密码 | 首次部署后立即修改 |

配置文件（`configs/*.yaml`）中这些字段默认为空，服务启动时从环境变量读取。代码中不应出现任何硬编码的密钥或密码。

---

## 8. 当前实现结构

| 文件 | 内容 |
| ---- | ---- |
| `gateway/middleware/auth.go` | HTTP/WS 认证中间件 |
| `logic/server/interceptor_auth.go` | gRPC 认证拦截器 |
| `logic/service/auth.go` | JWT 签发与验证业务逻辑 |
| `logic/service/chat.go` | 会话成员权限校验 |

---

## 9. 阅读建议

| 文档 | 内容 |
| ---- | ---- |
| `01-protocol.md` | 身份传递约定的协议层描述 |
| `10-gateway.md` | Gateway 认证中间件的位置与职责 |
| `11-logic.md` | Logic 权限校验的业务上下文 |

---

## 10. 小结

Resonance 的认证设计以"身份在 Gateway 收敛，权限在 Logic 判断"为核心。JWT token 通过 header 或查询参数传入，Gateway 验证有效性后提取 username，通过 gRPC metadata 传给 Logic，业务代码从 context 中读取，不接触原始 token。这条链路保证了身份来源唯一，也让 Gateway 和 Logic 各自只关注自己该关注的安全职责。
