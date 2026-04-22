# Pilot Service 设计

> 本文档描述 Resonance 中 Pilot Service 的职责、边界和核心处理路径。阅读完本文后，应该能回答三个问题：为什么 Pilot Service 不是一条新的接入层，而是一个特殊的 Logic 客户端；Agent Loop 在现有 ChatEvent 骨架上如何扩展；以及哪些输出应该进入会话历史，哪些只属于内部执行轨迹。
>
> **说明**：本文档描述的是设计目标与契约骨架。截至当前，代码仓库中尚无 `pilot/` 目录，所有实现落点都是设计预留，实际落地节奏见 `DEVELOPMENT.md` Phase 7 条目。

---

## 1. Pilot Service 的定位

Pilot Service 是 Resonance 中的 AI 能力承载层。它面对的不是"系统里要不要加一个模型调用接口"，而是一个更具体的问题：当一个会话的一方不是真人、而是具备自主规划与工具调用能力的 Agent 时，系统应该如何让它仍然守住现有的一致性边界，同时把模型的流式响应、工具调用和人类审批自然地嵌进会话历史。

从整个系统来看，Pilot Service 不是一条新的接入通道，也不是额外的业务裁决层。它更接近一个**特殊类型的会话参与者**：它订阅自己所属会话的事件流，以 Bot 身份回调 Logic 产出新的会话事件。换句话说，对 Logic 来说，Pilot Service 只是另一个 gRPC 客户端；对 Task 和 Gateway 来说，Pilot 产出的消息与真人产出的消息没有本质区别。这是本服务最重要的设计基调。

---

## 2. 设计目标

Pilot Service 的设计主要服务于四件事。第一件事是把模型调用与会话链路解耦，让 AI 能力可以独立演进，而不必侵入 Logic 或 Task 的主流程。第二件事是复用现有的一致性骨架，让 Pilot 产出的消息、撤回、编辑继续走 Outbox 与 ChatEvent，而不是另建一条链路。第三件事是把 Agent Loop 的中间状态——流式增量、工具调用、人类审批——以统一事件的形式呈现在会话里，让历史、审计、回放天然成立。第四件事是为 AIOps 等业务场景提供足够强的权限与防滥用约束，让"AI 可以调工具"这件事不会变成系统的安全黑洞。

这四个目标共同决定了 Pilot Service 的边界：它是业务能力层，而不是接入层；它输出的是会话事件，而不是独立的推送流。

---

## 3. 运行时位置

Pilot Service 站在 Logic 的下游与上游**之间**——它消费 Logic 产出的事件，也以客户端身份再次调用 Logic。

### 3.1 架构图

```text
Web ── HTTP/WS ──▶ Gateway ── gRPC ──▶ Logic ── Outbox ──▶ MQ(NATS)
                                         ▲                    │
                                         │                    ├──▶ Task (写扩散 + 推送)
                             gRPC(Bot 身份) │                    │
                                         │                    └──▶ Pilot Service
                                         │                            │
                                         │                            ├── LLM Provider
                                         └────────────────────────────┤
                                                                      └── Tools / MCP
```

### 3.2 Pilot Service 在链路中的作用

- 订阅 MQ 中的 `chat.events`，按会话类型过滤出 AI 会话的用户消息
- 驱动 Agent Loop：调用 LLM、执行工具、处理审批、生成最终回答
- 每一步产出都通过 `Logic.SendEvent` 以 Bot 身份写回系统
- 把 Agent 的内部执行轨迹写入独立的 trace 通道，不混入 ChatEvent

这里的关键在于：Pilot Service **不直接写数据库、不直接发 MQ、不直接推 Gateway**。所有会话事件都必须经过 Logic，享受既有的事务与投递保证。

---

## 4. 职责边界

### 4.1 负责什么

