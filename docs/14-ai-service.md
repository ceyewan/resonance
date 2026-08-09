# Pilot Service 设计

> 本文档描述 Resonance AI Agent 微服务的系统边界、身份与多租户模型、运行调度、可靠性和上线门槛。Agent Runtime 的 Pi 接入细节见 `15-agent-harness.md`。
>
> **设计决策（2026-08-08）**：Pilot 使用 Go 实现控制面，通过 stdin/stdout JSONL RPC 驱动 Pi Harness Runtime；业务工具由 Go Tool Broker 承载。首版不自研 Agent Loop，不引入 Docker Agent，也不把 Pi 直接暴露为 HTTP 服务。
>
> **实现状态（2026-08-09）**：`user-assistant` 与 `iam-admin` 两个 profile 的最小纵向闭环已经落地：tenant-scoped AI Session、durable Run/租约、prepare-then-commit、最终消息幂等、流式临时气泡、可信 Bridge/Tool Broker、用户与管理员只读 Tool、持久审批 UI，以及一个受保护的租户成员状态 Mutation。Gateway/Pilot 工作负载签名、Logic 权威 IAM 回查、成员版本撤销、租户预算账本和 Provider 前置硬上限已经接通。Pi/Node/Bridge 位于 profile-specific Runtime sidecar，通过私有 UDS 与 control 通信，并只能经严格 CONNECT proxy 访问 Provider。当前实现证明的是上述明确 Tool/Mutation 集合，不等于已经开放任意 IAM 运维；多主机 Session Store、真实 Provider 业务 Eval 和正式环境数据政策验收仍是扩流门禁。

---

## 1. 真实需求与非目标

Pilot Service 服务两类场景：

1. 普通用户在现有 IM 会话中与 AI 聊天，并查询自己有权看到的资料。
2. IAM 管理员通过 AI 查询和执行受控的用户、角色、租户运维操作。

系统需要保留 Go 对认证、租户、权限、审批、审计和业务接口的控制权，同时复用成熟 Harness 已有的模型调用、Agent Loop、工具调用、上下文压缩、重试和流式事件能力。

首版明确不做：

- 不在 Go 中重新实现 Agent Loop 或 Provider SDK 聚合层。
- 不允许模型、Prompt 或 Tool 参数决定调用者身份与租户。
- 不向公网暴露 Pi RPC，也不为 Pi 单独设计面向客户端的 HTTP API。
- 不开放通用 shell、文件系统、SQL、任意 HTTP 请求等高风险工具。
- 不把模型思考内容作为会话历史或审计事实保存。
- 不把订阅账号 OAuth 凭证作为服务端生产凭证；生产使用服务端 API Key 或工作负载身份。

---

## 2. 选型结论

### 2.1 为什么选择 Go 控制面 + Pi Runtime

Pi 已提供 Session、Compaction、自动重试、Abort、Steer/Follow-up、工具调用和流式事件。Go 只需要实现业务控制面与 Runtime Adapter，无需承担最容易长期膨胀的 Agent Loop。

这种拆分保留了两个关键自由度：

- IAM、租户、审批、审计和工具仍是 Go 的领域逻辑。
- Pi 被限制在 `AgentRuntime` 接口之后，未来可以替换为 Codex App Server、Claude 或自研 Runtime。

### 2.2 为什么不选择 Docker Agent

Docker Agent 是完整 Agent 框架，不只是一个容器运行方式。它已经拥有 Session、API、Tool 和权限控制面。在 Resonance 已有 Go 微服务和 IAM 控制面的前提下再引入它，会形成两套会话、权限和运行状态。Docker 仍用于打包与隔离 Pi，但不引入 Docker Agent 产品。

### 2.3 为什么首版不直接使用模型 Go SDK

直接使用模型 SDK 可以完成简单 Tool Loop，但 Session、上下文压缩、模型切换、重试、流式事件和工具执行状态仍需要自行维护。它保留为 Pi 不满足业务场景时的退出路径，不作为首版实现。

---

## 3. 总体架构

```text
Web / Mobile
     │ HTTP / WS
     ▼
Gateway ──gRPC──▶ Logic ──Outbox──▶ NATS ──▶ Task
                      ▲                │
                      │                └──▶ Pilot Ingress
                      │                         │
                      │                         ▼
                      │                  Pilot Control (Go)
                      │                  Run/Budget/Session
                      │                         │ private UDS
                      │                         ▼
                      │              Runtime Sidecar (Go host)
                      │                    Pi JSONL RPC
                      │                         │ trusted TS bridge
                      │                         ├── loopback Relay ── UDS ──▶ Tool Broker (Go)
                      │                         └── strict CONNECT proxy ──▶ Provider
                      │                         │
                      └─────────────────────────┴──▶ IAM / User / Tenant Services
```

### 3.1 Go 控制面的模块

