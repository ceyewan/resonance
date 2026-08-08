# 契约设计：Proto、事件模型与边界约束

> 本文档描述 Resonance 的协议分层、统一事件模型以及身份与错误处理约束。阅读完本文后，应该能回答三个问题：系统的 proto 为什么要按 `common / gateway / logic / mq` 分层；`ChatEvent` 为什么是整个会话域的核心抽象；以及 Web、Gateway、Logic、Task 在协议边界上分别交换什么数据。

---

## 1. 协议设计目标

Resonance 的协议设计不是从“接口数量”出发，而是从“系统到底要稳定传递什么对象”出发。对于一个 IM 系统来说，真正稳定的不是某条具体 RPC，而是会话中发生的变化本身。消息发送、消息撤回、消息编辑、已读位点推进、会话信息更新，这些变化都会穿过接入层、业务层、异步层，最终到达客户端。如果每一层都为这些变化设计自己的一套结构，系统很快就会出现协议重复定义、字段语义不一致、扩展成本越来越高的问题。

因此，Resonance 在协议设计上追求两件事。第一件事是把真正跨层稳定的数据结构提炼出来，放在统一的共享层。第二件事是让每一层只定义自己边界内真正需要的协议，而不是互相复用不该复用的入口结构。这样，协议分层本身就会成为架构边界的一部分，而不是随着实现演进不断退化成互相引用的杂糅集合。

---

## 2. Proto 分层

当前仓库的协议定义位于 `api/proto/` 下，整体分成 `common/v1`、`gateway/v1`、`logic/v1` 和 `mq/v1` 四层。这四层并不是按服务目录机械映射出来的，而是按“谁与谁通信、要解决什么问题”划分出来的。

### 2.1 协议分层图

```text
common/v1
  ├── 提供跨层共享模型
  │
  ├── gateway/v1   —— Web 与 Gateway 的边界协议
  ├── logic/v1     —— Gateway 与 Logic 的内部服务协议
  └── mq/v1        —— Logic 与 Task 的异步事件信封
```

### 2.2 各层职责

| 分层 | 作用 | 典型内容 |
| ---- | ---- | -------- |
| `common/v1` | 存放跨层共享的数据结构 | `ChatEvent`、消息模型、会话模型、公共类型 |
| `gateway/v1` | 定义客户端可见协议与 Gateway 内部 Push 接口 | Auth、Session、WS 包、PushService |
| `logic/v1` | 定义 Gateway 到 Logic 的内部业务服务 | AuthService、SessionService、ChatService、PresenceService |
| `mq/v1` | 定义 Logic 进入异步链路时的事件信封 | `MQEvent` |

这套分层背后的基本规则是：共享模型放在 `common`，边界协议各自留在各自的边界层，上层可以依赖共享模型，但不应该反向依赖别的入口层协议。这样做的目的，是避免 Gateway 的外部协议细节反过来污染 Logic 的内部服务定义，也避免 MQ 层自己再发明一套和业务对象脱节的消息结构。

---

## 3. 核心抽象：ChatEvent

在当前协议设计里，`ChatEvent` 是最重要的统一对象。它定义在 `api/proto/common/v1/event.proto` 中，代表“会话中发生的一次用户可感知变化”。Resonance 后续继续扩展时，协议能否稳定演进，关键就在于这一个对象是否足够稳定、是否足够通用。

### 3.1 ChatEvent 结构

```text
ChatEvent
├── event_id
├── seq_id
├── session_id
├── from_username
├── timestamp_ms
└── oneof payload
    ├── message
    ├── recall
    ├── edit
    ├── read_receipt
    └── session_update
```

### 3.2 为什么统一成事件

统一成 `ChatEvent` 之后，系统在多个位置都能沿用同一套表达。Logic 生成的是 `ChatEvent`，`mq/v1.MQEvent` 里携带的是 `ChatEvent`，Task 写入 Inbox 的是 `ChatEvent`，Gateway 最终推给 Web 的也应该是 `ChatEvent`。这意味着系统以后增加新能力时，不需要重复设计新的 MQ 消息结构、新的推送包和新的增量同步结构，只需要在 `payload` 上增加新的分支，并补齐对应的业务处理和客户端渲染逻辑。

### 3.3 当前实现状态

这里需要明确区分协议支持范围与真实业务落地范围。当前 `ChatEvent` 在 proto 层已经支持 `message`、`recall`、`edit`、`read_receipt` 和 `session_update`。但在当前代码里，`logic/service/chat.go` 真正完整打通的主要还是 `message`。也就是说，统一事件骨架已经建立，消息主链路已经成立，但其他事件类型仍主要处于协议预留和分发框架已就位的阶段。文档在这里必须明确这个差异，避免读者把“proto 已定义”误解成“业务已完整上线”。

---

## 4. 各层协议边界

理解 Resonance 的协议，关键不是逐个看 proto 文件，而是要知道每一层协议到底想解决什么问题。

### 4.1 Web 与 Gateway

Web 与 Gateway 的边界主要由 `gateway/v1` 定义。这里包含对外的认证和会话相关接口，也包含 WebSocket 使用的包结构。对客户端来说，它并不需要知道 Outbox 是什么，也不应该直接面对 MQ 信封或内部服务协议。客户端关心的是登录、查询会话、发送请求、接收下行事件，以及如何通过 WebSocket 维持实时交互。

