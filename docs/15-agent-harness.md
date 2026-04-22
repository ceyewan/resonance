# Agent Harness 设计

> 本文档描述 Resonance 中 Pilot Service 内部 Agent Harness 的设计——即 LLM 调用之外的全部工程部分。阅读完本文后，应该能回答三个问题：这个 Agent 到底要做什么场景；Agent Loop 的状态机与上下文如何组装；以及工具、命令、RAG、Provider 这四个扩展点各自承担什么。
>
> **说明**：本文档聚焦 Pilot Service 内部实现细节。Pilot Service 与 Resonance 其他服务之间的契约边界见 `14-ai-service.md`。当前代码仓库中尚无 `pilot/` 目录，本文档用于 Phase 7 启动前锁定 Harness 的目标与骨架。

---

## 1. Agent Harness 的定位

Agent Harness 是 Pilot Service 内部"除 LLM 模型之外的一切"。它包括 Agent Loop 的状态机、上下文的组装、工具的注册与执行、命令的分派、Provider 的适配、RAG 的检索、成本与缓存的控制。换句话说，如果说 LLM 是"大脑"，那么 Harness 就是这个大脑的感官、肌肉、记忆、边界与反射。

对 Resonance 来说，Harness 的定位不是"让模型能回答问题"，而是"让模型能在生产场景里稳定地完成任务"。这意味着它既要让模型可以调用真实世界的工具（查指标、grep 日志、操作容器），也要让用户始终能够理解、审批、打断模型的行为。它不是一个通用聊天机器人，而是一个有边界、有策略、有审计的运维助手。

---

## 2. 目标场景

Harness 的设计必须先明确"做什么"，再讨论"怎么做"。本节锁定第一阶段的目标场景，作为后续所有设计决策的依据。

### 2.1 主场景：AIOps 助手

Harness 的核心场景是 AIOps——把 AI 作为一个"懂运维、能操作、受约束"的助手，嵌入到工程师的日常工作流中。具体包含五个典型剧本：

1. **实时观察**：用户问"生产环境 API 的 P99 延迟怎么样？"→ Harness 调用 Prometheus 工具 → 返回指标摘要与同比变化
2. **告警分析**：用户粘贴一条告警消息 → Harness 关联最近日志、相关指标、近期变更 → 给出可能原因的排序列表
3. **变更评估**：用户问"我要重启 order-service，会有什么影响？"→ Harness 查询上下游依赖、近期错误率、当前在线用户 → 返回风险评估，提醒是否应走审批
4. **故障诊断**：用户问"为什么 payment 容器频繁重启？"→ Harness 查询 k8s events、容器日志、资源使用 → 定位 OOM / 健康检查失败 / 配置错误等具体原因
5. **架构咨询**：用户问"ChatEvent 是什么？为什么要 Outbox？"→ Harness 通过 RAG 在 `docs/` 中检索 → 引用文档回答，并附上相关章节链接

这些剧本共同的特征是：**问题起于对话，答案来自工具与文档，重要决策必须由人类拍板**。

### 2.2 明确不做的事

为了保持第一阶段的边界清晰，以下能力**暂不纳入**：

| 不做的事 | 原因 |
| -------- | ---- |
| 代码生成与自动修改代码 | 超出 AIOps 范围，另有 IDE 类工具更合适 |
| 多 Agent 协作（Planner / Evaluator 分离） | 单 Loop 先跑通，三 Agent 架构留作 v2 |
| 跨会话的长期记忆 | 需要专门的记忆系统，短期用 RAG 代替 |
| 主动提醒 / 定时任务 | 超出响应式会话模型，需要独立 scheduler |
| 多模态输入输出（图像 / 语音） | 第一阶段只做文本 |

这份排除清单比能力清单更重要：它锁定了 Harness 不会随着对话场景扩大而无限膨胀。

### 2.3 非目标场景的兜底

如果用户在 AI 会话中问了一个超出 AIOps 范围的问题（比如"帮我写个 React 组件"），Harness 不会拒绝回答，而是按"通用问答"降级处理——只允许文本响应，不调用任何工具。这类会话可以通过 AgentProfile 的 `approval_policy: read-only` 天然限制。

---

## 3. Agent Loop 状态机

Agent Loop 是 Harness 的主干。它的核心不是"一次函数调用"，而是一个**有限状态机**，每次状态切换都由具体事件驱动。

### 3.1 状态定义