| 模块 | 职责 |
| ---- | ---- |
| `Ingress` | 消费 AI 会话中的用户事件，写入 durable run queue |
| `RunCoordinator` | 按会话排队、加租约、恢复超时任务、管理取消 |
| `RuntimeAdapter` | 隔离 Pi RPC，向上提供稳定的 Go 事件接口 |
| `RuntimeHost/PiSupervisor` | 在隔离 sidecar 内启停子进程、stdin/stdout、超时、信号和资源回收 |
| `SessionStore` | 管理业务会话与 Pi Session 版本的映射和快照 |
| `ToolBroker` | 工具清单、参数校验、租户授权、审批、幂等和执行 |
| `EventWriter` | 以 Bot 身份调用 Logic 写入最终消息和控制事件 |
| `AuditService` | 保存脱敏的运行、工具、审批和用量记录 |

### 3.2 Pi Runtime 负责什么

- 模型 Provider 适配与流式调用
- Agent Loop 与多轮 Tool Call
- Session JSONL 与上下文树
- Context Compaction 与溢出恢复
- Provider 瞬时错误重试
- RPC 事件流、Abort、Steer 和 Follow-up

### 3.3 边界约束

Pilot 可以写自己拥有的控制面表，但不能绕过 Logic 写 IM 主事实。最终消息、审批请求和审批决策等用户可见事实必须经过 Logic；Pi Session、运行租约和内部审计属于 Pilot 自己的状态。

---

## 4. 当前实现边界与扩流门禁

当前已经完成的安全集合是 self profile read、当前租户 user read/list，以及带 `dry_run + expected_version + durable approval + exactly-once receipt` 的成员状态变更。任何新增 Role、Scope、用户凭证或租户配置 Mutation 都必须逐个增加窄 Logic API、不可变参数绑定、审批策略、审计和业务 Eval，不能把现有 Tool 改造成通用 IAM 写入口。

Gateway 与两个 Pilot 使用独立应用层工作负载签名，解决明文 Principal 伪造、载荷篡改和短窗重放；用户 Bearer 不进入内部 gRPC。该签名不提供传输机密性。生产仍应把 mTLS/工作负载身份和密钥轮换作为纵深防御，但不得以它们替代 Logic 的 IAM 权威回查，也不得退回内部 Bearer 或明文 Principal。

扩流前仍需完成：

- 为每个正式 Tenant 显式 provision Budget Policy；缺失/禁用时按设计 fail closed。
- 在候选镜像、固定 Provider/Model 和隔离测试 Tenant 上跑完整业务 Eval，验证文本、Tool 序列、拒绝和真实副作用收据。
- 单机 Compose 可使用 profile-specific named volume；多主机/多可用区必须替换为已验证的共享 Session Store/CAS，不能宣称本地 volume 具备 HA。
- 正式数据保留、Provider 区域/训练政策、密钥轮换和 Observation 审计通过安全评审。

---

## 5. 身份、租户与授权模型

### 5.1 三种身份不能混用

| 身份 | 用途 | 例子 |
| ---- | ---- | ---- |
| End-user Actor | 决定“谁请求了什么” | 普通用户、IAM 管理员 |
| Pilot Service | 服务间认证 | Pilot 调 Logic、Tool Broker |
| Bot User | 会话中显示的发送者 | `pilot-bot` |

Bot User 只是消息呈现身份，不能替代 Actor 授权。Pilot 调用 Tool Broker 时必须携带原始 Actor Principal；回写聊天消息时才使用 Bot 身份。

Pilot 也不能通过写入 `x-username=pilot-bot` 获得任意用户模拟能力。Logic 应先认证 Pilot Service，再只允许它以配置白名单中的 Bot 发送 Agent 事件，并记录 `run_id` 和原始 Actor。该 act-as 能力不能用于普通用户或其他服务账号。

### 5.2 Principal 最小字段

```text
Principal
├── tenant_id
├── actor_id / username
├── global_roles
├── scopes
├── auth_time / auth_level
├── session_id
├── source_event_id
└── request_id
```

这些字段来自认证链路和权威 IAM，不来自消息正文、Prompt 或模型生成的参数。

### 5.3 Capability Token

每次 Run 启动时，Pilot 为 Tool Bridge 签发短期 Capability Token，至少绑定：

- `aud=tool-broker`
- `tenant_id`、`actor_id`、`run_id`
- `profile_id` 与版本
- 允许的 Tool ID 或 Scope
- 过期时间与唯一 `jti`

Token 只供可信 Extension 与 Tool Broker 使用，不进入 Prompt、工具结果或普通日志。Tool Broker 仍需重新做权限判断，Capability 不是绕过业务授权的通行证。

### 5.4 授权必须在 Tool Broker 执行

- `get_my_profile` 忽略模型提供的 username，始终查询 Principal 的 Actor。
- 管理工具必须同时校验 Tenant、系统角色、Scope 和目标资源归属。
- Capability 是 Run 启动时的快照；特权 Tool 在实际执行和审批消费时还要查询当前 IAM 状态，角色已撤销或用户已禁用时立即拒绝。
- 所有查询必须显式包含 `tenant_id`；条件缺失时 fail closed。
- 会话管理员 `SessionMember.Role` 不是 IAM 管理员，二者不能互换。
- Prompt 中写“只能访问当前租户”只是辅助指令，不是安全控制。

