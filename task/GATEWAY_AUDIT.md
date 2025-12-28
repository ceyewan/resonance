# Gateway 服务审计与优化方案 (HTTP & RPC)

## 1. 现状审计 (Audit Report)

经过对 `gateway` 的 HTTP Handler (ConnectRPC) 和 Logic 交互逻辑的审计，发现以下关键问题。

| 问题点 | 严重程度 | 描述 | 影响 |
| :--- | :---: | :--- | :--- |
| **冗余鉴权 RPC** | 🟠 高 | 认证中间件 (`RequireAuth`) 和业务 Handler (`GetSessionList` 等) 重复调用 `ValidateToken`。 | 每个请求产生 2 次 Logic RPC 调用，双倍延迟，双倍负载。 |
| **无状态穿透** | 🟡 中 | 中间件验证后未将用户信息注入 Context，导致 Handler 必须重新获取。 | 导致上述的冗余调用。 |
| **缺乏缓存** | 🟠 高 | Gateway 对 Token 验证无缓存，全部透传给 Logic。 | Logic 压力巨大（需查 DB/Redis）。Gateway 无法起到保护后端的屏障作用。 |
| **手动结构体映射** | ⚪ 低 | 手动将 `gatewayv1` 对象转换为 `logicv1` 对象。 | 代码冗余，维护成本稍高，但在 BFF 层是常见模式。 |

### 1.1 冗余鉴权代码分析
```go
// 中间件
authGroup.Use(h.authConfig.RequireAuth()) // 内部调用了一次 ValidateToken

// Handler
func (h *Handler) GetSessionList(...) {
    // 又调用了一次 ValidateToken 来获取 username
    validateResp, err := h.logicClient.ValidateToken(ctx, token)
}
```

---

## 2. 优化方案 (Optimization Strategy)

### 2.1 鉴权上下文传递 (Context Propagation)

**目标**: 一次请求，一次鉴权。

**方案**:
1.  **中间件**: 调用 `ValidateToken` 成功后，将 `UserID` / `Username` / `Claims` 注入 `gin.Context`。
2.  **Handler**: 直接从 `context` 获取用户信息，不再发起 RPC。

**伪代码**:
```go
// Middleware
func (m *AuthConfig) RequireAuth() gin.HandlerFunc {
    return func(c *gin.Context) {
        // ... validate token ...
        c.Set("username", resp.Username)
        c.Next()
    }
}

// Handler
func (h *Handler) GetSessionList(ctx context.Context, ...) {
    // 从 context 获取（需适配 Gin 到 ConnectRPC 的 context 传递）
    username := ctx.Value("username").(string)
    // 直接调用业务逻辑
    h.logicClient.GetSessionList(ctx, username)
}
```

### 2.2 Token 本地缓存 (Local Cache)

**目标**: 降低 Logic 负载，提升 Gateway 响应速度。

**方案**: 引入 LRU 缓存 (如 `hashicorp/golang-lru` 或 `Ristretto`)。

*   **Key**: `token:{access_token}`
*   **Value**: `UserIdentity` (Username, ValidUntil)
*   **TTL**: 短期 (如 1-5 分钟) 或 与 Token 有效期一致（需处理撤销问题）。

```go
// Gateway 侧
func (h *Handler) validateToken(token string) (*User, error) {
    if user, ok := h.localCache.Get(token); ok {
        return user, nil
    }
    // Cache Miss: Call Logic
    resp, err := h.logicClient.ValidateToken(...)
    // Set Cache
    h.localCache.Set(token, resp.User, ttl)
    return resp.User, nil
}
```

---

## 3. 总结

Gateway 作为流量入口，目前的实现过于“老实”，完全透传请求给 Logic。
**首要优化**: 消除 Handler 中的重复鉴权调用。
**次要优化**: 引入 Token 缓存。

结合之前的 WebSocket 优化（批量推送），Gateway 将从一个单纯的“转发器”进化为具备“聚合、缓冲、保护”能力的智能网关。
