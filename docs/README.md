# Resonance 文档体系设计

> 本文档用于确定 `docs/` 的长期组织方式。目标不是把代码目录照抄成文档目录，而是围绕 **职责边界、关键流程、核心机制、工程治理** 建立一套可持续演进的设计文档体系。

---

## 1. 文档体系目标

这套文档主要解决四件事：

1. 让新人先建立系统整体心智，再逐层深入到具体服务与机制。
2. 让架构决策、契约设计、核心流程和工程规范各有稳定落点。
3. 让后续重构、扩容、协议演进、上线排障都有可查的一手依据。
4. 避免文档退化成“代码目录说明书”或零散 issue 记录。

文档组织遵循以下原则：

- **00 号文档是唯一总入口**：先看全局，再看专题。
- **按设计主题组织，不按代码目录机械拆分**。
- **先讲边界与原则，再讲实现细节**。
- **关键流程单独成文**，不要埋在服务文档里。
- **决策要留痕**，重大架构选择进入 ADR。
- **文档要能指导实现、联调、排障和演进**，而不仅是介绍现状。

---

## 2. 推荐目录结构

```text
docs/
├── README.md
├── 00-overview.md
├── 01-protocol.md
├── 02-database.md
├── 03-auth-and-security.md
├── 04-observability.md
├── 05-reliability.md
├── 06-deployment.md
├── 07-cicd-and-quality.md
├── 10-gateway.md
├── 11-logic.md
├── 12-task.md
├── 13-web.md
├── 14-ai-service.md
├── 20-message-flow.md
├── 21-write-fanout.md
├── 22-recall-edit-read.md
├── 23-offline-sync.md
├── 24-session-and-membership.md
├── 25-delivery-and-push.md
├── 26-failure-recovery.md
├── 30-adr-index.md
├── 31-coding-and-module-boundaries.md
├── 32-api-style-guide.md
├── 33-db-style-guide.md
├── 40-developer-onboarding.md
├── 41-runbook.md
├── 42-release-guide.md
├── 43-testing-strategy.md
└── adr/
    ├── ADR-001-chat-event.md
    ├── ADR-002-outbox-pattern.md
    └── ADR-003-write-fanout.md
```

说明：

- `00-09`：总览与横切设计
- `10-19`：服务设计
- `20-29`：关键域与核心流程
- `30-39`：决策与规范
- `40-49`：使用、运维与交付
- `adr/`：架构决策记录

历史材料已归档到 `docs/archive/`，后续不再作为主文档结构继续扩展。

---

## 3. 分层设计

## 3.1 总览层

### `00-overview.md`
唯一总入口，建立系统全局心智。

建议包含：
- 系统目标与非目标
- 核心业务场景
- 系统边界与上下游
- 总体架构图
- 核心抽象（如 `ChatEvent`）
- 服务职责边界（Web / Gateway / Logic / Task）
- 核心调用链
- 当前架构阶段与演进路线

---

## 3.2 横切设计层

### `01-protocol.md`
统一描述 Proto / API / 事件契约。

建议包含：
- 对外接口与对内接口的边界
- RPC、推送、事件三类契约的职责划分
- `ChatEvent` / `PushEvent` 等核心消息模型
- 命名、分页、时间戳、可选字段规范
- 错误码与兼容性策略
- Proto 演进与 breaking change 规则

### `02-database.md`
统一描述数据模型与持久化策略。

建议包含：
- 核心表结构与关系
- Inbox / Outbox / Session / Message 等关键模型
- 索引策略与查询模式
- 事务边界
- 迁移策略
- 分库分表预案

### `03-auth-and-security.md`
统一描述鉴权和安全边界。

建议包含：
- 登录态与 JWT 约定
- Web → Gateway → Logic 身份传递
- 权限模型
- 敏感数据处理
- 输入校验与信任边界
- 常见攻击面与防护措施

### `04-observability.md`
统一描述日志、指标、追踪和告警。

建议包含：
- 日志规范
- 指标体系
- Trace 设计
- 核心告警规则
- 排障入口与定位路径

### `05-reliability.md`
统一描述可靠性设计。

建议包含：
- 幂等
- 重试
- 补偿
- 死信
- 限流与熔断
- 一致性策略
- 故障降级思路

### `06-deployment.md`
统一描述部署与环境拓扑。

建议包含：
- 本地 / 测试 / 生产环境拓扑
- 依赖组件
- 配置来源
- 网络与端口
- 发布模型
- 扩容与容量规划入口

### `07-cicd-and-quality.md`
统一描述工程门禁与质量要求。

建议包含：
- format / lint / test / security scan
- Proto 契约检查
- DB migration 检查
- 分支与 PR 规范
- 发布门禁
- 文档更新要求

---

## 3.3 服务设计层

### `10-gateway.md`
描述 Gateway 的职责与边界。

建议包含：
- 协议转换
- 鉴权
- 连接管理
- 推送中转
- 心跳、重连、会话绑定
- 为什么不承载业务规则

### `11-logic.md`
描述 Logic 的业务编排与事务边界。

建议包含：
- 核心业务职责
- 事务模型
- 领域服务划分
- 事件生成
- Outbox 协作
- Repo 接口边界

### `12-task.md`
描述 Task 的事件消费与落地执行。

建议包含：
- MQ 消费模型
- 事件分发
- 写扩散执行
- 推送执行
- 补偿任务
- 幂等与重试策略

### `13-web.md`
描述前端架构与客户端同步模型。

