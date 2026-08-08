# Resonance 开发路线图：Phase 5–9

> Phase 0–4（事件驱动骨架改造）已完成。本文档只跟踪 Phase 5 起的功能扩展阶段。
> 每个 Phase 完成后在对应的 `[ ]` 上打勾，并更新 `docs/` 对应文档。

---

## 当前进度一览

| Phase | 内容 | 状态 |
|-------|------|------|
| 0–4 | 事件驱动骨架（Proto / DB / Task / 身份） | ✅ 完成 |
| **5** | **消息撤回** | ✅ 完成 |
| **6** | **消息编辑 + 多端已读同步** | ✅ 完成 |
| 7 | Pilot + Pi Runtime（单租户只读、非流式） | ✅ 完成 |
| 8 | 多租户 IAM Tool + 持久审批 | ✅ 完成（明确的最小 Tool 集） |
| 9 | AI 流式响应 + 生产加固 | 🟨 候选发布验证中 |

---

## Phase 5：消息撤回 ✅

### 目标

验证事件驱动框架对"非消息"事件的端到端处理。撤回是最简单的"修改既有主事实"场景，走通它意味着 Logic → Task → Web 的统一事件链路已完全通用。

### 完成状态

| 层 | 状态 |
|---|---|
| Proto `ChatEvent.Recall` + `ChatRequest.recall` | ✅ |
| `repo.GetMessageByEventID` / `RecallMessageWithOutbox` | ✅ |
| `logic/service/recall.go` — 5 条业务校验 + Outbox 事务 | ✅ |
| `logic/service/chat.go` — SendEvent dispatch 分支 | ✅ |
| `gateway/logicclient` — 转发 recall payload 到 Logic | ✅ |
| Task `handler_recall.go` — 写扩散 + 推送 | ✅ |
| Frontend `sync/applier.ts` — recall 标记目标消息 | ✅ |
| Frontend 撤回 UI — 悬浮按钮 + 已撤回占位文字 | ✅ |

### 关键实现文件

- `logic/service/recall.go` — handleRecall 主逻辑
- `logic/service/recall_test.go` — 7 个单元测试
- `test/integration/recall_test.go` — 3 个集成测试（GoldenPath / PermissionDenied / OfflineSync）
- `web/src/features/chat/MessageBubble.tsx` — 撤回 UI

### 验证标准

- [x] 用户 A 撤回自己的消息 → 会话双方看到"此消息已撤回"
- [x] 用户 A 尝试撤回用户 B 的消息 → 收到 `PermissionDenied`
- [x] 用户 A 撤回 3 分钟前的消息 → 收到 `FailedPrecondition`
- [x] 撤回后重启 → 历史拉取中该消息仍显示"已撤回"（依赖 `recalled_at` 字段）
- [x] 断线重连后 PullInboxDelta → 补到 Recall 事件，状态正确恢复

---

## Phase 6：消息编辑 + 多端已读同步 ✅

### 完成状态

| 层 | 状态 |
|---|---|
| Proto `ChatRequest.edit` + `ChatEvent.Edit/ReadReceipt` | ✅ |
| `repo/message.go` — `EditMessageWithOutbox` 原子编辑 | ✅ |
| `repo/session.go` — `AdvanceLastReadSeqWithOutbox` 原子推进已读位点 | ✅ |
| `logic/service/edit.go` — 编辑业务校验 + Outbox 事务 | ✅ |
| `logic/service/chat.go` — SendEvent dispatch 接入 edit | ✅ |
| `logic/service/session.go` — UpdateReadPosition 产出 read receipt 事件 | ✅ |
| Task `handler_edit.go` / `handler_read.go` — 写扩散 + 推送 | ✅ |
| Frontend `sync/applier.ts` — edit / read receipt 状态应用 | ✅ |
| Frontend UI — 编辑入口、已编辑标记、单聊/群聊已读展示 | ✅ |
| 单元测试 + 集成测试 | ✅ |

### 关键实现文件

- `logic/service/edit.go` — handleEdit 主逻辑
- `logic/service/edit_test.go` — 编辑规则单测
- `logic/service/session.go` — UpdateReadPosition 补齐 read receipt 事件闭环
- `logic/service/session_update_read_position_test.go` — 已读回执单测
- `repo/message.go` — `EditMessageWithOutbox`
- `repo/session.go` — `AdvanceLastReadSeqWithOutbox`
- `test/integration/edit_test.go` — edit 实时 + 离线补拉集成测试
- `test/integration/read_receipt_realtime_test.go` — read receipt 实时 + 离线补拉集成测试
- `web/src/features/chat/MessageBubble.tsx` — 编辑入口、已编辑标记、消息级读状态
- `web/src/features/session-detail/SessionDetailPanel.tsx` — 会话级读状态摘要

### 验证标准

- [x] 发送者可以在 2 分钟窗口内编辑自己的文本消息
- [x] 非发送者、跨会话、已撤回消息、超时消息不能编辑
- [x] 编辑后目标消息主事实更新，且生成独立 Edit 事件进入 MQ / Inbox / WS 链路
- [x] 在线接收方实时看到编辑后的内容，离线重连后可通过 `PullInboxDelta` 恢复 Edit 事件
- [x] `UpdateReadPosition` 在读位点真正前进时产出 ReadReceipt 事件
- [x] ReadReceipt 同时支持实时推送与离线增量恢复
- [x] 单聊展示“已读/未读”，群聊展示“X 人已读”，详情侧栏展示读回执摘要