```text
IDLE              ── 收到用户消息 ──▶ LOADING_CONTEXT
LOADING_CONTEXT   ── 历史与 Profile 加载完成 ──▶ THINKING
THINKING          ── 模型产出 ──▶
                      ├── 文本流 ─▶ STREAMING ─▶ THINKING（继续）
                      ├── tool_call ─▶ TOOL_DISPATCH
                      └── final ─▶ FINALIZING
TOOL_DISPATCH     ── 只读 / profile 允许 ──▶ EXECUTING
                  ── 变更 / 需要审批 ──▶ AWAITING_APPROVAL
AWAITING_APPROVAL ── Decision(approved) ──▶ EXECUTING
                  ── Decision(rejected) ──▶ THINKING（告知模型）
                  ── 超时 ──▶ FINALIZING（回写超时提示）
EXECUTING         ── 工具结果 ──▶ THINKING
FINALIZING        ── 写入定版 Message ──▶ DONE
DONE              ── 释放资源 ──▶ (进程结束本次响应)
```

### 3.2 为什么做成状态机而不是串行流程

如果只用简单的顺序调用，AWAITING_APPROVAL 这类可能跨越数小时甚至跨进程重启的状态就无处安放。状态机的价值在于：**每个状态都可以被持久化**。当 Harness 进入 AWAITING_APPROVAL 时，当前 tool_call 和对话上下文会被保存到 `t_tool_call`；几小时后用户点"同意"，一个完全不同的进程实例可以从 `t_tool_call` 重建状态，继续执行，用户体验无感。

### 3.3 单次响应内的事件驱动

单次响应内部（从 THINKING 到 FINALIZING），Harness 不依赖持久化——全部状态在内存里流转，通过 Go 的 channel 与 context 协调。只有跨越"需要等待外部事件"的边界（审批、长时间工具执行）时，状态才需要落地。这与 `14-ai-service.md` 中"无状态进程"原则一致：Harness 只在必要时持久化，大多数时候都是临时内存对象。

---

## 4. 上下文管理

上下文管理是 Harness 决定"模型看到什么"的环节。它直接决定两件事：模型回答的质量，以及单次调用的成本。

### 4.1 Prompt 组装顺序

Prompt 的组装遵循一个**确定性的顺序**，从稳定到易变：

```text
[1] System Prompt            ── AgentProfile.system_prompt（稳定）
[2] Tool Definitions         ── 当前 Profile 允许的工具 schema（稳定）
[3] RAG 检索结果             ── 若启用，按 doc_id 排序后拼入（稳定性中等）
[4] History Messages         ── Logic 拉取的历史（稳定）
[5] Current User Message     ── 当前触发本次响应的消息（变化）
```

这个顺序的意义在于**对齐 Prompt Cache 的前缀模型**：前面越稳定的内容，越容易被供应商的缓存命中。具体缓存断点的放置策略见第 9 节。

### 4.2 历史加载与裁剪

Harness 调用 `Logic.GetHistory(conv_id, before=now, limit=K)` 加载历史消息。K 的默认值按三级规则确定：

- **按 token 预算**：用 `tiktoken` 或类似库估算，给历史留约模型上下文窗口的 50%（其余留给工具定义、RAG 与输出）
- **按最近 N 轮**：默认最多 40 条消息（约 20 轮对话），防止极短消息把预算撑爆条数上限
- **按时间窗**：默认只加载最近 7 天，更早的内容除非用户显式提及否则不加载

三条规则取最严格的那一条。

### 4.3 上下文边界 marker

当用户执行 `/reset`，Harness 向 `t_agent_context` 写入一条边界 marker：

```text
(conv_id, marker_event_id=E, created_at=T, reason="/reset")
```

之后该会话的历史加载会加上过滤条件 `event_id > E`，marker 之前的消息完全不进 prompt。marker 本身不删除任何消息，只是改变加载行为。用户可以继续看到原历史，但模型不会。

### 4.4 摘要兜底

当用户确实需要模型理解一段超长的历史（比如跨越数周的诊断记录），Harness 提供摘要兜底机制：

1. 触发条件：历史条数超过阈值（例如 200 条）或 token 预算超出两倍
2. 执行方式：调用 LLM 生成一段压缩摘要（通常 500–1000 token），保存到 `t_agent_context.summary`
3. 使用方式：下次加载时，最早的 N 条消息被摘要替换，最近 M 条保持原文

摘要不是默认行为，只在超出阈值时触发。一次会话中最多存在一条摘要，每次再次触发会重新生成并覆盖。

### 4.5 确定性与缓存友好

