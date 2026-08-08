# 认证与安全

> 本文档描述 Resonance 的认证流程、身份传递链路和权限模型。阅读完本文后，应该能回答三个问题：token 如何从客户端流转到业务层；Gateway 和 Logic 各自负责什么安全职责；以及系统如何防止身份伪造和越权访问。

---

## 1. 认证架构概述

系统采用 JWT 作为客户端认证凭证。Gateway 只做 JWT 的本地密码学验证并提取 `tenant_id`、`sub` 和成员版本；Logic 不接收用户 Bearer，而是验证 Gateway 的逐请求服务签名，再从权威 IAM Repo 重新加载成员、角色与 Scope。

```text
Web ──Authorization: Bearer <token>──▶ Gateway（本地验证 JWT）
                                            │
                                            │ HMAC(service_id, method, payload_hash,
                                            │ tenant, actor, member_version, ts, nonce)
                                            ▼
                                       Logic（验签、防重放、IAM 权威回查）
```

---

## 2. JWT 约定

### 2.1 签发

JWT 由 Logic 的 `AuthService.Login` 接口签发，使用 `RESONANCE_AUTH_SECRET_KEY` 环境变量中的密钥（HMAC-SHA256）。Token 有效期默认 24 小时，配置在 `configs/logic.yaml` 的 `auth.access_token_ttl`。

Token payload 包含：

- `sub`：用户名（username）
- `iss`：`resonance-service`
- `exp`：过期时间
- `extra.tenant_id`：显式租户
- `extra.membership_version`：成员授权版本
- `extra.scopes`：仅用于客户端展示的快照，不能作为 Logic 授权依据

### 2.2 验证

Gateway 的认证中间件（`gateway/middleware/auth.go`）在每个需要认证的请求上执行验证：

1. 从 `Authorization: Bearer <token>` header 提取 token
2. WebSocket 连接场景兼容查询参数 `?token=<jwt>`（浏览器 WS API 不支持自定义 header）
3. 使用与 Logic 签发端一致的 JWT 配置在 Gateway 本地验证签名、类型、签发者和过期时间
4. 只将 tenant、username 和成员版本写入进程内上下文；原 Access Token 不进入 WebSocket 长连接上下文，也不转发给 Logic

Logic 的 `AuthService.ValidateToken` 仍是公开的显式校验 API，会从 IAM Repo 回查成员状态和版本；Gateway 的内部业务调用不依赖它，也不形成可重放的 Bearer 通道。

---

## 3. 身份传递链路

```text
Web
  │  Authorization: Bearer <jwt>
  ▼
Gateway（auth middleware）
  │  本地验证 JWT → 提取 tenant / username / membership_version
  │  只把最小 Principal 写入进程内 context
  ▼
Gateway handler
  │  对 gRPC method + protobuf payload hash + Principal + ts + nonce 签名
  │  不发送用户 Bearer、roles 或 scopes
  ▼
Logic（authUnaryInterceptor）
  │  验证 Gateway 工作负载身份、时间窗和载荷绑定
  │  通过共享 Redis 原子消费 nonce；Redis 故障时拒绝请求
  │  按 tenant + username 回查 ACTIVE 成员、当前 roles/scopes/version
  │  签名版本必须等于 Repo 当前版本
  ▼
Logic service
  │  从 context 读取 username（MustUsernameFromCtx）
  └  执行业务权限校验
```

**关键约束：业务 body 永远不能决定 Actor，也不能携带 access token。** 请求体可以携带目标 `tenant_id`，但 Logic 必须强制它等于可信 `UserPrincipal.tenant_id`。Actor 只来自验签并经 IAM 回查后的上下文。

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

**需要认证的接口**：Logic 的 `authUnaryInterceptor` 对 `ChatService`、`SessionService` 和 `AgentApprovalService` 要求有效的签名服务身份。来自 Gateway 的用户调用还必须完成权威 Principal 回查；`AuthService` 的登录、注册和显式 Token 校验不要求用户 Principal。

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
| `RESONANCE_GATEWAY_SERVICE_AUTH_SECRET` | Gateway → Logic 服务签名密钥 | 与 JWT/Pilot 密钥分离，强随机，至少 32 字节 |
| `RESONANCE_PILOT_SERVICE_AUTH_SECRET` | user-assistant Pilot → Logic 签名密钥 | 不得与 Gateway/iam-admin/Capability 密钥复用 |
| `RESONANCE_PILOT_IAM_SERVICE_AUTH_SECRET` | iam-admin Pilot → Logic 签名密钥 | 独立工作负载密钥，强随机，至少 32 字节 |
| `RESONANCE_POSTGRES_PASSWORD` | 数据库密码 | 强密码 |
| `RESONANCE_ADMIN_PASSWORD` | 初始管理员密码 | 首次部署后立即修改 |

配置文件（`configs/*.yaml`）中这些字段默认为空，服务启动时从环境变量读取。代码中不应出现任何硬编码的密钥或密码。

---

## 8. AI / IAM Agent 上线前置条件

认证链路已经具备首个租户身份纵向切片，但完整 IAM 资源隔离尚未完成：