### 5.5 AI 会话成员边界

首版个人助手会话必须是“一个真人 Actor + 一个 Bot”。如果多个真人共享同一个 Conversation，Pi Session 会共享历史和 Tool Result；即使每次 Tool 调用都正确 self-scope，也可能在后续自然语言回答中泄露前一个成员的数据。

群组 Agent 不直接沿用个人助手设计。后续若支持，必须选择并验证一种明确语义：

- 只开放所有群成员本来就共同可见的 Tool，禁止 self/private 数据进入共享 Session；或
- 为每个 Actor 建立独立 Runtime Context，并严格控制哪些摘要可以回到共享会话。

在此之前，Logic 创建 AI 会话时应强制成员基数和类型约束，Pilot 发现不符合约束时 fail closed。

当前纵向切片已经使用独立 `CreateAgentSession(profile)` 入口落实该约束：Logic
只从可信 `UserPrincipal` 取得 tenant/roles/scopes，只加入当前真人与配置 Bot，并
把 Profile ID/Version 固定在 `t_session`。`user-assistant` 要求 `chat:use`；
`iam-admin` 同时要求 `iam-admin` 系统角色和 `iam:users:read` Scope。Web 保存的
claims 只用于显示“可能可用”的提示，按钮调用后的服务端判定才是权威结果。

---

## 6. Agent Profile

Profile 是版本化配置，不是权限事实。它定义模型可见能力，Tool Broker 再做最终授权。

| 字段 | 说明 |
| ---- | ---- |
| `profile_id/version` | 不可变版本，Run 必须记录 |
| `system_prompt` | 完整替换 Pi 的 coding prompt |
| `provider/model` | 允许的模型配置，不允许用户任意指定 |
| `allowed_tools` | 模型可见 Tool 白名单 |
| `risk_policy` | 自动执行、准备审批、禁止 |
| `limits` | 最大时长、Tool 次数、Token、输出大小 |
| `data_policy` | PII 脱敏、保留期限、允许的数据类型 |

至少拆分两个 Profile 和 Worker Pool：

1. `user-assistant`：普通聊天、个人资料、自助查询，只读且 self-scoped。
2. `iam-admin`：IAM 查询与变更，只对具备系统 Scope 的管理员开放，写操作强制审批。

每个 Profile 实例使用独立 NATS queue group、Worker ID 和 opaque Session volume。
Agent Run 的领取条件同时包含 `tenant_id + profile_id + profile_version`，避免两个
Pilot 实例共享数据库时误领对方的 Run。不同 Profile 会收到相同 durable chat
topic 的事件，但会对不属于自己的权威 Session Profile 安全忽略；同 Profile 的
版本不一致则拒绝准入并进入运维异常路径。

隔离不能只停留在 queue group。两个 Pilot 还必须使用不同 service ID、service-auth
密钥和 Capability 密钥。Logic 的签名策略显式绑定允许的 Tenant 与 gRPC 方法集合，
并把 service ID 映射到唯一 Profile ID/version。Bot 调用 Chat/History 时，Session 的
`tenant_id + profile_id + profile_version` 必须与该工作负载完全一致；因此泄漏普通
Pilot 密钥也不能调用审批/Mutation API，或读取 iam-admin Session。

用户不能通过 `/profile iam-admin` 自行切换。Profile 选择由 Go 根据权威权限决定。普通助手与 IAM 管理助手使用不同 Conversation 和 Session namespace，禁止在同一 Pi Session 中跨安全等级切换，否则降权后仍可能从旧上下文泄露管理员查询结果。

Role/Scope 撤销、Tenant 迁移、Profile 安全等级变化或数据政策收紧时，Ingress
先按当前权威 Principal 拒绝新事件；RunCoordinator 还必须在领取 Run、Runtime
settled 后和写入最终消息前重新校验 Profile 所需 Role/Scope。校验失败时取消当前及
同一 Actor/Profile 的 queued Run，并把相关 Binding 标记为 `dirty/revoked`。旧
Capability 在每次 Tool 调用时仍要重新授权；`iam-admin` 的临时文本 Delta 在最终
消息提交前不对客户端发布，因此中途降权不会把旧管理员上下文作为临时流泄露。

撤销边界以 Logic 已确认的最终消息事实为准：`final_event_id == 0` 的 Run 不得提交；
一旦 Logic 已幂等持久化最终消息并返回 ACK，后续恢复只能完成同一个 Session CAS 和
Run 状态，不能因稍后的降权删除既成消息，也不能再次发布。后续新 Run 必须创建符合
当前权限的新 Session，不能继续恢复已经标记为 dirty/revoked 的管理员上下文。可重现
证据见 `docs/verification/17-agent-profile-revocation.md`。