---

## Phase 7：Pilot + Pi Runtime（单租户只读、非流式）

> 本 Phase 只验证 Runtime 和 IM 闭环，不宣称具备多租户或管理员写权限。实现必须遵循 `docs/14-ai-service.md` 与 `docs/15-agent-harness.md`。

### 目标

走通“用户事件 durable 入队 → Go 调度 Pi RPC → 可信 Tool Bridge 调 Go Tool Broker → Bot 最终消息经 Logic 回写”，并具备幂等与崩溃恢复。

### Runtime Spike

- [x] 固定 Node、Pi 和 Bridge 精确版本，禁止运行时自动更新
- [x] 实现严格 LF JSONL Decoder，覆盖大帧、分片、malformed JSON 和 stdout 污染
- [x] 通过 Fake Pi 进程验证 command/event 交错、Abort、Retry、Compaction、`agent_settled`
- [x] 使用安全启动参数关闭内建工具、Skills、Prompt Template、Context Files 和扩展发现
- [x] 完整替换 coding system prompt；模型表现进入版本化业务 Eval 与真实 Provider 发布门禁

### Pilot 控制面

- [x] 新建 `pilot/` 服务和 `-module pilot` 启动入口
- [x] Ingress 只负责把 AI 会话事件写入 `t_agent_run`，提交后 ACK NATS
- [x] `source_event_id` 唯一约束，避免 MQ 重投产生重复 Run
- [x] RunCoordinator 实现会话级队列、租约、超时恢复和显式取消
- [x] `AgentRuntime` 接口与 `PiRuntime` Adapter，不让业务层依赖 Pi 类型
- [x] 每个 Active Run 一个 Pi 子进程，完成后回收 Pipe、进程和临时目录

### Session 与消息幂等

- [x] `t_agent_session_binding` 保存 Conversation → Pi Session generation
- [x] staging Session + prepare-then-commit，失败不污染最后提交快照
- [x] Logic 增加 `(session_id, sender_username, client_msg_id)` 部分唯一幂等约束、请求 Hash 与原 ACK 返回语义
- [x] 最终 Bot Message 使用 `client_msg_id=agent:<run_id>:final`
- [x] Session 丢失时从 ChatEvent 降级重建，Tool 不得重放
- [x] AI 历史发生 Edit/Recall/删除时 binding 置 dirty，下轮按有效历史重建

### Tool Bridge 与只读工具

- [x] 唯一可信 TypeScript Bridge 动态注册 Tool Manifest，并在 Prompt 前完成 readiness 证明
- [x] Go Tool Broker 实现 Schema 校验、输出上限、超时、审计和 fail-closed
- [x] Capability Token 绑定 Actor、Run、Profile、Scope 和短 TTL
- [x] 首个业务工具为 `get_my_profile`，忽略模型提供的用户名，只使用 Actor Principal
- [x] `echo` 仅限测试环境，生产 Manifest 不暴露

### Bot 与前端

- [x] 增加 `SessionKindAI` 和受保护的 Bot Service Account
- [x] 个人 AI 会话强制“一个真人 + 一个 Bot”，异常成员关系 fail closed
- [x] Bot 使用独立工作负载身份，不使用长期自签用户 JWT
- [x] “新建 AI 会话”入口和普通消息式最终回复

### 验证标准

- [x] 正常回复、Tool 回复、模型 429/5xx、Pi kill、服务重启均有确定终态
- [x] 同一用户事件不会产生两条最终回复
- [x] 普通用户伪造其他 username 时，Tool 仍只能返回本人资料
- [x] 第二个真人无法加入个人 AI 会话或复用其 Pi Session
- [x] Pi Session 可恢复，失败 Run 不会把半轮 Tool Call 带入下一轮
- [x] `bash/read/write/edit` 等内建 Tool 未注册；任何未知扩展 command 在 Prompt 前失败

---

## Phase 8：多租户 IAM Tool + 持久审批

> TenantMembership、SystemRoleBinding、Scope 与成员版本已经成为权威事实。Phase 8 只完成文档列出的窄 Tool/Mutation 集，不代表通用 IAM 运维已开放。

### 身份与租户

- [x] 为权威会话、成员和已开放 IAM 资源建立 `tenant_id` 边界
- [x] 建立系统级 Role/Scope，不能复用 `SessionMember.Role`
- [x] Gateway → Logic/Pilot → Tool Broker 使用不可伪造且逐请求权威回查的 Principal
- [x] 服务间使用 payload-bound workload signature；mTLS 保留为传输纵深防御
- [x] 普通用户与 `iam-admin` 使用不同 Profile、Worker、身份、Runtime 和 Session volume
- [x] 普通/管理员 Profile 使用不同 Conversation/Session namespace，降权时撤销旧 Capability 与 queued Run

### IAM Tool Broker