- `TenantMembership` 和 `SystemRoleBinding` 是租户成员与系统角色的权威来源；登录和 `ValidateToken` 都会按 `tenant_id` 重新加载 ACTIVE 成员、当前角色和 Scope，成员版本不匹配时 fail closed。
- 新注册用户原子进入显式 `default` 租户并获得 `user` 角色；初始化工具会将既有真人用户回填到默认租户，初始管理员另外绑定独立 `iam-admin` 角色。
- JWT 的 `extra` 携带 `tenant_id`、Scope 快照和成员版本，但校验时不信任 Token 内的角色/Scope 快照，仍以当前 Repo 为准。
- `model.User`、`model.Session` 等业务资源仍未全部引入 `tenant_id`，因此这一步只建立身份边界，不能据此宣称完整多租户 IAM 已上线。
- Gateway 只保存从 JWT 验出的 tenant、username、成员版本；每个用户 gRPC 调用都由独立 Gateway 工作负载密钥签名，并绑定 method、确定性 protobuf payload hash、时间戳和一次性 nonce。Logic 使用共享 Redis `SET NX` 在整个签名有效窗口内原子消费 nonce，因此跨 Logic 实例重放也会被拒绝；Redis 不可用时不得降级到实例内存。随后 Logic 按 tenant + username 权威回查，且签名成员版本必须与 Repo 相等。
- `user-assistant` 与 `iam-admin` Pilot 使用不同 service ID、签名密钥和 Capability 密钥。Logic 的 verifier 显式绑定 Actor、gRPC FullMethod 和 Tenant：普通 Pilot 只能访问 Bot 最终消息与历史读取；只有 iam-admin Pilot 可以创建审批和调用 IAM Mutation API。Gateway 只能读/决定审批，不能代替 Pilot 创建审批或调用 Mutation。
- Logic 还把 Pilot service ID 映射到固定的 Profile ID/version。Bot 对 Chat/History 的服务调用只能访问同 Tenant 且 Profile 完全相等的 AI Session；普通 Pilot 不能凭已知 Session ID 读取或回写管理员会话，管理员 Pilot 也不能复用普通助手会话。
- 用户 Bearer 不跨 Gateway→Logic 边界。生产 Logic 默认拒绝 `Authorization`、明文 `x-username`、`x-tenant-id`、`x-roles`、`x-scopes` 作为用户身份。`x-username` 兼容路径只能通过显式测试 Option 开启，且不能构造可信 `UserPrincipal` 或进入审批等特权 API。
- 审批用户 API 强制请求体 `tenant_id == UserPrincipal.tenant_id`，并在决定/管理员读取时通过 Identity Repo 重新查询当前系统 Scope；禁用成员、撤销角色或 IAM Repo 故障都会 fail closed。
- `SessionMember.Role` 只表示会话内角色，不能作为系统 IAM 管理员角色使用。
- Bot User 只决定聊天消息显示为谁发送，不能替代触发操作的 End-user Actor。

在开放 Pilot 的个人信息 Tool 前，必须向 Tool Broker 传递不可伪造的 Actor Principal；在开放管理员 Tool 前，还必须完成 Tenant、系统 Role/Scope、服务身份认证、持久审批和审计。详细边界见 `14-ai-service.md` 第 4–5 节。

---

## 9. 当前实现结构

| 文件 | 内容 |
| ---- | ---- |
| `gateway/middleware/auth.go` | HTTP/WS 认证中间件 |
| `gateway/logicclient/client.go` | Gateway→Logic payload-bound 用户调用签名拦截器 |
| `logic/server/interceptor_auth.go` | gRPC 认证拦截器 |
| `logic/service/auth.go` | JWT 签发与验证业务逻辑 |
| `logic/service/authorization.go` | `user` / `iam-admin` 到最小 Scope 集合的映射 |
| `repo/identity.go` | 强制 Tenant 条件的成员、系统角色和版本 CAS |
| `pkg/userauth/context.go` | 仅在 Gateway 进程内携带 tenant、username、成员版本，不保存 Access Token |
| `pkg/serviceauth/serviceauth.go` | 服务身份签名、载荷/方法/Tenant allowlist、时间窗和 nonce 防重放 |
| `logic/service/chat.go` | 会话成员权限校验 |

---

## 10. 阅读建议

| 文档 | 内容 |
| ---- | ---- |
| `01-protocol.md` | 身份传递约定的协议层描述 |
| `10-gateway.md` | Gateway 认证中间件的位置与职责 |
| `11-logic.md` | Logic 权限校验的业务上下文 |
| `14-ai-service.md` | Pilot 的 Actor、Bot、服务身份和多租户边界 |

---

## 11. 小结

Resonance 的认证设计以“客户端凭证止于 Gateway、权威授权止于 Logic”为核心。Gateway 本地验证 JWT 后用逐请求、载荷绑定的工作负载签名传递最小身份；Logic 验签、防重放并回查当前 IAM 状态后才构造 `UserPrincipal`。角色撤销、成员禁用、版本变化或 IAM 故障都会 fail closed。
