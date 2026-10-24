# Resonance 架构文档

## 系统与基础约定

- [00-overview.md](00-overview.md)：架构总览
- [01-protocol.md](01-protocol.md)：协议、身份与错误
- [02-database.md](02-database.md)：数据模型与事务
- [03-auth-and-security.md](03-auth-and-security.md)：认证与安全边界
- [04-observability.md](04-observability.md)：日志、指标与 Trace
- [05-reliability.md](05-reliability.md)：可靠性
- [06-deployment.md](06-deployment.md)：部署
- [07-cicd-and-quality.md](07-cicd-and-quality.md)：质量守门

## 服务

- [10-gateway.md](10-gateway.md)
- [11-logic.md](11-logic.md)
- [12-task.md](12-task.md)
- [13-web.md](13-web.md)
- [14-ai-service.md](14-ai-service.md)：单 Bot、每用户唯一私聊
- [15-agent-harness.md](15-agent-harness.md)：固定 Pi Runtime
- [16-agent-operations.md](16-agent-operations.md)：部署与迁移

## 业务流程

- [20-message-flow.md](20-message-flow.md)
- [21-write-fanout.md](21-write-fanout.md)
- [22-recall-edit-read.md](22-recall-edit-read.md)
- [30-adr-index.md](30-adr-index.md)

当前任务见仓库根 `DEVELOPMENT.md`。旧 IAM/审批、多 Profile 和 Genesis RC2 专用验证文档已删除，不再作为运行契约。