| 职责 | 说明 |
| ---- | ---- |
| 订阅过滤 | 订阅 `chat.events`，只处理 AI 会话、且发送者不是自己的事件 |
| Agent Loop | 维护规划 / 生成 / 工具调用 / 审批等待的状态机 |
| LLM 调用 | 封装 Provider 接口，支持流式与工具调用 |
| 工具执行 | 通过 MCP 等协议调用外部工具，并按策略决定是否需要审批 |
| 事件回写 | 以 Bot 身份调 `Logic.SendEvent`，输出 StreamDelta / ToolCall / Message |
| 会话级策略 | 维护 AgentProfile（工具白名单、数据作用域、审批策略、配额） |
| 内部轨迹 | 写 Agent 执行 trace 到独立通道，供审计与调试 |

### 4.2 不负责什么

| 不负责的事情 | 原因 |
| ------------ | ---- |
| 用户鉴权与协议适配 | 这是 Gateway 的职责 |
| 主事实写入与事务 | 这是 Logic 的职责 |
| 写扩散与在线推送 | 这是 Task 的职责 |
| 自主直连数据库 | 主事实一律由 Logic 产出 |
| 管理真人会话 | 只处理 AI 参与的会话 |

Pilot Service 最需要克制的，就是不要越层去"直接"给用户推送内容。只要一条事件没有经过 Logic，它就没有 event_id、没有 seq_id、没有 Inbox 记录，也就没有一致性可言。

---

## 5. Bot 身份与会话模型

### 5.1 Bot 用户

在 `t_user` 表中引入一个 `is_bot bool` 标记字段。系统在 `init` 模块种子化时插入一个或多个 Bot 用户（例如 `pilot-bot`），这些用户不走注册流程、没有密码，仅由 Pilot Service 启动时用 `genesis/auth` 自签长期 JWT 使用。Bot 用户在身份层面与真人用户完全一致：有 username、可加入会话、可在 Logic 中发消息、在 Task 中正常写扩散。

### 5.2 AI 会话

用户与 AI 的会话仍然复用现有 `t_conversation` 结构，通过一个 `kind` 字段（例如 `CONVERSATION_KIND_AI`）标记。创建 AI 会话时，Logic 自动把指定的 Bot 用户加为成员。Pilot Service 在订阅侧只处理 `kind=CONVERSATION_KIND_AI` 的会话事件，避免消费真人之间的对话。

### 5.3 为什么复用而不是新建

如果为 AI 会话新建一套独立的表结构与事件通道，系统就会出现两套历史、两套撤回语义、两套写扩散路径，Task / Gateway / Web 都要双重实现。复用现有模型的代价只是新增一个会话 `kind` 和一个 `is_bot` 标记，收益却是整条一致性与可恢复链路自动可用。

---

## 6. Agent Loop 处理语义

Pilot Service 内部的执行模型，是一个以"事件驱动"为主干的循环：**收到用户消息 → 规划 → 生成 → 工具调用 → 等待审批 → 继续生成 → 产出最终回答**。

### 6.1 处理流程图

```text
MQ: chat.events (AI 会话 + 真人消息)
  ├── 载入历史与 AgentProfile
  ├── 调用 LLM (stream = true)
  │   └── 每段增量 ──▶ Logic.SendEvent(StreamDelta)
  ├── 若产生 tool_calls:
  │   ├── 若需要审批 ──▶ Logic.SendEvent(ToolCallRequest)
  │   │                  └── 等待 chat.events 中的 ToolCallDecision
  │   ├── 执行工具（本地 / MCP）
  │   └── Logic.SendEvent(ToolCallResult) 并回填到 LLM 上下文
  ├── 回到循环顶部，直到 LLM 不再产生 tool_call
  └── Logic.SendEvent(Message) 作为定版最终回答
```

### 6.2 哪些输出进入会话历史

**用户可见层**（进入 ChatEvent，落 Inbox，写入历史）：

- 最终回答文本
- 流式增量（可选落库，默认只经 MQ 推送一次）
- 工具调用请求与审批决策
- 工具调用结果摘要（默认折叠，详情按需展开）

**内部执行层**（不进入 ChatEvent，仅落 trace）：

- 模型的思考 token / reasoning 内容
- 规划草稿与中间迭代
- 失败重试与降级路径
- 子 Agent 间的内部对话

这条边界的价值在于：**会话历史承担"用户能理解、能决策、能追溯"的那部分，内部轨迹承担"调试、审计、回放"的那部分**，两者互不污染。

