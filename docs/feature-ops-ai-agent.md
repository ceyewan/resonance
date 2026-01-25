# Ops AI Agent 实施计划

> 为 Resonance IM 系统引入 AI 运维助手，用户通过与 Bot 聊天来获取系统信息、执行运维操作。

## 元信息

- **分支**: `feature/ops-ai-agent`
- **状态**: 规划中
- **创建时间**: 2025-01-25

---

## 一、架构设计

### 1.1 整体架构

```
┌─────────┐     ┌─────────┐     ┌─────────┐     ┌─────────────┐
│   Web   │ ──→ │ Gateway │ ──→ │  Logic  │ ──→ │    MySQL     │
│         │ WS  │         │ gRPC │         │     │
└─────────┘     └─────────┘     └────┬────┘     └─────────────┘
                                    │
                                    ▼
                             ┌─────────────┐
                             │     NATS    │
                             └──────┬──────┘
                                    │
                    ┌───────────────┼───────────────┐
                    │               │               │
                    ▼               ▼               ▼
            ┌───────────┐   ┌───────────┐   ┌───────────┐
            │   Task    │   │Bot Service│   │  (扩展)   │
            │  Pusher   │   │  AI Agent  │   │           │
            └───────────┘   └─────┬─────┘   └───────────┘
                                │
                                ▼
                         ┌──────────────┐
                         │ LLM (Claude) │
                         │   + MCP Tools │
                         └──────────────┘
```

### 1.2 核心设计原则

| 原则 | 说明 |
|------|------|
| **Bot 即用户** | Bot 被视为一个特殊的系统用户，拥有自己的 username |
| **消息复用** | Bot 对话消息存储在同一个 `t_message_content` 表 |
| **路由分流** | Logic 服务识别 Bot 消息，路由到专门的 MQ Topic |
| **异步解耦** | LLM 响应慢，通过 MQ 异步处理，不阻塞核心链路 |
| **推送复用** | Bot 回复走现有的 `Task -> Gateway -> WS` 推送链路 |

---

## 二、数据模型设计

### 2.1 Bot 用户

```sql
-- t_user 表中添加 Bot 用户
INSERT INTO t_user (username, nickname, is_bot, created_at)
VALUES ('ops-bot', '运维助手', 1, NOW());
```

### 2.2 会话类型

| Type | 名称 | 说明 |
|------|------|------|
| 1 | 单聊 | 用户与用户 |
| 2 | 群聊 | 多人群组 |
| 3 | Bot 会话 | 用户与 Bot（可选扩展） |

**当前方案**：Bot 会话复用单聊类型（Type=1），Bot 作为一个特殊用户。

### 2.3 MQ 消息

**现有 PushEvent**（复用）：
```protobuf
message PushEvent {
  int64  msg_id        = 1;
  int64  seq_id        = 2;
  string session_id    = 3;
  string from_username = 4;  // 用户名
  string to_username   = 5;  // 接收者（可以是 Bot）
  string content       = 6;
  string type          = 7;
  int64  timestamp     = 8;
  string session_name  = 9;
  int32  session_type  = 10;
}
```

**新增 BotEvent**（Bot 专用）：
```protobuf
// api/proto/mq/v1/bot.proto

syntax = "proto3";
package resonance.mq.v1;

import "common/v1/options.proto";

message BotEvent {
  option (resonance.common.v1.default_topic) = "resonance.bot.event.v1";

  int64  msg_id        = 1;
  string session_id    = 2;
  string from_username = 3;  // 发起提问的用户
  string content       = 4;  // 用户问题
  int64  timestamp     = 5;
  string message_type  = 6;  // 消息类型：text, command 等
}
```

---

## 三、服务设计

### 3.1 Bot Service 架构

```
bot/
├── main.go                 # 服务入口
├── config/                 # 配置管理
├── consumer/               # MQ 消费者
│   └── bot.go            # BotEvent 消费者
├── agent/                  # AI Agent 核心
│   ├── agent.go          # Agent 协调器
│   ├── llm/              # LLM 调用（Claude SDK）
│   ├── mcp/              # MCP 工具调用
│   │   ├── registry.go  # 工具注册表
│   │   ├── tools/        # 工具实现
│   │   │   ├── get_logs.go      # 获取日志
│   │   │   ├── get_metrics.go   # 获取指标
│   │   │   ├── restart.go      # 重启服务
│   │   │   └── ...
│   │   └── client.go      # MCP 客户端
│   └── context.go        # 对话上下文管理
└── logic/                  # Logic 客户端
    └── client.go         # gRPC 客户端封装
```

