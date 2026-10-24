# Pilot：每用户唯一的私人助手

## 产品边界

Pilot 是一个全局、不可登录的 Bot 用户（默认 `resonance-agent`），不是 IAM 管理平台。
每个用户在当前租户只有一个固定的 Pilot 私聊，始终连续对话，不能另建、分叉或加入第三人。
不同用户/租户共享 Bot 身份，不共享聊天记录、Pi Session 或上下文。

注册、登录与会话列表会幂等补齐私聊；`CreateAgentSession` 是无参的“获取或确保存在”，不是创建新聊天。
会话 ID 只由 tenant + owner 决定；数据库部分唯一索引 `uniq_pilot_owner(tenant_id, owner_username) WHERE kind=1` 是并发兜底。
模型、Prompt 或 Runtime 版本变化不能产生第二个会话。

## 消息链路

```text
Web → Gateway → Logic → Outbox → NATS → Pilot → Pi → 模型
                         ↑               │
                         └──── gRPC ─────┘ 最终 Bot 消息

临时文本：Pilot → NATS → Task → Gateway → WebSocket → Web
```

- 用户消息和最终回复复用现有 ChatEvent、Outbox、Inbox、历史与离线同步。
- Pilot 只将用户文本入队，不响应自己的 Bot 消息。
- 入口先用 MQ 的 `target_usernames` 判断 Bot 是否为本条事件收件人，再读一次会话：
  普通聊天完全不经过 Pilot 的数据库，也不必为每条全系统消息做一次查询。
- 这一次会话读取是**有意保留**的：它给出签名所需的 tenant，以及 AI 私聊 + owner
  三项权威事实。`target_usernames` 蕴含"Bot 是成员"，但那是 Logic 侧的不变量，
  由被影响的一方在运行时校验一次比跨服务口头约定更可靠。代价是每个 Pilot 对话
  回合一次主键查询，相对于该回合的写事务与模型调用可以忽略。
- 不要为省这一次读去给 `MQEvent` 加 `tenant_id`：收益是一次主键查询，代价是
  协议字段、Logic 侧逐点透传和两处重复事实。真正的杠杆是消息量上来后按 kind
  拆分 subject，让 Pilot 不再接收全量聊天事件。
- `source_event_id` 唯一，MQ 重投不会产生第二个 Run。
- 同一会话按 source sequence 串行执行，不同会话并行，进程数受 Worker 数限制。
- 最终回复以 `agent:<run_id>:final` 为幂等键，经 Logic 写入；不允许直接改消息表。
- 流式增量是临时展示，可丢弃；最终 ChatEvent 是权威事实。

## 连续对话

每次 Run 用独立 Pi 子进程，加载该私聊已提交的 Session，成功后提交新快照。
失败只影响 staging，不污染上一轮。Pi 自动压缩上下文；Session 丢失、历史编辑/撤回或文件过大时，从 Logic 有效历史重建。
Pilot 自己订阅 Recall/Edit 事件：取消该会话所有未落地的 Run、把已提交快照置 `DIRTY`，Logic 的消息事务不认识 `t_agent_*` 表。
每个 Run 执行前都会用 Logic 权威历史校验源事件，被撤回或改写的源消息直接失败重试，不依赖跨模块取消。

### 提交闸门

最终回复写入 Logic 之前必须复查一次，因为消息一旦落库就收不回来：

- Run 启动时把当时读到的 `binding.Version` 记进 `base_binding_version`；
- 写入前重读 Run 与 Binding，要求 Run 仍是 `COMMITTING` 且 `binding.Version` 未变；
- 不满足就把 Run 立刻退回队列（`FAILED_RETRYABLE`，`history_invalidated`），不写消息。

这里比的是 **binding 版本而不是 generation**：同一个 `DIRTY(G)` 有两种含义——"本轮执行期间被置脏"（必须拦）
和"本轮就是从 DIRTY 重建的"（必须放行并晋升）。只比 generation 会把后者也误伤，让会话永远停在 DIRTY。

这是 best-effort 而非原子保证：检查与写入之间有毫秒级窗口。窗口内消息可能发出，但
`commitAgentSessionBinding` 会因版本不一致拒绝晋升（`committed_generation = 0`，快照保持 `DIRTY`），
基线不会被污染，下一轮从有效历史重建。
重建使用有界历史窗口，不承诺无限保存模型逐字记忆；IM 聊天记录仍正常持久化。

## 最小隔离

- 保留现有 IM 登录、租户与会话成员校验；不引入 Agent Role、Scope 或审批。
- Bot 通过独立工作负载签名调用 Logic，只允许读 AI 私聊历史、提交 Bot 消息。
- Logic 校验 tenant、AI 类型、两个成员与 Bot 身份；用户不能通过普通建聊接口加入 Bot。
- Pi 禁用内建工具、扩展发现、Skills、上下文文件及项目配置；只加载仓库固定的模型扩展。
- Pi 环境只包含模型凭证等白名单，不含数据库或服务签名密钥。
- 单个 Pilot 容器内运行 Go + Pi。子进程不是强安全沙箱，当前不执行不可信代码或业务工具。

## 明确删除

管理员 Bot、IAM 查询/Mutation、审批协议/页面/状态机、Tool Broker、Capability、租户预算账本、多 Profile 部署、远程 Runtime/Relay、自研出口代理和 RC2 专用发布证据流程。
当前限额只包含并发、运行超时、输入/输出和快照大小；费用以 Provider 账单为准。

运行契约见 [15-agent-harness.md](15-agent-harness.md)，部署与全新数据初始化见 [16-agent-operations.md](16-agent-operations.md)。