### 6.3 流式响应的落地策略

流式响应通过新增的 `StreamDelta` payload 传递，默认的落地约定是：

- 首个 Delta 创建逻辑消息 ID（由 Logic 分配 event_id 并返回）
- 后续 Delta 引用该 event_id，仅携带增量内容
- 结束时调用 `Logic.SendEvent(Message)` 作为定版，覆盖前面累积的内容
- Delta 在 Logic 侧**不落主表**，直接经 MQ 推给在线客户端

这样设计的代价是断线重连时丢掉中间 Delta，但定版消息可从 Inbox 恢复。换来的收益是 MQ 流量可控、主表不被高频小写操作污染。

---

## 7. Agent 生命周期与状态恢复

Pilot Service 在生命周期设计上坚持一个明确的原则：**进程无状态，Agent Loop 的生命周期等于单次响应**。Agent 不是一个常驻的对话对象，而是一段由消息触发、产出结束即销毁的执行序列。对话记忆不在进程内存中，而在 Logic 的消息历史里。

### 7.1 为什么 Pilot Service 必须无状态

AI 会话的跨度通常远超一次响应。用户今天问一个问题，隔天继续问下一个，时间跨度可能是几分钟也可能是几天。如果把 session 做成一个常驻的进程对象，系统就会立刻面对三个问题：节点重启会丢失所有对话上下文；水平扩容时必须做 session 亲和性调度；内存占用会随活跃对话数线性增长。

相反，如果 Pilot Service 把自己当成一个无状态消费者，这三个问题全部消失。任何节点都可以接任何会话的下一条消息，因为所有"记忆"都来自外部存储——Logic 的消息历史、Redis 里的 AgentProfile 缓存、PostgreSQL 里的 tool call 持久化表。这也与 IM 系统本身的心智一致：**消息即事实，历史即状态**。

### 7.2 每次响应的完整生命周期

```text
MQ: chat.events 到达（AI 会话 + 真人消息）
  ├── 从 Logic 拉取该会话最近 K 条历史
  ├── 从 Redis / PG 加载 AgentProfile
  ├── 读取上下文边界 marker（若用户曾执行 /reset）
  ├── 组装 Prompt = system + tools + history + current
  ├── 运行 Agent Loop（流式 / 工具调用 / 审批等待）
  ├── 通过 Logic.SendEvent 输出所有用户可见事件
  └── 响应结束，释放所有进程内存
```

这里最关键的一步是"从 Logic 拉取历史"。对 Pilot Service 来说，**conversation_id 就是 session**；所谓"加载上下文"不过是一次普通的消息历史拉取，和 Web 客户端重新打开一个会话拉历史没有本质区别。不存在额外的"session 初始化"协议。

### 7.3 历史加载策略

Pilot Service 在构建 prompt 时需要决定加载多少历史。基本策略有三条：

- **Token 预算优先**：按模型上下文窗口的一定比例（例如 50%）留给历史，其余留给工具定义、当前消息与模型输出
- **最近优先**：按时间倒序加载，到预算上限为止
- **摘要兜底**：当历史超出预算仍需要更早的上下文时，把最早一段压缩为摘要，保留最近 N 轮完整消息

这三条策略的具体实现细节属于 Harness 内部，放在 `15-agent-harness.md` 展开。本文档只需要明确一件事：历史加载是 Pilot Service 作为 Logic 客户端的一个普通行为，不是新的系统协议。

### 7.4 跨响应的持久状态

并非所有状态都可以在单次响应结束后丢弃。以下几类状态必须持久化，下次响应时按需重新加载：

| 状态 | 存储位置 | 说明 |
| ---- | -------- | ---- |
| 待审批的 tool call | `t_tool_call` | 用户 Decision 可能跨几分钟甚至几小时才到 |
| AgentProfile | Redis 缓存 + PG 权威 | 会话启动时加载，命令可切换 |
| 上下文边界 marker | `t_agent_context` | 用户通过 `/reset` 显式切断历史时使用 |
| 成本与配额计数 | Redis | 会话级 QPS、每日 token 消耗 |