Prompt 组装必须是**确定性的**——同样的输入必然产生同样的字节序列。这意味着以下几类内容**不能**出现在 prompt 里：

- 时间戳（`now()`、`request_id`）
- 随机 ID（UUID、追踪 ID）
- 变化的系统状态（当前在线人数、实时负载）

这些信息如果必须让模型知道，应该作为 tool 的返回值传递，而不是硬编码进 prompt。

---

## 5. 工具管理

工具是 Harness 暴露给模型的"外部世界的入口"。设计上坚持两个原则：**统一注册、分级审批**。

### 5.1 Tool Registry

所有工具通过一个统一的 Registry 接口注册：

```go
type Tool interface {
    Name() string                               // 工具唯一名
    Description() string                        // 模型可见的描述
    InputSchema() jsonschema.Schema             // 输入参数 schema
    Risk() RiskLevel                            // ReadOnly / Mutation / Dangerous
    Execute(ctx, args []byte) (ToolResult, err) // 执行入口
}

type Registry interface {
    Register(t Tool)
    Resolve(profile *AgentProfile) []Tool   // 按 profile 过滤
    Get(name string) (Tool, bool)
}
```

Registry 是进程级单例，启动时注册所有可用工具；每次请求按 AgentProfile 的 `allowed_tools` 过滤出本次对话可见的子集。

### 5.2 工具来源

Harness 支持三类工具来源：

| 来源 | 说明 | 示例 |
| ---- | ---- | ---- |
| **内建工具** | Go 代码实现，进程内直接调用 | `docs_search`（RAG）、`health_check`、`echo`（调试用） |
| **MCP Server** | 通过 MCP 协议调用外部进程 | Prometheus MCP、Docker MCP、k8s MCP |
| **HTTP Tool** | 直接通过 HTTP 调用内部服务 | 自研监控平台的查询接口 |

三类工具在 Registry 层面统一抽象。模型看到的只是"工具名 + schema"，不知道背后是进程内函数、MCP 子进程还是 HTTP 调用。

### 5.3 MCP 客户端

MCP 客户端是 Harness 与外部工具进程通信的标准通道。每个 MCP Server 作为独立子进程运行（Docker MCP、Prometheus MCP 等各自独立），Harness 通过 stdio 或 WebSocket 与它们通信。

MCP Server 的配置声明在 AgentProfile 中：

```yaml
mcp_servers:
  - name: prometheus
    command: ["./mcp-prometheus", "--endpoint", "http://prom:9090"]
  - name: docker
    command: ["./mcp-docker", "--socket", "/var/run/docker.sock"]
```

启动时由 Harness 拉起对应子进程，每个会话独占或共享（按配置决定）。

### 5.4 审批策略与执行

每个工具声明一个 `Risk()` 等级，Harness 按 AgentProfile 的 `approval_policy` 决定是否需要人工审批：

| Risk | 含义 | 默认策略 |
| ---- | ---- | -------- |
| `ReadOnly` | 只读，对外部状态无副作用 | 自动执行 |
| `Mutation` | 有副作用但可逆（创建/更新配置） | 需审批 |
| `Dangerous` | 不可逆或可能导致事故（删除、重启、生产变更） | 强制审批，且需要附加理由 |

AgentProfile 可以收紧策略（比如 `ReadOnly` 也需要审批），但不能放松 `Dangerous` 的审批要求。

### 5.5 内建工具的最小集

第一版 Harness 至少实现以下内建工具，作为跑通链路的最小集：

- `docs_search(query, top_k=5)`：在 `docs/` 的向量索引里检索
- `now()`：返回当前时间（这是少数可以允许返回动态值的工具）
- `echo(text)`：纯回显，用于单测与调试

MCP 工具从 Prometheus / Docker 两个开始，覆盖"查指标 + 查容器"两大高频场景。

---

## 6. RAG 子系统

RAG 在 Harness 中作为一个**特殊的内建工具**存在，而不是 prompt 层面的隐式注入。

### 6.1 语料来源

第一版 RAG 的语料**只来自 `docs/` 目录**。理由有三：

- 内容高质量，天然结构化
- 与系统架构强相关，正好对齐"架构咨询"场景
- 数据量小（20+ 篇文档），索引构建快，维护成本低

未来扩展的可能方向包括：内部 wiki、Runbook、故障复盘记录。但这些都不在第一版。

### 6.2 索引构建

索引构建通过独立命令完成，不进运行时：

```bash
make rag-index
```

流程：