---

## 7. 事件接入、排队与并发

### 7.1 不在 MQ Handler 内执行长任务

Agent Run 可能持续数十秒甚至数分钟。Ingress 收到 ChatEvent 后只做：

1. 验证它是 AI 会话中由非 Bot Actor 发送的可触发 Message，排除 Bot 回复、流式事件和无关控制事件，避免自触发循环。
2. 以 `source_event_id` 写入 `t_agent_run`，唯一冲突视为重复投递。
3. 提交数据库事务后 ACK NATS。
4. Worker 从 durable run queue 领取任务。

Ingress 还应在入队前执行消息大小、用户/Tenant 速率和队列深度限制。这样既避免依赖 JetStream `AckWait` 承载整个模型调用，也让崩溃任务可以由数据库状态恢复。

### 7.2 会话内顺序

- 同一会话同一时间最多一个 Active Run。
- Worker 使用数据库租约或 `genesis/dlock` 获取 `(tenant_id, conversation_id)` 锁。
- 后续用户消息默认进入队列，在当前 Run 完成最终 Message 与 Session Binding 提交、释放会话租约后再按 `seq_id` 处理；`agent_settled` 本身还不是释放点。
- 首版不把普通新消息自动映射为 Pi `steer`，避免工具执行过程中改变语义。
- 显式“停止生成”才调用 Pi `abort`；是否丢弃后续排队消息由业务命令决定。

### 7.3 Run 状态机

```text
QUEUED → CLAIMED → STARTING_RUNTIME → RUNNING
                                   ├──▶ READY_TO_COMMIT → COMMITTING → SUCCEEDED
                                   ├──▶ FAILED_RETRYABLE → QUEUED
                                   ├──▶ FAILED_FINAL
                                   └──▶ CANCELLED
```

审批不是让一个 Run 无限期停在内存中，相关流程见第 9 节。

---

## 8. Session 与事实来源

在首版单 Actor AI 会话中，Resonance Conversation 和 Pi Session 是一对一的当前映射，但不是同一个概念。群组 Agent 不能套用这一映射。

| 数据 | 权威来源 |
| ---- | -------- |
| 用户可见消息与控制事件 | Logic / ChatEvent |
| Actor、Tenant、Role、Scope | IAM / Logic |
| Run、审批、幂等、用量 | Pilot PostgreSQL |
| 模型上下文、Tool 对话、Compaction | Pi Session JSONL |
| 临时流式 Delta | 短生命周期流通道，不是主事实 |

### 8.1 为什么不能只用聊天历史重建上下文

Pi Session 还包含 Tool Call/Result、模型元数据和 Compaction。每一轮都从普通聊天历史重新组 Prompt，会丢失 Harness 已维护的状态，也会重新承担上下文裁剪工作。因此正常路径复用 Pi Session；只有 Session 丢失或迁移失败时才从 ChatEvent 重建一个降级 Session。

### 8.2 Session 绑定

`t_agent_session_binding` 至少保存：

- `tenant_id`、`conversation_id`
- `runtime_kind=pi`
- `runtime_version`、`bridge_version`
- `runtime_session_id` 与不透明 `session_ref`
- `profile_id/version`
- `generation`、`last_committed_entry_id`
- `status`、`updated_at`

业务代码不得依赖 Pi JSONL 的内部字段做授权或业务判断。

### 8.3 Prepare-then-commit

每个 Run 基于“最后一次已提交 Session”创建 staging 副本，并把 Runtime 结果先准备成不可变候选版本：

1. 获取会话租约和 binding version。
2. 将已提交快照复制/下载到 Worker 临时目录。
3. Pi 只写 staging Session，直到 `agent_settled`。
4. 上传候选 Session，并在 `t_agent_run` 原子记录候选 `session_ref/checksum`、冻结的最终输出（或加密对象引用）和 `READY_TO_COMMIT` 状态。
5. 使用确定性 `client_msg_id` 把已记录的最终文本幂等提交给 Logic。
6. Logic 成功后，通过乐观锁把 binding generation 切换到候选 Session。
7. 标记 Run 成功；若在第 4 步之后崩溃，恢复任务继续提交，不重新调用模型。

第 4 步之前失败可以丢弃 staging，并从最后一次已提交版本重新运行；第 4 步之后不得重新推理，否则可能产生与已准备文本不同的 Session。该协议同时避免半轮 Tool Call 污染下一轮，以及“最终消息已写但 Runtime Session 丢失”造成的上下文分叉。

### 8.4 Edit、Recall 与删除

Pi Session 是追加式上下文，不会自动应用 Resonance 中对旧消息的 Edit/Recall/删除。当前实现不依赖一个可能丢失的异步通知再失效 Session，而是在 Logic 的 Edit/Recall 主事实事务中锁定目标消息，并同步处理该 AI Conversation 的 Run 与 Binding：