**不持久化的状态**：模型的思考 token、规划草稿、中间 tool call 迭代、子 Agent 之间的对话。这些每次响应重新推理。代价是重复计算，收益是简单与可水平扩容。

### 7.5 节点重启与消费者重平衡

因为进程无状态，节点重启对系统的影响只有一个：**正在运行中的 Agent Loop 会丢失**。这会导致两种情况：

- **未产出任何事件的 Loop**：用户视角完全感知不到丢失，重启后 MQ 会把未 ACK 的消息重投，下一次处理正常产出
- **已产出部分事件（如 StreamDelta）但未产出最终 Message 的 Loop**：用户会看到一个气泡停在中间态，始终不到终态。这类情况由 Pilot Service 的监护任务兜底：若 N 秒内没有看到定版 Message，系统回写一条错误消息说明"生成中断"，并允许用户重试

MQ 消费者重平衡遵循同样的幂等原则：每条用户消息只应该被处理一次（按 `event_id` 去重），重复消费不会导致重复响应。

### 7.6 跨天消息的表现

当用户在一次响应结束后很久（可能一天）再发下一条消息，Pilot Service 的行为与第一次响应完全相同：从 Logic 拉历史、重建 prompt、运行 Loop。**没有"session 过期"的概念**，因为根本没有 session 对象可以过期。

唯一的副作用是 Prompt Cache 的命中率：大模型供应商的缓存 TTL 通常在分钟到小时级，隔天的首轮必然 miss。这是成本问题而非正确性问题，相关讨论放在 `15-agent-harness.md` 的缓存策略一节。

---

## 8. ChatEvent Payload 扩展

Pilot Service 的落地需要在 `common/v1/event.proto` 中新增四个 payload 分支。它们都继续遵循"一次会话内变化对应一个 ChatEvent"的原则。

### 8.1 新增 Payload

```proto
message StreamDelta {
  int64  target_event_id = 1;  // 定版消息的 event_id
  string content_delta   = 2;  // 本段增量
  bool   is_final        = 3;  // 最后一段（之后会发 Message 定版）
}

message ToolCallRequest {
  string call_id         = 1;  // Pilot Service 生成，贯穿 Request/Result/Decision
  string tool_name       = 2;
  bytes  arguments_json  = 3;
  string reason          = 4;  // 模型给出的调用理由
  bool   needs_approval  = 5;  // 是否需要人类审批
}

message ToolCallDecision {
  string call_id  = 1;
  bool   approved = 2;
  string comment  = 3;         // 用户备注（可选）
}

message ToolCallResult {
  string call_id     = 1;
  bool   success     = 2;
  bytes  result_json = 3;
  string summary     = 4;      // 面向 UI 的简短描述
}
```

### 8.2 为什么沿用 oneof 而不是独立协议

系统其他地方已经假定"一条会话内变化 = 一个 ChatEvent"。如果为 AI 单独建一套事件通道，Task 的写扩散、Gateway 的推送、Web 的渲染、Inbox 的增量同步都要双重实现。沿用 oneof 的代价只是多四个分支，收益是整条链路完全复用。这与 Phase 5–6 中 Recall / Edit / ReadReceipt 的接入模式完全一致。

### 8.3 Logic 在新增 payload 上的处理

| Payload | Logic 是否落主事实 | 说明 |
| ------- | ------------------ | ---- |
| `StreamDelta` | 否 | 只分配 `event_id` 与 `seq_id`（若复用定版 ID，则直接转发），经 Outbox 推送 |
| `ToolCallRequest` | 是 | 落独立的 `t_tool_call` 表，承载审批状态 |
| `ToolCallDecision` | 是 | 更新 `t_tool_call.status`，产出对应事件 |
| `ToolCallResult` | 是 | 同上，记录执行结果 |

三张与 tool call 有关的事件共享一个 `call_id`，审批流程的完整状态机在 `t_tool_call` 上维护。

---

## 9. Slash Command 与消息链路