1. 扫描 `docs/*.md`
2. 按标题与段落切分（每段不超过 1000 token，保留完整小节）
3. 通过 Provider 的 embedding 接口向量化
4. 存入本地向量库（第一版用 `sqlite-vss` 或 PostgreSQL pgvector，由 `genesis/connector` 选择）
5. 同时生成 `docs_index.json`，保存每个切片的 `(doc_id, section, content)`

### 6.3 检索与注入

`docs_search` 工具的调用流程：

```text
query ──▶ embedding ──▶ 向量检索 top_k ──▶ 按 doc_id 排序 ──▶ 拼装 "引用片段 + 原文 URL" 返回
```

模型拿到 top_k 个片段后，自行决定是否引用、如何引用。这种"工具化 RAG"的好处是模型可以自己判断何时需要检索——简单问题直接回答，复杂问题才检索。相比每次 prompt 都强行注入检索结果，更灵活也更省 token。

### 6.4 切片的稳定性

切片顺序必须稳定：同样的 query 多次检索返回同样的片段组合，排序也一致。这是为了与 Prompt Cache 配合——如果检索结果每次都在变，前缀缓存基本失效。实现上要求：

- 向量检索的 top_k 按分数降序，分数相同按 `doc_id + section` 字典序
- 拼装结果时按 `doc_id` 二次排序，同 doc 的多个片段按原文顺序

### 6.5 更新策略

`docs/` 变动后，RAG 索引不会自动更新，必须显式执行 `make rag-index`。这是刻意的选择——避免运行时对 `docs/` 目录的写操作敏感，也让索引版本与代码版本一起走 CI。

---

## 7. 命令注册表与内建命令

Slash Command 的系统集成契约见 `14-ai-service.md` 第 9 节。本节描述 Harness 内部的实现。

### 7.1 Command 接口

```go
type Command interface {
    Name() string                            // "reset"
    Scope() CommandScope                     // Global | ProfileSpecific
    Describe() string                        // /help 显示用
    Handle(ctx, convID, args []string) (reply string, err error)
}

type CommandRegistry struct {
    global   map[string]Command
    profile  map[ProfileID]map[string]Command
}

func (r *CommandRegistry) Lookup(profileID ProfileID, name string) (Command, bool)
```

Global 命令对所有会话可用；ProfileSpecific 命令只在绑定了对应 Profile 的会话中可用。

### 7.2 内建 Global 命令

| 命令 | 行为 |
| ---- | ---- |
| `/help` | 列出当前会话可用的所有命令（Global + 当前 Profile 的 Specific） |
| `/reset` / `/clear` | 写入上下文边界 marker，后续响应只加载 marker 之后的历史 |
| `/profile <name>` | 切换当前会话的 AgentProfile（需要用户在 Profile 列表中有权限） |
| `/model <name>` | 切换当前会话使用的 LLM 模型 |
| `/tools` | 列出当前 Profile 允许的工具清单 |
| `/whoami` | 显示当前会话绑定的 Profile、模型、配额使用情况 |

### 7.3 Profile 专属命令

AgentProfile 可以声明自己的命令映射：

```yaml
commands:
  - name: describe
    tool: aiops.describe_service
    desc: "/describe <service> 汇总服务当前状态"
    args: [service]
  - name: incidents
    tool: aiops.list_incidents
    desc: "/incidents [last=1h] 列出最近告警"
    args_schema: {last: string}
```

本质上这些命令是"预置的 tool 调用"——Harness 收到命令后，直接以指定参数执行对应 tool，跳过模型规划步骤。这对高频、格式固定的运维操作非常有用。

### 7.4 命令的回写形态

命令执行的结果通过普通 `Logic.SendEvent(Message)` 回写为 Bot 消息。前端可以根据消息元信息（例如在 Message 上附加 `is_command_reply=true` 的 metadata）用不同样式渲染，但这不改变消息本身的结构。

---

## 8. Provider 抽象

Provider 抽象让 Harness 可以在不改业务逻辑的前提下切换 LLM 供应商。

### 8.1 Provider 接口

```go
type Provider interface {
    Name() string
    Chat(ctx, req ChatRequest) (<-chan ChatEvent, error)   // 流式返回
    Embed(ctx, texts []string) ([][]float32, error)        // 向量化
}

type ChatRequest struct {
    Model       string
    Messages    []Message
    Tools       []ToolSchema
    CachePoints []int           // 在 Messages 的哪些索引后放置缓存断点
    Options     ChatOptions     // 温度、top_p、最大 token 等
}

type ChatEvent struct {
    Kind       EventKind  // TextDelta | ToolCall | Stop | Error
    TextDelta  string
    ToolCall   *ToolCallSpec
    StopReason string
}
```