- 没有 durable 最终消息事实的 queued/active/prepared Run 直接变为 `CANCELLED`，清除租约并记录 `history_invalidated`；旧 Candidate 不再可提交。
- 已经持久化最终消息、但 Pilot 尚未记录 ACK 或完成 Session CAS 的 Run 保持可恢复，只设置 `session_invalidated_at`。恢复只确认同一个最终消息事实，随后以 `committed_generation=0` 成功结束，绝不提升过期 Candidate。
- 同一事务把 Session Binding 标记为 `dirty`；下一次 Run 不恢复旧快照，而是通过 Logic 的有效历史重建新 generation。
- Logic 接受 `agent:<run_id>:final` 前锁定匹配 Run 和 Binding，校验 Tenant、Profile/version、Bot 成员、冻结文本、base generation 和失效标记。普通客户端不能伪造 Agent 最终消息绕过该边界。
- 若 Run 已进入模型或 Tool，控制面仍会尽力 Abort；已完成的外部 Tool 副作用不会因消息撤回而自动回滚。
- 管理员删除/隐私删除还必须触发旧 Session 快照的保留与删除流程，不能只隐藏 ChatEvent。

事务锁序固定为 Agent Run → Session Binding；最终消息与 Session commit 使用相同顺序，避免 Edit/Recall 和提交路径形成反向锁。`client_msg_id` 的幂等查询仍可在失效后返回已经存在的原事实，但不能创建第二条最终消息。

前端应明确提示：撤回 Prompt 可以影响后续模型上下文，但不能撤销已经执行的 IAM 操作。

---

## 9. Tool Broker 与持久化审批

### 9.1 Tool 不是 Pi 进程内的业务代码

Pi 只加载一个受信任的 TypeScript Tool Bridge。Bridge 根据 Capability 从 Go Tool Broker 获取当前 Run 的 Tool Manifest，动态注册工具；执行时把调用转发给 Tool Broker。

Tool Broker 负责：

- JSON Schema 校验和未知字段拒绝
- Tenant、Actor、Role、Scope 授权
- 风险分级、参数脱敏、输出裁剪
- 超时、熔断、幂等和审计
- 对下游 IAM API 的调用

每次执行前先持久化 `(run_id, runtime_tool_call_id, tool_name, args_hash)`。同一 Runtime Tool Call 重放时返回已记录结果；Mutation 的 prepare 还应按 `(run_id, tool_name, args_hash)` 合并，避免 Provider/Agent Retry 生成新 Tool Call ID 后创建多张相同审批单。

### 9.2 工具分级

| 等级 | 示例 | 默认行为 |
| ---- | ---- | -------- |
| `ReadSelf` | 查询自己的资料 | 自动执行 |
| `ReadTenant` | 管理员查询租户用户 | 授权后执行，记录审计 |
| `Mutation` | 禁用用户、改角色 | 先 prepare，必须审批 |
| `Dangerous` | 批量删除、不可逆操作 | 首版禁止；不能只靠一次点击放行 |

### 9.3 不让 Pi 进程跨小时等待审批

Pi RPC 的 Extension UI 可以阻塞等待确认，但让子进程跨小时常驻不利于扩缩容和故障恢复。业务审批采用 durable two-phase flow：

```text
模型调用 mutation tool
  → Tool Broker 创建 PREPARED execution，冻结 immutable args/hash
  → Pilot 幂等调用 Logic.CreateToolApproval(call_id, args_hash, ...)
  → Logic 在同一事务写 Approval 与 ToolCallRequest Outbox
  → 返回 {status: "approval_required", call_id}
  → Pi 生成“等待审批”的最终回复，本 Run 正常结束

管理员审批
  → Gateway 调用 Logic.DecideToolApproval
  → Logic 重新校验审批人权限，在同一事务更新 Approval 与 ToolCallDecision Outbox
  → Pilot 消费决定，按 call_id + args_hash 找到已冻结参数
  → Tool Broker 重新校验请求人、审批人和目标资源的当前权限后执行
  → 结果持久化
  → 启动新的 Pi Run，让模型解释执行结果
```

审批后不允许模型重新提交一套参数。执行入口只接受 `call_id`，Tool Broker 校验批准的是同一参数哈希、未过期且尚未执行。

### 9.4 变更工具的附加要求

- 必须有幂等键，重复执行返回同一结果。
- 使用 `expected_version`、ETag 或等价前置条件防止审批期间状态变化。
- 支持 `dry_run` 的工具先展示影响范围。
- 审批策略显式声明是否允许 self-approval、审批人 Scope 和所需人数；高风险操作默认请求人与审批人分离。
- 审批授权单次有效，执行后立即消费。
- 审批与执行时重新校验请求人、审批人的当前 Role/Scope；队列等待期间的旧权限不能继续生效。
- 下游超时状态不明时不得盲目重试，先通过查询接口确认实际结果。

### 9.5 数据归属与事务边界

审批是用户可见的业务事实，执行是 Pilot 的 Runtime 事实，不能混在一张跨服务共享表中：