Slash Command（如 `/reset`、`/profile`、`/tools`）是 AI 会话中常见的交互方式。系统在设计上坚持一个简单原则：**命令就是带前缀的普通消息**，不走旁路通道、不建新 RPC、不改 Gateway 或 Logic。所有命令处理逻辑都收敛在 Pilot Service 内部。

### 9.1 核心原则

命令消息在 Gateway、Logic、Task、Web 的链路上与普通消息完全一致：同样的 `SendEvent`、同样的 Outbox、同样的写扩散、同样的历史落地。只有在消费端（Pilot Service 与前端输入框），才会把带 `/` 前缀的消息识别成命令。

这样做的收益有四点：

- 命令本身进入会话历史，天然可审计、可搜索、可回放
- 任何客户端（Web、移动、纯 API）都能使用命令，不依赖特殊协议
- Gateway / Logic / Task 完全不需要知道命令存在，职责边界保持清爽
- 未来接入新命令时，只需在 Pilot Service 内注册 handler，不动任何对外契约

### 9.2 处理链路

```text
用户输入 "/reset"
    ↓
Gateway ──▶ Logic.SendEvent (Message payload, content="/reset")
    ↓
Outbox ──▶ MQ: chat.events
    ↓
Pilot Service 订阅 ── 检查 content 首字符是否为 '/'
    ├── 是 ──▶ 分派到 Command Registry ──▶ handler 执行
    │           └── 通过 Logic.SendEvent 回写 Bot 响应（可能是系统提示，也可能是命令执行结果）
    └── 否 ──▶ 进入正常 Agent Loop
```

命令 handler 的执行不经过 LLM，属于 Harness 硬编码的确定性动作。常见形态包括：修改 AgentProfile、写入上下文边界 marker、查询内部状态并组装成消息回写。

### 9.3 命令的分类

| 类型 | 示例 | 说明 |
| ---- | ---- | ---- |
| 上下文控制 | `/reset` / `/clear` | 插入边界 marker，后续响应只加载 marker 之后的历史 |
| Profile 切换 | `/profile aiops` | 切换当前会话的 AgentProfile |
| 模型切换 | `/model claude-opus-4-7` | 切换 LLM Provider 或具体模型 |
| 能力查询 | `/tools` / `/help` | 列出当前可用工具或命令 |
| Profile 专属 | `/describe <service>` | 由 AgentProfile 声明的快捷方式，等价于预置 tool 调用 |

具体命令清单与扩展方式属于 Harness 内部实现，详见 `15-agent-harness.md`。

### 9.4 新会话不做成命令

新建一个全新的独立对话（类似 ChatGPT 的 "+ New Chat"）**不适合做成 slash command**。原因是它的预期副作用是"跳转到另一个会话"，而命令消息本身停留在当前会话里——两者语义冲突。

正确的做法是在前端 AI 入口加一个 "+" 按钮，调用 `Logic.CreateConversation` 创建一个新的 AI 会话并跳转过去。这是一个 UI 行为，不是 Pilot Service 的职责，相关实现放在 `13-web.md`。

如果系统仍希望提供 `/new` 命令，其行为应等价于 `/reset`——清空当前会话的上下文，而不是新建会话。

### 9.5 未识别命令的处理

当用户输入一个不存在的命令（比如 `/foo`），Pilot Service 的默认策略是返回一条系统提示："未知命令 `/foo`，输入 `/help` 查看可用命令"。**默认不把它当普通消息交给 LLM**，避免用户误打字导致额外 token 消耗。

这条策略是 AgentProfile 级别可配置的：某些偏开放的 Profile 可以选择"未识别命令 fallthrough 给 LLM"，但这不是系统默认行为。

---

## 10. 权限、防滥用与审计

AI 可以调工具、可以访问运维数据，这件事本身就是一个高风险面。Pilot Service 的安全边界必须在会话级而非用户级生效。

### 10.1 AgentProfile

每个 AI 会话绑定一个 AgentProfile（或会话创建时指定一个默认 Profile）。Profile 声明以下内容：

