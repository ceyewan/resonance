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
├── 00-overview.md              ✅
├── 01-protocol.md              ✅
├── 02-database.md              ✅
├── 03-auth-and-security.md     ✅
├── 04-observability.md         ✅
├── 05-reliability.md           ✅
├── 06-deployment.md            ✅
├── 07-cicd-and-quality.md      ✅
├── 10-gateway.md               ✅
├── 11-logic.md                 ✅
├── 12-task.md                  ✅
├── 13-web.md                   ✅
├── 14-ai-service.md            ✅
├── 15-agent-harness.md         ✅
├── 16-agent-operations.md       ✅
├── 20-message-flow.md          ✅
├── 21-write-fanout.md          ✅
├── 22-recall-edit-read.md      ✅
├── 23-offline-sync.md          ✅
├── 24-session-and-membership.md 🔲 待创建
├── 25-delivery-and-push.md     🔲 待创建
├── 26-failure-recovery.md      🔲 待创建
├── 30-adr-index.md             ✅
├── 31-coding-and-module-boundaries.md 🔲 待创建
├── 32-api-style-guide.md       🔲 待创建
├── 33-db-style-guide.md        🔲 待创建
├── 40-developer-onboarding.md  ✅
├── 41-runbook.md               🔲 待创建
├── 42-release-guide.md         🔲 待创建
├── 43-testing-strategy.md      ✅
└── adr/                        ✅
    └── ADR-001-pilot-pi-runtime.md
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

描述 Pilot Go 控制面的系统边界：事件接入、身份与多租户、Run 队列、Session 映射、Tool Broker、持久审批、幂等恢复和生产门槛。

### `15-agent-harness.md`

描述 Pi Harness Runtime 的具体接入：JSONL RPC、子进程、可信 TypeScript Bridge、Session prepare-then-commit、安全启动参数、契约测试与升级回滚。

建议包含：

- Pilot、Pi Runtime 与 Tool Broker 的职责
- Actor、Service、Bot 身份和多租户前置条件
- Run Queue、Session 快照、幂等和故障恢复
- Tool Manifest、Capability、持久审批和审计
- 非编码 Prompt、流式消息与 Runtime 升级门槛

### `16-agent-operations.md`

定义 Agent Service 的发布单元、CI 门禁、Canary、排空、回滚、Session 恢复与 Egress 运维边界。

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

### `adr/ADR-001-pilot-pi-runtime.md`

记录为什么采用 Go 控制面 + Pi Runtime + Go Tool Broker，以及为什么不选择自研 Harness 或 Docker Agent。

### `verification/01-pi-runtime-adapter.md`

记录首个 Loop Engineering 切片的验收条件、可复现命令、已证明结论和剩余风险。

### `verification/02-message-idempotency.md`

记录 Logic 消息幂等切片的数据库契约、并发/迁移故障测试和生产迁移限制。

### `verification/03-pi-runtime-hardening.md`

记录 Pi Runtime 复审 P1 的修复、真实 OS helper-process 覆盖、Race 结果与残余隔离边界。

### `verification/04-agent-approval-execution-audit.md`

记录 Agent 审批、冻结参数执行和追加式审计聚合的 PostgreSQL 并发、幂等、状态转换与篡改检测证据。

### `verification/05-agent-streaming.md`

记录 Agent 临时流、最终消息对账、背压和浏览器状态回收的验证证据。

### `verification/06-agent-observability.md`

记录 Pilot 指标、Trace 上下文恢复和安全标签约束的验证证据。

### `verification/07-agent-session-gc.md`

记录多实例 Session 引用快照、集群锁和孤儿对象回收的验证证据。

### `verification/08-agent-identity.md`

记录 Gateway→Logic 可信 Principal、共享 Redis nonce 防重放和 Pilot 权威 IAM 解析的验证证据。

### `verification/09-agent-iam-mutation.md`

记录冻结参数、持久审批、幂等 IAM Mutation receipt 与崩溃/响应丢失恢复的验证证据。

### `verification/10-agent-workload-isolation.md`

记录 user-assistant/iam-admin 独立工作负载身份、方法/Tenant allowlist 与 Session Profile 绑定的验证证据。

### `verification/11-agent-budget-ledger.md`

记录租户 UTC 日/月 Token 与 micro-USD 预算、Attempt reservation/UNKNOWN hold、Provider 前置硬上限和并发账本证据。