- Logic 权威持有 `t_agent_approval`：请求人、审批人、风险级别、脱敏摘要、`args_hash`、过期时间和决定。
- Pilot/Tool Broker 权威持有 `t_agent_tool_execution`：Runtime Tool Call、精确参数的加密值或安全引用、Tool/Schema 版本、幂等键、执行状态和结果。
- 两边只通过 `call_id + args_hash` 关联；Logic 不保存原始敏感参数，ChatEvent 也不承载原始参数或结果。
- `CreateToolApproval` 和 `DecideToolApproval` 都必须幂等。Logic 在本地数据库事务中同时更新 Approval 与 Outbox，不能先发事件再落状态。
- 这里不做分布式事务。若 Execution 已是 `PREPARED` 而审批单尚未创建，Pilot Reconciler 重试 `CreateToolApproval`；若决定已持久化而 Pilot 未执行，Pilot 从事件或定期对账继续处理。

这样可以明确每个服务的恢复责任，并避免“审批记录已批准，但真正执行的参数已被换掉”。

---

## 10. 用户可见事件与流式输出

### 10.1 Durable 与 Ephemeral 分离

用户可见的最终事实继续走 Logic → Outbox → NATS → Task：

- 最终 Bot Message
- ToolCallRequest 的脱敏摘要
- ToolCallDecision
- ToolCallResult 的脱敏摘要

模型 Text Delta 是传输优化，不是会话事实。它不进入 ChatEvent 主历史，不为每个 token 分配 `event_id/seq_id`，也不写 Outbox。

### 10.2 流式通道

系统使用独立的 `resonance.agent.stream.v1` 主题，由 Task/Gateway 路由在线用户：

```text
StreamBegin(run_id, stream_id, session_id, source_event_id, final_client_msg_id, bot)
StreamChunk(run_id, stream_id, uint64 sequence, delta)
StreamEnd(run_id, stream_id, uint64 sequence, reason, final_client_msg_id)
```

旧 `parent_event_id/int32 sequence` 字段仅为 wire compatibility 保留并标记 deprecated；新客户端必须使用 `run_id/stream_id/stream_sequence`。`source_event_id` 只关联触发消息，不能被解释为最终 Bot Message 的 ID。最终对账只使用 `final_client_msg_id=agent:<run_id>:final`。

### 10.3 Delta 规则

- 只转发 `text_delta`，绝不转发 thinking/reasoning。
- 按时间或字节合并 Delta，避免逐 token 冲击 NATS/WS。
- `sequence` 单调递增，客户端检测缺口但不请求重放。
- 最终 ChatEvent Message 是权威结果，客户端用 `client_msg_id=agent:<run_id>:final` 替换临时气泡。
- 断线只会丢 Delta，重连后仍能从 Inbox 获取最终消息。
- Pilot 的每 Run pending bytes、并发 stream 数和 chunk bytes 都有硬上限；超过上限只丢临时 Delta，不阻塞 Pi stdout 或最终提交。
- Task 的 Stream Consumer 不依赖 `MessageRepo`，任何 Stream Event 都不能写入 Inbox；Gateway 慢队列满时直接丢临时流。

---

## 11. 幂等与故障恢复

| 故障点 | 处理方式 |
| ------ | -------- |
| NATS 重复投递用户事件 | `t_agent_run.source_event_id` 唯一约束 |
| Worker 在启动 Pi 前崩溃 | 租约过期后重新领取 |
| Pi 运行中崩溃 | 丢弃 staging Session，从最后提交快照重试 |
| Tool 只读调用重复 | Tool Call 级幂等缓存或安全重试 |
| Tool 已变更但结果丢失 | 使用持久 `call_id/idempotency_key` 查询下游结果，禁止盲重放 |
| Execution 已 Prepare、Approval 未创建 | Reconciler 幂等重试 `CreateToolApproval(call_id, args_hash)` |
| Approval 已决定、Pilot 未收到事件 | Pilot 按 `call_id + args_hash` 对账并继续，执行端仍做权限和幂等校验 |
| 候选 Session 已准备、最终 Message 未写 | 从 Run 中读取已冻结文本继续提交，不重新调用模型 |
| 最终 Message 已写、Run 未标成功 | Logic 按 `(session, bot, client_msg_id)` 去重，然后继续 binding CAS |
| 候选快照已上传、binding 未更新 | 内容寻址/版本号使重试 CAS 安全；未引用候选由后台回收 |
| Pilot 发出部分 Delta 后崩溃 | 发 StreamEnd(error) 或由超时监护清理，最终消息不存在则允许重试 |

最大自动重试次数、退避、总墙钟时间必须配置化。权限拒绝、参数错误和审批拒绝不是可重试错误。

---

## 12. 数据模型建议

### 12.1 `t_agent_run`

保存队列和运行事实：`run_id`、Tenant、Conversation、Actor、Source Event、Profile/Runtime/Model 版本、状态、attempt、租约、错误分类、Token/Cost、候选 Session 引用、冻结的最终输出、起止时间。