建议包含：
- 页面和模块划分
- 状态管理
- ConnectRPC / WS 使用方式
- 本地缓存与 Dexie 角色
- 离线策略
- 重连同步

### `14-ai-service.md`
为未来 AI 服务预留。

建议包含：
- 服务职责
- 消息过滤边界
- 模型调用边界
- Bot 身份模型
- 流式消息链路
- 安全与审计要求

如果短期不做，可以先建占位文档说明“暂未启用”。

---

## 3.4 关键域与核心流程层

### `20-message-flow.md`
发消息主链路。

### `21-write-fanout.md`
写扩散模型与一致性策略。

### `22-recall-edit-read.md`
撤回、编辑、已读等事件的统一抽象。

### `23-offline-sync.md`
离线补偿、重连同步、游标与去重。

### `24-session-and-membership.md`
会话、成员、权限、群聊 / 单聊差异。

### `25-delivery-and-push.md`
在线推送、ACK、投递保证、重试机制。

### `26-failure-recovery.md`
常见故障场景、影响面、恢复流程。

这一层建议全部按统一模板写：
- 背景
- 参与组件
- 时序图
- 正常路径
- 异常路径
- 一致性点
- 监控点
- 验收标准

---

## 3.5 决策与规范层

### `30-adr-index.md`
ADR 总索引，记录重要设计决策与对应文档。

### `adr/ADR-001-chat-event.md`
记录为什么采用 `ChatEvent` 作为统一抽象。

### `adr/ADR-002-outbox-pattern.md`
记录为什么采用 Outbox 保证事务一致性。

### `adr/ADR-003-write-fanout.md`
记录为什么选择写扩散及其权衡。

### `31-coding-and-module-boundaries.md`
描述模块边界、依赖方向和编码约束。

### `32-api-style-guide.md`
描述 Proto / API 设计规范。

### `33-db-style-guide.md`
描述表、索引、迁移、审计字段等数据库规范。

---

## 3.6 面向使用者的操作文档层

### `40-developer-onboarding.md`
新开发者本地启动与调试指南。

### `41-runbook.md`
运维操作与常见故障排查手册。

### `42-release-guide.md`
发版、回滚、配置变更、数据迁移步骤。

### `43-testing-strategy.md`
测试分层与测试重点。

---

## 4. 最小可落地版本（第一批优先补齐）

建议先补齐以下 10 份文档，先把系统边界、核心机制和可靠性说清楚：

1. `00-overview.md`
2. `01-protocol.md`
3. `02-database.md`
4. `05-reliability.md`
5. `06-deployment.md`
6. `07-cicd-and-quality.md`
7. `10-gateway.md`
8. `11-logic.md`
9. `12-task.md`
10. `21-write-fanout.md`

这 10 份优先完成后，再逐步展开 `web`、离线同步、已读撤回、运行手册等专题。

---

## 5. 每篇设计文档统一模板

建议所有 design 文档都尽量保持同一骨架：

```md
# 标题

## 1. 背景与目标
## 2. 范围与非目标
## 3. 设计原则
## 4. 方案设计
## 5. 时序 / 数据流 / 状态流
## 6. 异常与边界情况
## 7. 监控 / 测试 / 验收标准
## 8. 演进计划 / 未决问题
```

补充约定：
- 面向“为什么这样设计”，不是代码逐行解释。
- 能画时序图的链路，尽量配图。
- 涉及契约、表结构、事件结构时，尽量给最小示例。
- 如果一个结论来自关键权衡，补一条 ADR，而不是只写在正文里。

---

## 6. 写作规则

- **先总览，后细节**：新专题先补入口文档，再补细节文档。
- **先原则，后实现**：先写边界、约束、数据流，再写具体机制。
- **避免和代码目录一一映射**：文档服务理解与协作，不服务于目录对齐。
- **避免把 issue 列表当设计文档**：设计文档描述稳定方案，任务跟踪放到 issue / PR。
- **改动架构时同步更新文档**：尤其是契约、表结构、服务边界、关键流程。
- **重大变更补 ADR**：特别是协议、事件模型、一致性方案、写扩散策略。

---

## 7. 推荐阅读顺序

### 新人第一次进入项目
1. `00-overview.md`
2. `01-protocol.md`
3. `02-database.md`
4. `10-gateway.md`
5. `11-logic.md`
6. `12-task.md`
7. `21-write-fanout.md`

### 做前端 / 同步相关开发
1. `00-overview.md`
2. `01-protocol.md`
3. `13-web.md`
4. `20-message-flow.md`
5. `23-offline-sync.md`

### 做可靠性 / 运维 / 排障
1. `05-reliability.md`
2. `04-observability.md`
3. `06-deployment.md`
4. `26-failure-recovery.md`
5. `41-runbook.md`

---

## 8. 后续执行建议

建议按下面节奏推进：

### Phase 1：先定骨架
- 建立本文档定义的主文档目录
- 优先补齐前 10 份核心设计文档
- 为尚未撰写的文档创建占位文件，避免结构反复变化

### Phase 2：补关键链路
- 完成发消息、写扩散、离线同步、投递推送等核心流程文档
- 对高复杂度机制补 ADR

### Phase 3：补工程治理
- 完成 CI/CD、质量门禁、测试策略、runbook、release guide
- 形成“开发—测试—发布—排障”闭环

---

## 9. 归档说明

旧版文档已归档到 `docs/archive/`，用于历史参考，不再作为当前主文档体系的一部分。后续如果需要迁移旧内容，建议采用“提炼后重写”的方式，而不是直接把旧文档搬回主目录。