### `verification/12-provider-egress-proxy.md`

记录严格 CONNECT、DNS rebinding/private range、TLS SNI/ALPN 和有界 tunnel 的出口代理证据。

### `verification/13-agent-runtime-isolation.md`

记录 control/runtime 镜像、私有 UDS、profile volume、凭证/网络分离和真实 Pi+Bridge 契约证据。

### `verification/14-agent-business-eval.md`

记录版本化业务 Eval 数据集、Tool/拒绝/真实副作用评分器及每个候选镜像必须执行的 Provider Gate。

### `verification/15-agent-multitenant-isolation.md`

记录多工作负载并发下 Session、Capability、Tool Result、配置日志和指标标签无跨租户串扰的可重现证据。

### `verification/16-agent-release-rollback-gate.md`

记录 Agent control/runtime 不变 digest 发布产物、生产准入、回滚顺序和失败后保持停止摄入的机器门禁，并明确区分尚未完成的候选环境实操。

### `verification/17-agent-profile-revocation.md`

记录 Agent Profile 权威准入、运行前/settled 后/最终事实前再授权、queued/active Run 取消、Session Binding 失效和已确认最终消息不可回滚的验证证据。

### `verification/18-agent-history-invalidation.md`

记录 Edit/Recall 与 Agent Run/Session Binding 的同事务失效、最终消息事实边界、迟到提交拒绝和崩溃恢复验证证据。

### `verification/19-agent-session-rollover.md`

记录 Pi JSONL 字节/entry 双阈值、持久容量字段、安全 Run 边界重建和硬上限行为的验证证据。

### `verification/20-go126-lint-gate.md`

记录 Go 1.26 对应的 golangci-lint v2 固定版本、配置迁移、全仓真实 package 加载和回归证据。

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

## 4. 当前完成状态与下一步

### 4.1 已完成文档（22 份）

核心架构骨架、横切设计层、服务设计层、发消息主链路、写扩散、撤回编辑已读、离线同步、开发者入门、CI/CD 与测试策略均已覆盖。

### 4.2 待补齐文档（对应 Phase 5–9 开发节奏）

| 文档 | 建议补齐时机 |
|------|-------------|
| `22-recall-edit-read.md` | ✅ 已有（Phase 5–6 前依据） |
| `24-session-and-membership.md` | Phase 5/6 涉及群成员变化时 |
| `25-delivery-and-push.md` | Phase 6 多端推送深入前 |
| `26-failure-recovery.md` | Phase 5 完成后（有第一个完整闭环时） |
| `30-adr-index.md` + `adr/` | ✅ 已建，首条记录 Pilot Runtime 选型 |
| `31~33` 规范文档 | 团队扩大或代码 review 问题反复出现时 |
| `41-runbook.md` / `42-release-guide.md` | 准备正式上线前 |

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

文档体系骨架已就位，当前处于功能扩展期（代码 Phase 5 开始）。

### 当前阶段：功能文档随功能推进

- Phase 5（撤回）：`22-recall-edit-read.md` 已有，开发中可直接对照
- Phase 6（已读同步）：同上，ReadReceipt 分支在 `22-recall-edit-read.md` 中
- Phase 7（Pilot + Pi Runtime）：`14-ai-service.md` + `15-agent-harness.md` 已按“Go 控制面 + Pi Runtime + Go Tool Broker”重构，先完成单租户只读闭环
- Phase 8（多租户 IAM Tool）：必须先完成 Tenant、系统 Role/Scope、服务身份和持久审批，不能只靠 Prompt/Profile
- Phase 9（流式与生产加固）：Delta 与 ChatEvent 主事实分离，并完成容量、故障注入、灰度和回滚

### 下一批补齐目标

- 主要故障场景清楚后补 `26-failure-recovery.md`
- 准备上线时补 `41-runbook.md` / `42-release-guide.md`
- Runtime、Session 一致性或 Tool 授权边界发生变化时新增 ADR，不覆盖旧记录

### 长期治理

- 规范文档（`31~33`）在团队协作出现反复摩擦时补
- 运维文档（`41~42`）在灰度或上线前补
- 每次重大架构调整同步更新对应的 `docs/` 文档

---

## 9. 归档说明

旧版文档已归档到 `docs/archive/`，用于历史参考，不再作为当前主文档体系的一部分。后续如果需要迁移旧内容，建议采用“提炼后重写”的方式，而不是直接把旧文档搬回主目录。