这个边界层的重点不是“把内部细节暴露给前端”，而是把统一事件模型包装成适合浏览器消费的形式。客户端面对的是 Gateway，而不是 Logic 或 Task，因此这里的协议必须优先服务用户界面与终端交互。

### 4.2 Gateway 与 Logic

Gateway 到 Logic 的边界由 `logic/v1` 定义。这里包含 Auth、Session、Chat 和 Presence 等内部服务。对 Gateway 来说，这些内部服务就是唯一应该依赖的业务入口。Gateway 不应该绕过这些协议自己猜测数据库结构，也不应该直接操作底层资源。

其中最关键的服务是 `ChatService.SendEvent`。虽然当前只有消息类 payload 已完整打通，但这个接口的意义已经非常清楚：它代表系统希望逐步把会话内动作收敛到统一事件入口，而不是随着功能增加不断新增彼此割裂的消息接口、撤回接口、编辑接口和同步接口。统一事件入口是架构方向，message 只是当前第一条完整落地的路径。

### 4.3 Logic 与 Task

Logic 与 Task 之间的边界由 `mq/v1/event.proto` 定义。这里的 `MQEvent` 不是新的业务模型，而是 `ChatEvent` 的异步传输信封。它包含三部分：事件本体、目标用户名列表和 trace 头。事件本体描述发生了什么，目标用户名列表描述要扩散给谁，trace 头描述这次调用如何在跨服务链路中被关联起来。

这个设计的关键在于：异步层不重新定义业务语义，而只在统一事件模型之外增加“送达所需的最小附加信息”。这样 Task 消费到的仍然是完整的业务事件，而不是一套被压扁到只剩投递信息的消息格式。

---

## 5. 身份传递约定

AI 会话必须通过 `CreateAgentSession` 显式创建。请求体只有
`AgentProfile` 枚举（`USER_ASSISTANT` 或 `IAM_ADMIN`），不包含
`tenant_id`、Bot 用户名、Role、Scope 或 Profile Version。Logic 从已验签并完成
IAM 回查的 `UserPrincipal` 取得租户与当前授权，再写入服务端配置固定的 Profile
ID/Version。普通 `CreateSession` 只接受真人成员，不能用 Agent Bot 绕过这条入口。

在身份设计上，系统坚持一个简单但重要的约束：业务 body 不能决定 Actor。对外层面，Web 到 Gateway 使用 `Authorization: Bearer <token>` 完成认证；在 WebSocket 握手场景下，也兼容查询参数中的 token。Gateway 本地验 JWT 后不向下游传播原始 token，而是传播由 Gateway 工作负载密钥签名的最小 Principal。

### 5.1 身份传递链路

```text
Web ── Authorization: Bearer <token> ──▶ Gateway
Gateway ── payload-bound serviceauth(tenant, actor, member_version) ──▶ Logic
Logic ── 验签、防重放、IAM Repo 回查 ──▶ context UserPrincipal ──▶ 业务服务
```

### 5.2 为什么这样设计

这样设计有两个直接好处。第一，业务请求体本身会更干净，只保留会话 ID、消息内容、目标对象等业务字段，不需要混入 `access_token` 或 `username`。第二，身份来源保持唯一：接入层负责判断“你是谁”，业务层负责判断“你是否有权限做这件事”。如果身份同时出现在 Header、metadata 和 body 里，最终只会让协议越来越混乱。

---

## 6. 错误处理约定

Resonance 在内部服务边界上使用 gRPC status 表达错误，而不是在每个响应 message 里再塞一个自由文本 `error` 字段。这样 Gateway 和客户端面对的是结构化的错误码与语义，而不是额外约定的一层字符串协议。

### 6.1 约定

- 失败通过 `status.Errorf(codes.X, ...)` 返回
- 成功响应不额外携带错误字段
- Gateway 和客户端按标准 gRPC / ConnectRPC 错误语义处理失败

这种方式的价值在于，它让错误表达保持和底层框架一致，避免协议本身被非结构化字段污染，也减少了前后端各自再定义一套错误包装规则的需求。

---

## 7. 演进方向

协议层当前最重要的价值，不是“已经定义了多少类型”，而是它已经把未来扩展限制在一个稳定范围内。只要继续坚持 `ChatEvent + oneof payload` 这套模型，未来接入撤回、编辑、已读同步、群成员变化甚至 reaction，主要变化都应落在 payload 分支和对应处理逻辑上，而不需要重新设计新的推送协议、新的 MQ 结构和新的增量同步接口。

这意味着协议层真正承担的是“稳定骨架”的角色。它不会替业务实现一切，但它决定了后续业务接入时，不需要反复拆骨重来。

---

## 8. 阅读建议

本文档描述的是协议层的整体边界与核心模型。继续往下看时，建议按下面的顺序阅读：

| 文档 | 内容 |
| ---- | ---- |
| `11-logic.md` | 统一事件入口如何在 Logic 中落地 |
| `12-task.md` | 统一事件如何在异步层扩散和推送 |
| `20-message-flow.md` | message 主链路的详细时序 |
| `21-write-fanout.md` | 统一事件进入 Inbox 后的写扩散模型 |

---

## 9. 小结

如果用一句话概括当前的协议设计，那么 Resonance 采用了一套以 `ChatEvent` 为中心的分层 proto 体系：`common` 负责共享模型，`gateway` 负责客户端边界，`logic` 负责业务服务边界，`mq` 负责异步信封。它真正重要的地方不在于文件怎么分，而在于整个系统终于拥有了一套可以同时承载消息、撤回、编辑、已读和会话更新的统一骨架。