### 3.2 关键组件

#### Agent 协调器

```go
// bot/agent/agent.go

type Agent struct {
    llm      LLMClient
    mcp      *MCPRegistry
    context  *ConversationContext
    logic    LogicClient
}

func (a *Agent) ProcessUserMessage(ctx context.Context, msg *BotEvent) (string, error) {
    // 1. 更新对话上下文
    a.context.AddMessage("user", msg.Content)

    // 2. 检查是否需要调用工具
    if tools := a.mcp.MatchTools(msg.Content); len(tools) > 0 {
        // 工具调用模式
        return a.executeTools(ctx, tools)
    }

    // 3. 普通对话模式
    return a.llm.Chat(a.context.GetMessages())
}
```

#### MCP 工具注册表

```go
// bot/agent/mcp/registry.go

type MCPRegistry struct {
    tools map[string]*MCPTool
}

type MCPTool struct {
    Name        string
    Description string
    Handler     func(ctx context.Context, params map[string]interface{}) (string, error)
    Parameters  []Parameter
}

// 预定义工具
var defaultTools = []*MCPTool{
    {
        Name: "get_logs",
        Description: "获取服务日志",
        Handler:     getLogsHandler,
    },
    {
        Name: "get_metrics",
        Description: "获取 Prometheus 指标",
        Handler:     getMetricsHandler,
    },
    {
        Name: "list_pods",
        Description: "列出 K8s Pods 状态",
        Handler:     listPodsHandler,
    },
    // ...
}
```

#### 对话上下文管理

```go
// bot/agent/context.go

type ConversationContext struct {
    mu       sync.Mutex
    sessionID string
    messages  []*Message
    createdAt time.Time
}

type Message struct {
    Role    string // "user" 或 "assistant"
    Content string
    Timestamp time.Time
}

// 保留最近 N 条消息作为上下文
const ContextWindow = 10
```

---

## 四、交互流程

### 4.1 用户提问流程

```
1. 用户发送消息: "帮我查看 Gateway 的日志"
   Web ──WS──> Gateway ──gRPC──> Logic

2. Logic 处理消息
   - 落库（t_message_content）
   - 检查 to_username == "ops-bot"
   - 发送 BotEvent 到 MQ (resonance.bot.event.v1)

3. Bot Service 消费
   - 订阅 resonance.bot.event.v1
   - 调用 LLM 分析意图
   - 识别需要调用 get_logs 工具

4. MCP 工具调用
   - Bot Service 调用 Logic/Admin API（或直接调用 K8s API）
   - 获取 Gateway 日志

5. LLM 生成回复
   - 将工具结果作为上下文
   - 生成自然语言回复

6. 发送回复
   - Bot Service 调用 Logic.SendMessage()
   - sender: "ops-bot", receiver: 用户
   - 走正常推送链路
```

### 4.2 时序图

```
User    Gateway    Logic    MQ    BotService    LLM/MCP
  |        |          |        |          |          |
  |--msg-->|          |        |          |          |
  |        |--RPC---->|        |          |          |
  |        |          |---+   BotEvent  |          |
  |        |          |   |              |          |
  |        |          |   +------------>|          |          |
  |        |          |        |          |--分析意图->|
  |        |          |        |          |<--需要工具--|
  |        |          |        |          |--MCP调用-->|
  |        |          |        |          |<--返回结果--|
  |        |          |        |          |--生成回复--|
  |        |          |        |          |          |  (异步)
  |        |          |<--RPC 回复---------|          |
  |        |<--WS----------------------------------|
  |        |          |        |          |          |
```

---

## 五、实施步骤

### Phase 1: 基础设施

- [ ] **步骤 1.1**: 创建 Bot 消息 Proto 定义
  - 文件：`api/proto/mq/v1/bot.proto`
  - 定义 `BotEvent` 消息结构
  - 运行 `make gen`