### 8.2 首批支持的 Provider

| Provider | 模型 | 说明 |
| -------- | ---- | ---- |
| Anthropic | `claude-opus-4-7` / `claude-sonnet-4-6` | 首选，工具调用稳定、缓存机制成熟 |
| OpenAI | `gpt-5.3` / `gpt-5.4` | 备选，部分场景需要快速迭代 |

Provider 的具体实现放在 `pilot/model/provider_<name>.go`，由配置选择。

### 8.3 错误映射

Provider 内部将各家 SDK 的错误映射到统一的语义分类：

- `RateLimited` → 退避重试
- `ContextTooLong` → 触发摘要兜底，重试一次
- `ToolCallMalformed` → 降级为纯文本响应，提示用户
- `Fatal` → 回写系统消息，结束本次响应

业务层代码只面对这四类错误，不关心底层 SDK 的具体错误格式。

---

## 9. 缓存与成本

本节作为 TODO 占位，详细策略在首版功能稳定后再展开。

### 9.1 缓存断点策略（待实施）

计划按以下断点放置策略：

| 断点位置 | 是否启用 | 原因 |
| -------- | -------- | ---- |
| System Prompt 末尾 | ✅ | 固定不变，全局复用 |
| Tool Definitions 末尾 | ✅ | Profile 稳定时不变 |
| RAG 检索结果末尾 | 🔶 | 按 query 哈希决定，相同 query 才启用 |
| 历史的倒数第二轮末尾 | ✅ | 多轮对话命中 |
| 当前消息之前 | ❌ | 当前消息必然变化，不设断点 |

### 9.2 成本可观测

每次 LLM 调用记录四个指标：

- Provider / Model
- Input / Output token 数
- 是否缓存命中（供应商返回）
- 端到端延迟

这些指标汇总到 OpenTelemetry，供后续成本分析。会话级与 Profile 级的配额消耗在 Redis 里实时累计。

### 9.3 成本优化的后置原则

在第一版跑通之前，**不做任何针对成本的优化**。过早优化会让基础 Loop 的复杂度提前膨胀，反而延迟真正的问题暴露。有了真实流量与账单之后，再回头优化断点策略、模型选择、摘要频率等参数。

---

## 10. 当前实现状态

截至本文档产出时，Agent Harness 整体处于设计阶段：

- `pilot/` 目录尚未创建
- Tool Registry、Command Registry、Provider 抽象均为设计预留
- RAG 索引与 `docs_search` 工具未实现
- MCP 客户端骨架未搭建

建议的首版落地顺序（与 `14-ai-service.md` 第 11 节的系统侧步骤并行）：

1. Provider 接口 + Anthropic 实现 + 最小 Chat 跑通（不带工具）
2. Tool Registry + `echo` 内建工具 + 走一次"用户消息 → 模型调 tool → 返回结果"链路
3. 状态机骨架 + `AWAITING_APPROVAL` 的持久化（`t_tool_call`）
4. Command Registry + `/help` / `/reset` / `/tools` 三个内建命令
5. RAG 索引构建（`make rag-index`）+ `docs_search` 工具
6. MCP 客户端 + Prometheus / Docker 两个外部工具
7. 缓存断点策略与成本可观测
8. AIOps 场景下五个典型剧本的端到端验证

---

## 11. 阅读建议

本文档描述的是 Harness 内部的实现设计。继续往下看时，建议结合以下文档一起阅读：

| 文档 | 内容 |
| ---- | ---- |
| `14-ai-service.md` | Pilot Service 与 Resonance 其他服务的契约边界 |
| `01-protocol.md` | ChatEvent 与 payload 扩展的整体模式 |
| `04-observability.md` | 指标、trace 与日志的系统级约定 |
| `11-logic.md` | Harness 作为客户端调用 Logic 的业务入口 |

---

## 12. 小结

如果用一句话概括 Agent Harness，那么它是 Resonance 中让"AI 能做正确的事、能不做错误的事"的工程骨架。它的目标不是把模型包得更花哨，而是让模型在 AIOps 这个具体场景里拥有**足够的能力**（工具、RAG、命令）与**足够的约束**（审批、配额、审计）。状态机、上下文管理、工具注册、命令分派、Provider 抽象这五个模块之所以各自独立，是因为它们都是未来可能被反复替换、扩展、重构的部分；把它们封装好，模型换一版、工具换一批、Provider 换一家，Harness 的主干依然成立。
