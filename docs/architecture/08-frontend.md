# 08 · 前端边界说明

`docs/architecture/` 只保留系统级架构文档，因此前端的实现细节、UI 设计、运行时拆分已经迁移到独立目录 `docs/frontend/`。

## 这里保留什么

这里只保留与系统总架构直接相关的前端边界：

- Web 通过 `ConnectRPC(HTTP)` 与 `WebSocket` 接入 Gateway
- 前端消费统一事件模型 `ChatEvent`
- HTTP 负责鉴权、会话列表、历史消息、联系人、Inbox 增量同步
- WebSocket 负责实时事件、ACK、心跳，以及后续 AI 流式扩展
- 前端本地缓存、页面组织、UI 设计不再属于系统架构主文档范围

## 前端专项文档

- [前端文档索引](../frontend/README.md)
- [Web 前端架构](../frontend/01-web-architecture.md)
- [Liquid Glass 视觉设计](../frontend/02-liquid-glass-design.md)
- [Native Liquid Glass 演进](../frontend/03-native-liquid-glass-evolution.md)
- [UI/UX 开发计划](../frontend/04-uiux-plan.md)

## 与架构主文档的关系

- 协议约束：见 [01-protocol.md](./01-protocol.md)
- 服务职责：见 [03-services.md](./03-services.md)
- 核心时序：见 [04-flows.md](./04-flows.md)
- 迁移阶段：见 [05-migration.md](./05-migration.md)

换句话说：

- 如果问题是“前端为什么要这样接后端”，看 `docs/architecture/`
- 如果问题是“前端具体怎么实现、怎么组织、怎么做 UI”，看 `docs/frontend/`
