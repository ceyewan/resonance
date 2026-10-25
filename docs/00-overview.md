# Resonance 架构总览

Resonance 专注 IM：消息、撤回、编辑、已读、离线同步，以及作为联系人存在的 Pilot 私人助手。基础设施使用 Genesis 正式版 `v1.0.0`，不在业务层重复封装基础组件。

## 服务边界

```text
Web ─HTTP/ConnectRPC─→ Gateway ─gRPC─→ Logic ─Outbox/NATS─→ Task
 ↑                      │               ↑                  │
 └────────── WS ─────────┴──── Push ──────┴──────────────────┘
                                        │ gRPC
                                   Pilot ← NATS
                                        ↓
                                   Pi → Provider
```

| 服务 | 负责 | 不负责 |
| --- | --- | --- |
| Gateway | 协议转换、登录验证、连接和实时推送 | 业务裁决、持久化 |
| Logic | 会话权限、业务规则、主事实与 Outbox 事务 | 在线连接、写扩散 |
| Task | MQ 消费、先 Inbox 后 Push | 业务判断 |
| Pilot | 固定 Bot 私聊、运行队列、受控 Pi 子进程和回复 | IAM 运维、审批、Agent Loop、直接写 IM 主事实 |
| Web | UI、本地缓存、WS 与 Inbox 对账 | 服务端授权 |

## 统一事件

`common.v1.ChatEvent` 表达 Message、Recall、Edit、ReadReceipt、SessionUpdate 等会话变化。
用户操作经过 Logic 校验，在同一数据库事务写业务数据与 Outbox。MQ 异步送至 Task，先写用户 Inbox，再尝试在线推送。
ACK 表示业务已持久化，不代表所有接收方已收到。Push 是在线优化，Inbox 是断线后的正确性兜底。

## Pilot

一个不可登录的全局 Bot，每个用户在当前租户有且仅有一个固定私聊。注册、登录、拉列表幂等补齐，不提供第二个会话或 Profile 选择。
Pi 复用模型调用、Session、压缩与 Agent Loop；Go 只管 IM 接入和可靠性。不同私聊拥有独立上下文，不因共享 Bot 而共享历史。
AI 文本增量走独立临时事件，最终回复仍是普通 ChatEvent，经 Logic/Inbox 可靠送达。

## 身份与错误

Web 使用 JWT。Gateway 对内部 method、payload、tenant、actor、成员版本、时间戳和 nonce 签名；Logic 验签并重新查询当前 IM 成员。
Pilot 使用独立服务签名、固定 Bot Actor。业务 body 不能决定身份；生产不接受明文 x-username 或内部 Bearer。
失败使用标准 gRPC status，响应不额外携带字符串 error。

## 存储与生命周期

PostgreSQL 保存主事实、Outbox/Inbox 和 Pilot 队列；Redis 保存路由、缓存和序列号；NATS 传播事件；etcd 用于内部服务发现。
服务入口集中初始化并逆序关闭依赖，业务层只接收 Repo 接口。
Pilot 正常退出先停止摄入与任务领取，再排空、Abort 并回收 Pi，最后关闭依赖。

## 阅读入口

- [协议](01-protocol.md)、[数据库](02-database.md)、[安全](03-auth-and-security.md)
- [Gateway](10-gateway.md)、[Logic](11-logic.md)、[Task](12-task.md)、[Web](13-web.md)
- [Pilot](14-ai-service.md)、[Pi Runtime](15-agent-harness.md)、[Agent 运维](16-agent-operations.md)
- [消息流](20-message-flow.md)、[写扩散](21-write-fanout.md)、[撤回/编辑/已读](22-recall-edit-read.md)

当前任务只以根目录 `DEVELOPMENT.md` 为准，不将旧 IAM/审批或 RC2 验收计划当作后续目标。