关键约束：

- `UNIQUE(tenant_id, source_event_id)`
- 同一 Conversation 只能有一个非终态 Active Run
- 状态更新使用 compare-and-swap，禁止无条件覆盖
- 最终消息复用 Logic 已实现的 `(session_id, authenticated_sender, client_msg_id) WHERE client_msg_id <> ''` 部分唯一约束；同键同请求返回第一次 ACK，同键不同请求 fail closed

### 12.2 `t_agent_session_binding`

保存第 8.2 节中的不透明 Runtime Session 映射和提交 generation。

### 12.3 Logic：`t_agent_approval`

保存 `call_id`、Tenant、请求人、Tool 名称、风险、脱敏摘要、`args_hash`、审批策略、状态、审批人、决定和过期时间。它属于 Logic；状态变更与 `ToolCallRequest/ToolCallDecision` Outbox 在同一数据库事务中提交。

### 12.4 Pilot：`t_agent_tool_execution`

保存 `call_id`、Run、Runtime Tool Call ID、Tool/Schema 版本、精确参数的加密值或安全引用、`args_hash`、幂等键、执行状态、下游操作 ID 和结果摘要。它属于 Pilot/Tool Broker；批准后只能执行这份冻结参数。

### 12.5 `t_agent_audit_log`

保存结构化安全审计。默认不保存完整 Prompt、Completion、thinking、访问令牌或敏感 Tool Result。需要内容留存时必须有单独的数据分类、加密、访问控制和保留期限。

---

## 13. 安全基线

### 13.1 Prompt Injection 假设

所有用户输入、历史消息、RAG 文档和工具结果均视为不可信内容。无法靠 System Prompt 消除 Prompt Injection，因此权限边界必须在模型之外。

### 13.2 Tool 设计

- 只提供意图明确的业务工具，不提供 `sql(query)`、`http(url)`、`shell(command)`。
- 输出采用结构化字段，区分 `model_text` 与仅供 UI/审计的数据。
- 对返回条数、单字段长度和总字节数设上限。
- 密码哈希、Token、Secret、恢复码等字段永不进入模型上下文。
- Tool Result 中的自然语言按“不可信数据”标注，不能改变授权策略。

### 13.3 容器边界

- 非 root、只读 root filesystem、删除 Linux capabilities。
- 不挂载 Docker Socket、宿主机目录或宿主 `~/.pi/agent`。
- Runtime 只挂 profile-specific 私有 Session/socket volume，不挂 control 配置或业务凭证。
- Runtime 不加入业务网络；Tool 通过私有 UDS/loopback Relay，Provider 通过严格 CONNECT proxy。
- 设置 CPU、内存、PID、临时磁盘和最大运行时间。
- Pi 内建 `read/bash/edit/write/grep/find/ls` 全部禁用。

### 13.4 数据保护

- 日志和 Trace 默认不记录 Prompt、Completion、Tool 参数全文。
- PII 字段在进入模型前按 Profile 脱敏。
- Session 快照加密、按 Tenant 分区、设置生命周期和删除流程。
- 审计读取本身需要独立管理员 Scope，并记录二次审计。

### 13.5 面向普通用户的滥用控制

- 入队前执行输入大小、用户/Tenant 频率、并发和每日预算限制。
- 根据产品政策决定是否接入输入/输出内容安全检查，并为拒绝、申诉和误判保留可审计原因码。
- 模型或 Tool 错误对用户返回稳定错误码和安全文案，不暴露 Provider 响应、堆栈、内部地址或策略细节。
- 对重复越权探测、自动化撞库式查询和异常 Tool 序列建立告警与临时封禁策略。

---

## 14. 部署与扩缩容

### 14.1 首版拓扑

首版部署拆成同一 Profile 的两个兼容发布物：Go Pilot control 镜像不包含 Node/Pi；Runtime sidecar 镜像固定 Node、Pi 和唯一可信 Bridge。sidecar 内的 Go Host 仍以 stdio JSONL 驱动每个 Pi 子进程，control 只通过共享私有 UDS 调用稳定的 `AgentRuntime` 协议。

这个 UDS wrapper 不是面向客户端或跨节点的远程 Pi RPC：它没有 TCP listener、不注册服务发现、没有公网入口，也不把 Pi wire envelope暴露给业务层。拆分的目的只是让 Pi 无法同时持有 Provider Key 与 PostgreSQL/NATS/Etcd/Logic 网络权限，并保留 Go control 的可替换 Runtime 边界。

### 14.2 进程模型

首版采用“一次 Active Run 一个 Pi 子进程”：

- 启动时装载该 Conversation 的 staging Session。
- `agent_settled` 后正常退出。
- Capability 与临时目录只属于当前 Run。
- 崩溃隔离和跨租户清理最简单。

只有性能数据证明启动成本不可接受后，才考虑 Warm Pool。Warm Pool 也不能让同一进程并发处理多个 Session，且必须证明 Capability、环境变量、Extension 状态和 Session 不会串租户。