- [ ] **步骤 1.2**: 创建 Bot 用户
  - 数据库插入 `ops-bot` 用户
  - 或者在代码中自动初始化

- [ ] **步骤 1.3**: 创建 Bot Service 项目结构
  - 目录：`bot/`
  - 复用 `logic` 的项目结构

### Phase 2: Logic 改造

- [ ] **步骤 2.1**: 添加 Bot 路由逻辑
  - 文件：`logic/service/chat.go`
  - 在 MQ 发布前检查 `to_username`
  - 如果是 Bot，发送 `BotEvent` 到 `resonance.bot.event.v1`

```go
// 伪代码：Logic 路由判断
if isBotUser(req.ToUsername) {
    // 发送 BotEvent
    botEvent := &mqv1.BotEvent{
        MsgId:        msgID,
        SessionId:    req.SessionId,
        FromUsername: req.FromUsername,
        Content:      req.Content,
        Timestamp:    time.Now().Unix(),
    }
    publishBotEvent(botEvent)
    // 不走普通推送流程
    return &logicv1.SendMessageResponse{...}
}
// 否则走原有流程
```

### Phase 3: Bot Service 实现

- [ ] **步骤 3.1**: 实现 MQ 消费者
  - 文件：`bot/consumer/bot.go`
  - 订阅 `resonance.bot.event.v1`
  - 调用 Agent 处理消息

- [ ] **步骤 3.2**: 实现 Agent 核心逻辑
  - 文件：`bot/agent/agent.go`
  - LLM 调用
  - 工具路由
  - 上下文管理

- [ ] **步骤 3.3**: 实现 MCP 工具框架
  - 文件：`bot/agent/mcp/registry.go`
  - 工具注册表
  - 工具调用执行

- [ ] **步骤 3.4**: 实现基础工具
  - `get_logs`: 获取服务日志
  - `get_metrics`: 获取 Prometheus 指标
  - `list_pods`: K8s Pod 状态

### Phase 4: LLM 集成

- [ ] **步骤 4.1**: 选择 LLM SDK
  - 选项：Anthropic Claude SDK / OpenAI SDK
  - 配置 API Key

- [ ] **步骤 4.2**: 实现 LLM 客户端
  - 文件：`bot/agent/llm/client.go`
  - 封装 Chat / Messages API

### Phase 5: 测试与部署

- [ ] **步骤 5.1**: 单元测试
- [ ] **步骤 5.2**: 集成测试
- [ ] **步骤 5.3**: Docker 部署

---

## 六、关键文件清单

### 需要修改的文件

| 文件 | 修改内容 |
|------|----------|
| `api/proto/mq/v1/bot.proto` | 新增 BotEvent 定义 |
| `logic/service/chat.go` | 添加 Bot 消息路由逻辑 |
| `main.go` | 添加 Bot 启动支持 |

### 需要新建的文件

| 文件 | 说明 |
|------|------|
| `bot/main.go` | Bot Service 入口 |
| `bot/config/config.go` | Bot Service 配置 |
| `bot/consumer/bot.go` | BotEvent 消费者 |
| `bot/agent/agent.go` | Agent 协调器 |
| `bot/agent/llm/client.go` | LLM 客户端 |
| `bot/agent/mcp/registry.go` | MCP 工具注册表 |
| `bot/agent/mcp/tools/*.go` | 工具实现 |
| `bot/logic/client.go` | Logic gRPC 客户端 |
| `docker-compose.bot.yml` | Bot Service 部署配置 |

---

## 七、技术选型

| 组件 | 选型 | 说明 |
|------|------|------|
| LLM Provider | Anthropic Claude | 支持 MCP，工具调用能力强 |
| MCP SDK | anthropic-experimental-go | 官方 MCP SDK |
| 框架 | 复用 Genesis + Logic/Task 结构 | 保持一致性 |

---

## 八、风险与缓解

| 风险 | 缓解措施 |
|------|----------|
| LLM 响应慢 | MQ 异步处理，用户发送成功后立即返回 |
| LLM 调用失败 | 返回友好错误提示，记录日志 |
| 工具执行权限 | 限制 MCP 工具只读操作，或需严格鉴权 |
| 对话上下文泄露 | 不记录敏感信息到 LLM，或使用本地部署的 LLM |