- [x] 普通用户只暴露 self-scoped Tool
- [x] 管理员查询同时校验 Actor Role/Scope、Tenant 和目标资源
- [x] 禁止通用 SQL、Shell、任意 HTTP；只提供意图明确的业务 Tool
- [x] Tool 输出执行 PII/Secret 脱敏、条数和字节限制
- [x] 跨租户、伪造 Tool 参数、Prompt Injection 安全测试进入 CI

### Durable Approval

- [x] Logic 的 `t_agent_approval` 固化脱敏摘要、参数哈希、审批策略和决定，并与 Outbox 同事务
- [x] Pilot 的 `t_agent_tool_execution` 固化安全引用、Tool/Schema 版本、幂等键和执行状态
- [x] 两个聚合只以 `call_id + args_hash` 关联，通过幂等命令和 Reconciler 收敛，不做跨库事务
- [x] Mutation Tool 首次只 prepare，返回 `approval_required + call_id`
- [x] 审批人权限由 Logic/Tool Broker 验证，不由 Pi 判断
- [x] 批准后按冻结参数直接执行，Pi 不重新生成参数
- [x] 支持 `dry_run`、`expected_version`、审批过期和单次授权消费
- [x] Tool 已成功但响应丢失时通过幂等 receipt 查询，禁止盲目重放

### 验证标准

- [x] 普通用户无法查看其他用户，管理员不能跨 Tenant
- [x] `/profile`、`/model` 和 Prompt 不能提权
- [x] 审批参数替换、过期审批、重复审批均被拒绝
- [x] 在每个关键崩溃点杀进程都不会重复 IAM 变更

---

## Phase 9：AI 流式响应 + 生产加固

### 协议与链路

- [x] 将流式相关 proto 的 `parent_event_id` 澄清/改造为 `stream_id` 或 `run_id`
- [x] Delta 走独立 ephemeral `AgentStreamEvent` 通道，不写 ChatEvent/Outbox
- [x] Task/Gateway 路由 StreamBegin/Chunk/End，Pilot 不直接成为公网或 WS 接入层
- [x] 合并相邻 Text Delta，设置频率、字节和有界队列限制
- [x] 只发送文本 Delta，不发送 thinking/reasoning

### 前端

- [x] 按 `run_id` 维护临时气泡和单调 sequence
- [x] 收到最终 ChatEvent 后按 `client_msg_id` 替换临时气泡
- [x] 缺少 Delta 或断线不触发重放，最终 Inbox Message 仍为权威事实
- [x] 超时/取消/错误可以清理临时状态

### 生产加固

- [x] 非 root、只读 rootfs、无 Docker Socket/宿主目录、Runtime 网络隔离和严格 Provider Egress
- [x] CPU/内存/PID/临时磁盘/并发/Token/Cost 限额与 per-tenant 日/月预算账本
- [x] 共享 POSIX Session Store、快照 CAS、孤儿回收和恢复测试；跨主机部署仍需替换本地 named volume
- [ ] Runtime 升级契约、Session fixture 和回滚文档已完成；候选版本 Canary/回滚实操仍待发布环境
- [x] 版本化业务 Eval 覆盖回答质量、Tool 序列、越权拒绝和真实副作用；每个候选仍须运行真实 Provider Observation
- [x] 建立 Run Queue、首 token、Pi 异常、Tool、审批、Session、成本指标

### 验证标准

- [x] AI 回复可流式展示，断线后从 Inbox 获取完整最终消息
- [x] 慢客户端不会拖垮 Pi stdout 消费或导致无界内存
- [x] 多租户并发压测无 Session、Capability、日志和 Tool Result 串扰
- [ ] 在候选环境实操 Pi 升级失败后按 control/runtime digest 组合回滚并恢复旧 Session 路径

---

## 各 Phase 完成后的文档更新要求

| Phase | 需更新的文档 |
|-------|------------|
| 5 | `docs/22-recall-edit-read.md`（将"框架已就位"改为"完整闭环"） |
| 6 | `docs/22-recall-edit-read.md` 同上；如 dispatcher 改动较大补 ADR |
| 7 | `docs/14-ai-service.md` + `docs/15-agent-harness.md`（Runtime 契约与故障矩阵） |
| 8 | 同上，并同步 `docs/03-auth-and-security.md` 与数据库文档 |
| 9 | 同上，并同步流式协议、部署、可观测和 Runbook |

---

## 关键约束提醒（改动前必看）

1. **Logic 任何业务变更 + 事件发布必须在同一事务**：先写主表，再写 Outbox，事务外异步发 MQ。参考 `mqpublish.PublishMessageToMQ`。
2. **Task 永远先写 Inbox，后推送**：存储失败 NAK 重试，推送失败只记指标，不 NAK。
3. **seq_id 必须由 Redis 原子递增**：通过 `sequencer.Next(ctx, session_id)` 获取，Recall/Edit 事件同样需要占一个 seq_id（保证 Inbox 有序）。
4. **身份来自 context，不来自 body**：`MustUsernameFromCtx(ctx)`，不接受 req 里的 username 字段。
5. **提交前必须通过**：`make format && make lint`。