### 14.3 Session 存储

- 本地单机 POC：受限 Docker Volume。
- 多实例：对象存储或支持原子版本发布的持久存储，Worker 本地只保留 staging。
- PostgreSQL 保存 metadata 和 generation，不把巨大 JSONL 直接塞进热表。
- 快照上传、binding CAS、孤儿回收必须有指标和后台任务。

---

## 15. 可观测性与运行门槛

每个 Run 必须关联 `trace_id/run_id/source_event_id/conversation_id`，并记录以下指标：

- 队列等待、首 token、总运行时延
- Active/Queued Run 数、租约恢复次数
- Pi 启动失败、异常退出、RPC 解析错误
- Provider/Model、Input/Output/Cache Token、估算成本
- Tool 调用次数、授权拒绝、审批等待与执行结果
- Session 快照大小、提交延迟、恢复/重建次数
- 流式 Delta 合并率和丢弃率

建议在真实流量前通过压测确定 SLO，不在设计阶段伪造固定数字。至少要有：并发上限、排队超时、首 token 超时、Run 总超时、Tool 超时、最大 Session 大小和单租户预算。

---

## 16. 分阶段落地

### Stage 0：Runtime Spike

- Go 拉起固定版本 Pi RPC，验证严格 JSONL、Abort、超时和 `agent_settled`。
- 用假 Provider/假 Tool 完成可重复的契约测试。
- 验证完全替换 coding prompt，并关闭全部内建工具和资源发现。

### Stage 1：单租户只读闭环

- `user-assistant` Profile。
- `get_my_profile` 与一个无副作用测试工具。
- 非流式最终消息、Run 幂等、Session prepare-then-commit。
- 不开放管理员写操作。

### Stage 2：多租户与管理员只读

- 完成 Tenant、系统 Role/Scope 和服务身份改造。
- Tool Broker 全链路 Tenant 测试。
- `iam-admin` 只读工具、审计和配额。

### Stage 3：审批与写操作

- Durable two-phase approval。
- 变更工具幂等、dry-run、expected_version。
- 崩溃点注入和重放测试通过。

### Stage 4：流式与规模化

- 独立 Agent Stream 通道和客户端临时气泡。
- 多实例 Session Store、容量测试、灰度升级和回滚。

---

## 17. 验收清单

上线前至少验证：

- 普通用户无法查询或修改其他用户，即使在 Prompt/Tool 参数里伪造 ID。
- 第二个真人不能加入个人 AI 会话；构造异常成员关系时 Pilot 拒绝运行。
- 管理员不能跨 Tenant，Profile 切换不能提权。
- 管理员降权后旧 Capability、未提交/queued Run 和旧 Pi Session 均不能继续使用；已被 Logic 确认的最终消息只能完成其原有 Session CAS，不能被回滚或重复发布。
- Tool Result 中含有恶意指令时不能扩大 Tool 权限。
- Pi 崩溃、进程被杀、Provider 429/5xx、Tool 超时均不会重复写操作或重复回复。
- 审批前后资源发生变化时，`expected_version` 能阻止过期变更。
- Session 丢失可以从 ChatEvent 降级重建，并明确标记上下文质量下降。
- 历史 Edit/Recall 后旧文本不会继续出现在新 Runtime Session；已执行副作用不会被误认为已撤销。
- Session JSONL 达到软字节或 entry 阈值后，下一轮从权威有效历史创建新 generation；达到硬上限前不会继续无限追加。
- 运行时升级可以通过旧/新版本回放同一契约测试并快速回滚。
- 删除用户/租户时，聊天历史、Pilot 表、Session 快照和审计按保留策略协同处理。

---

## 18. 官方资料与变更风险

本设计核验于 2026-08-08，实施时应重新检查：

- [Pi RPC Mode](https://pi.dev/docs/latest/rpc)
- [Pi Sessions](https://pi.dev/docs/latest/sessions)
- [Pi Session Format](https://pi.dev/docs/latest/session-format)
- [Pi Compaction](https://pi.dev/docs/latest/compaction)
- [Pi Extensions](https://pi.dev/docs/latest/extensions)
- [Pi Security](https://pi.dev/docs/latest/security)
- [Pi Containerization](https://pi.dev/docs/latest/containerization)
- [Pi Providers](https://pi.dev/docs/latest/providers)

Pi 仍在快速迭代，历史版本出现过 RPC framing 的 breaking change。生产镜像必须固定精确版本，升级必须经过协议契约、Session 迁移、工具和故障注入测试；禁止运行时自动更新。

---

## 19. 小结

Pilot 不是另一个 Agent 平台，而是 Resonance 的 Go 业务控制面。Pi 负责“如何运行 Agent”，Go 负责“谁能让它做什么、对哪个 Tenant 做、何时需要审批、失败后如何恢复”。只有把身份、租户、Tool、Session 和幂等边界都放在模型之外，这套方案才适合从普通聊天演进到 IAM 管理操作。
