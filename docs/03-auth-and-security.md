# 认证与安全边界

## 客户端身份

Web 通过 `Authorization: Bearer <jwt>` 调用 Gateway；WebSocket 握手使用既有 Token 接入。
Gateway 验证 JWT，提取 username、tenant 和 membership version。Bot 账号不可登录。
业务 body 不携带 username、access token、Role 或 Scope 来决定调用者身份。

## 服务间身份

Gateway → Logic 使用 payload-bound 工作负载签名：method、payload hash、tenant、actor、成员版本、时间戳和 nonce。
Logic 验签、防重放后重新查询当前 IM 成员状态，生成可信 UserPrincipal。
生产拒绝明文 x-username 和内部 Bearer；测试中的旧 fixture 开关不参与生产组装。

JWT、Gateway 服务签名与 Pilot 服务签名使用不同强密钥。生产传输还应使用受控网络与 TLS/mTLS；签名本身不提供加密。

## IM 资源隔离

- 租户成员状态与版本属于登录基础，不向模型提供管理工具。
- 读写会话必须同时校验当前 tenant 和会话成员关系。
- 系统仅映射普通 `user` 角色到最小聊天权限；不再提供 Agent IAM 管理员角色。
- 会话成员的 Role 只代表会话内角色，不能提升系统权限。

## Pilot

Pilot 是服务所有租户的受保护 Bot，不是任意用户代理。
它的 Logic method allowlist 只有 `ChatService.SendEvent` 与 `SessionService.GetHistoryEvents`，Actor 固定为 Bot。
Logic 强制同 tenant、AI 私聊、一真人一 Bot；最终消息还必须匹配可提交 Run 的会话、内容和幂等键。

每个私聊有独立 Pi Session；不共享历史、缓存和 staging。Pi 只收到模型环境变量，不收到业务数据库或签名凭证。
关闭内建工具和用户扩展，不提供 Shell、文件、SQL、任意 HTTP 或 IAM 工具。模型只负责回答，不能通过 Prompt 提权。

单 Pilot 容器不等于强沙箱。未来支持不可信工具时必须重新设计进程/容器和网络边界。
凭证不进入日志、前端、消息或 Session；thinking 不作为用户流式事件传输。

更多设计见 [14-ai-service.md](14-ai-service.md) 和 [15-agent-harness.md](15-agent-harness.md)。