| 字段 | 作用 |
| ---- | ---- |
| `allowed_tools` | 可用工具列表（按 MCP server 名称或工具名白名单） |
| `data_scope` | 数据作用域（如 Prometheus 数据源、Docker 环境、k8s namespace） |
| `approval_policy` | 审批策略（只读自动执行 / 写操作必须审批 / 全部审批） |
| `rate_limit` | 会话级 QPS 与每日 token 配额 |
| `system_prompt` | 附加的系统提示词 |

Profile 在 Pilot Service 内部生效，Logic 与 Task 不感知具体内容。

### 10.2 审批默认策略

以 AIOps 场景为例，工具按副作用分两类，默认策略不同：

| 类型 | 示例 | 默认策略 |
| ---- | ---- | -------- |
| 只读 | 查询 Prometheus 指标、grep 日志、`docker ps` | 自动执行，仍记 Audit |
| 变更 | 重启容器、修改配置、`kubectl rollout` | 强制 HITL，必须审批 |

这套默认策略可以在 AgentProfile 中覆盖，但整体方向是"变更必审"。

### 10.3 防滥用

- 使用 `genesis/ratelimit` 做会话级 QPS 限制与每日 token 配额
- 异常模式（短时间内大量失败、异常工具调用序列）通过监控规则触发告警
- 达到配额后，Pilot Service 直接回写一条系统提示类 Message，并拒绝继续消费

### 10.4 审计

每次 LLM 调用、每次工具执行、每次审批决策都写入独立的审计表 `t_agent_audit_log`：

- Prompt、Completion、Token 使用、时延
- 工具调用入参与结果
- 审批人与决策时间

这张表与 ChatEvent 历史相互独立，供合规与事后回放使用。

---

## 11. 当前实现状态

截至本文档产出时，Pilot Service 尚未进入代码仓库：

- `pilot/` 目录暂未创建
- `common/v1/event.proto` 中的 `StreamDelta / ToolCallRequest / ToolCallDecision / ToolCallResult` 为文档预留，未落 proto
- Bot 用户与 `is_bot` 字段未加入 `t_user`
- AgentProfile 与 `t_tool_call / t_agent_context / t_agent_audit_log` 未建表

本文档用于在 Phase 7 启动前锁定协议契约与服务边界。真正动手实现时，建议按下列顺序推进：

1. proto 层新增四个 payload 分支并跑通 `make gen`
2. Logic 侧补齐 StreamDelta 的透传与 ToolCall 三张事件的主事实写入
3. `init` 模块种子化 Bot 用户与默认 AgentProfile
4. 启动 Pilot Service 骨架：订阅 → Agent Loop → 回写 Logic
5. 接入第一个 MCP 工具（建议从只读工具开始，例如 `docker ps`）
6. 前端按 payload 类型渲染审批卡片与流式气泡

---

## 12. 阅读建议

本文档描述的是 Pilot Service 的职责与契约边界。继续往下看时，建议结合以下文档一起阅读：

| 文档 | 内容 |
| ---- | ---- |
| `00-overview.md` | Pilot Service 在整体架构中的位置 |
| `01-protocol.md` | 统一事件模型与 payload 扩展模式 |
| `11-logic.md` | Pilot Service 调用的业务入口与事务边界 |
| `12-task.md` | AI 产出的事件如何在异步层继续扩散 |
| `15-agent-harness.md` | Agent Harness 内部的场景、Loop、上下文、工具、命令实现 |
| `22-recall-edit-read.md` | Recall / Edit / ReadReceipt 的统一扩展模式（可类比 AI 的 payload 扩展） |

---

## 13. 小结

如果用一句话概括 Pilot Service，那么它是 Resonance 中的**会话参与者，而不是接入层**。它订阅事件、驱动 Agent Loop、以 Bot 身份回写 Logic；它不碰数据库、不碰推送、不碰连接；它对自己无状态这件事毫不遗憾，因为会话历史本身就是它的记忆。只要这条边界守住，AI 能力的演进就始终是"在现有骨架上扩展"，而不是"为 AI 重写一套平行系统"。Recall / Edit / ReadReceipt 已经证明 ChatEvent + oneof 足以承载不同类型的会话变化，AI 的流式响应、工具调用、人类审批与 slash command 只是这套模式的下一个自然延伸。
