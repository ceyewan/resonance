# ADR-001：Pilot 复用 Pi，限定为私人聊天助手

状态：Accepted，精简修订。

## 决策

- Pilot 是全局不可登录 Bot，每个用户在当前租户只有一个固定私聊。
- Go 承担 IM 接入与任务可靠性，Pi 承担模型调用、Agent Loop、Session 和压缩。
- Go 与 Pi 通过本地 stdio JSONL RPC 通信；单 Pilot 镜像运行，不自研 Agent Loop。
- 用户消息与最终回复复用 Logic/Outbox/Inbox，临时文本复用 Task/Gateway/WS。
- 租户隔离复用现有 IM 会话权限，不再建设独立 Agent IAM、审批或计费平台。
- 不提供业务工具，不启用 Pi 的 coding 工具，不加载用户扩展。

## 保留的复杂性

任务去重、会话串行、进程回收、Session staging、历史失效与最终消息幂等直接保护聊天正确性，保留。
多 Profile、Tool Broker、Capability、IAM Mutation、审批、预算账本、远程 Runtime/Relay 和自研出口代理不服务当前目标，删除。

## 后果

部署和代码规模明显降低。暂不支持管理员操作、不可信工具和跨主机 Session 高可用；需要这些能力时按真实需求独立设计，不能通过恢复通用工具绕过权限。

实现契约见 [Pilot](../14-ai-service.md)、[Runtime](../15-agent-harness.md)。
