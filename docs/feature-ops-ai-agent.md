# Ops AI Agent 实施计划 (v2)

> 为 Resonance IM 系统引入 AI 运维助手，用户通过与 Bot 聊天来获取系统信息、执行运维操作。

## 元信息

- **分支**: `feature/ops-ai-agent`
- **状态**: 规划中
- **创建时间**: 2025-01-25
- **更新时间**: 2025-01-25

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
                                │   │
                                │   ▼
                                │ ┌─────────┐
                                │ │  Redis  │ (State/Context/Memory)
                                │ └─────────┘
                                ▼
                         ┌──────────────┐
                         │ LLM (Claude) │
                         │   + MCP Tools │
                         └──────────────┘
```

### 1.2 核心设计原则

| 原则 | 说明 |
|------|------|
| **内置体验** | Bot 作为基础设施内置，用户注册即自动添加好友，零门槛使用 |
| **权限隔离** | 通过用户 Role 区分权限，仅 Admin 可挂载敏感运维工具，普通用户仅限闲聊 |
| **确定性交互** | 敏感操作（HITL）通过**结构化卡片 + ActionID** 确认，**完全绕过 LLM 推理** |
| **配置覆盖** | 支持用户自带 Key (BYOK) 和模型偏好，覆盖系统默认配置 |
| **路由分流** | Logic 服务识别 Bot 消息，路由到专门的 MQ Topic |
| **状态驱动** | 基于状态机管理会话生命周期，支持异步工具执行和状态恢复 |
| **可观测性** | 全链路追踪 LLM 调用、工具执行、状态转换，支持成本分析和质量监控 |

---

## 二、数据模型设计

### 2.1 用户体系与权限

**1. Bot 账号 (Built-in)**
在系统初始化时创建 Bot 用户，标记为 `is_bot=1`，用于接收和发送消息。

**2. 用户表改造 (Schema Change)**
需在 `t_user` 表中添加角色字段，用于权限隔离。
```sql
ALTER TABLE t_user ADD COLUMN role VARCHAR(32) DEFAULT 'user' COMMENT 'user/admin';
```

**3. 用户 Bot 配置 (New Table)**
用于存储用户的个性化设置和 BYOK 密钥。
```sql
CREATE TABLE user_bot_settings (
    user_id VARCHAR(64) PRIMARY KEY,
    provider VARCHAR(32) DEFAULT 'system', -- 'openai', 'anthropic', 'system'
    model_name VARCHAR(64) DEFAULT 'gpt-3.5-turbo',
    api_key VARCHAR(255), -- AES 加密存储
    api_endpoint VARCHAR(255),
    system_prompt TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);
```

### 2.2 Redis 状态分层设计

#### L1: 会话状态 (Session State)
**用途**: 管理会话的生命周期状态机，支持状态转换和异常恢复。

**数据结构**:
- Key: `bot:session:{session_id}:state`
- Type: Hash
- Fields: `state`, `user_id`, `current_intent`, `last_tool_call`, `retry_count`, `created_at`, `updated_at`
- TTL: 1h (活跃会话), 24h (非活跃会话)

**状态机定义**:
- `idle`: 空闲状态，等待用户输入
- `processing`: LLM 推理中
- `tool_executing`: 工具执行中（支持异步工具）
- `waiting_hitl`: 等待人工确认（HITL 场景）
- `error`: 错误状态，需要人工介入或自动恢复

**状态转换规则**:
- `idle` → `processing`: 收到用户消息
- `processing` → `tool_executing`: LLM 决定调用工具
- `processing` → `waiting_hitl`: 敏感操作需要确认
- `tool_executing` → `processing`: 工具执行完成，继续推理
- `waiting_hitl` → `tool_executing`: 用户确认执行
- 任意状态 → `error`: 发生不可恢复错误
- `error` → `idle`: 错误恢复或超时重置

#### L2: 对话上下文 (Conversation Context)
**用途**: 存储多轮对话历史，支持 Sliding Window 和定期摘要压缩。

**数据结构**:
- Key: `bot:context:{session_id}:messages`
- Type: List (JSON)
- Value: `[{"role": "user", "content": "...", "timestamp": 123}, ...]`
- TTL: 24h
- 策略: 保留最近 20 轮对话，超出部分触发摘要压缩

**上下文摘要**:
- Key: `bot:context:{session_id}:summary`
- Type: String
- Value: "用户正在排查 Gateway 连接问题，已查看日志，发现端口冲突"
- 触发条件: 对话轮次超过 10 轮时，调用 LLM 生成摘要

#### L3: 工具执行状态 (Tool Execution State)
**用途**: 追踪异步工具的执行状态，支持超时检测和重试。

**数据结构**:
- Key: `bot:tool:{execution_id}`
- Type: Hash
- Fields: `tool_name`, `status`, `params`, `result`, `started_at`, `retry_count`
- TTL: 10m
- Status: `pending`, `running`, `success`, `failed`, `timeout`

**应用场景**:
- 长时间运行的工具（如部署、重启服务）
- 需要轮询结果的异步操作
- 支持工具执行失败后的重试逻辑

#### L4: 用户记忆 (User Memory)
**用途**: 存储用户的长期偏好和事实记忆，提升个性化体验。

**用户画像**:
- Key: `bot:user:{user_id}:profile`
- Type: Hash
- Fields: `preferred_model`, `timezone`, `language`, `notification_preference`
- TTL: 30d

**事实记忆**:
- Key: `bot:user:{user_id}:facts`
- Type: Set
- Value: `["负责 Gateway 运维", "偏好详细日志", "工作时间 9:00-18:00"]`
- TTL: 30d
- 更新策略: LLM 从对话中提取关键事实，定期更新

#### L5: 挂起动作 (Pending Action - HITL)
**用途**: 存储等待用户确认的敏感操作，支持二次确认和超时取消。

**数据结构**:
- Key: `bot:action:{action_id}`
- Type: String (JSON)
- Value: `{"tool": "restart_gateway", "params": {...}, "user_role": "admin", "created_at": 123}`
- TTL: 5m
- 超时策略: 5 分钟未确认自动取消，通知用户

### 2.3 消息协议扩展

**新增消息类型**:
- `interactive`: 确认卡片 (JSON Payload)，包含按钮和 ActionID
- `action_response`: 按钮点击回调，携带 ActionID 和用户选择

---

## 三、服务设计

### 3.1 Bot Service 架构

```
bot/
├── main.go
├── config/                 # 系统配置
│   └── config.yaml       # Agent 配置（重试策略、超时、TTL 等）
├── consumer/               # MQ 消费者
│   └── bot.go            # 消息分流
├── agent/
│   ├── agent.go          # Agent 核心（消息处理入口）
│   ├── orchestrator.go   # Multi-Agent 编排器（预留）
│   ├── config_manager.go # 用户配置加载 (BYOK)
│   ├── state/            # 状态管理
│   │   ├── fsm.go        # 状态机定义与转换规则
│   │   ├── manager.go    # 状态持久化与加载
│   │   └── recovery.go   # 状态恢复（处理僵尸会话）
│   ├── context/          # 上下文管理
│   │   ├── manager.go    # 上下文存储与检索
│   │   └── summarizer.go # 上下文摘要（调用 LLM 压缩）
│   ├── memory/           # 用户记忆
│   │   ├── profile.go    # 用户画像管理
│   │   └── facts.go      # 事实记忆提取与存储
│   ├── executor/         # 工具执行器
│   │   ├── executor.go   # 工具调用与超时控制
│   │   └── async.go      # 异步工具状态追踪
│   ├── llm/              # LLM Client
│   │   ├── factory.go    # Client 工厂（支持 BYOK）
│   │   ├── retry.go      # 重试策略（指数退避）
│   │   └── client.go     # 统一 LLM 调用接口
│   ├── mcp/              # MCP 工具链
│   │   ├── registry.go   # 工具注册与鉴权
│   │   ├── base.go       # 工具基类（定义接口）
│   │   ├── ops/          # 运维工具
│   │   │   ├── restart.go
│   │   │   └── logs.go
│   │   └── analytics/    # 分析工具（预留）
│   └── metrics/          # 可观测性
│       └── metrics.go    # Prometheus 指标定义
└── logic/                  # Logic 客户端（gRPC）
```

### 3.2 关键业务流程

#### A. 注册即好友 (Onboarding)
**流程**:
1. 用户注册成功后，Logic 服务触发 Hook
2. 自动创建用户与 `ops-bot` 的单聊会话（插入 `t_session_member`）
3. Bot Service 发送欢迎消息："你好，我是你的 AI 助手，输入 `/help` 查看可用命令"
4. 初始化用户状态：在 Redis 中创建 `bot:session:{session_id}:state`，状态为 `idle`

**实现位置**: `logic/service/auth.go` 的 `Register` 方法中添加 Hook

#### B. 消息处理主流程
**流程**:
1. **接收消息**: Consumer 从 NATS 订阅 `resonance.bot.event.v1` Topic
2. **加载状态**: 从 Redis 加载会话状态，检查当前状态是否允许处理新消息
3. **状态转换**: `idle` → `processing`，更新 Redis
4. **加载上下文**: 从 Redis 获取最近 20 轮对话历史 + 上下文摘要
5. **加载用户记忆**: 获取用户画像和事实记忆，注入到 Prompt
6. **LLM 调用**: 根据用户配置选择 LLM Client（BYOK 或系统默认），带重试机制
7. **意图识别**: 解析 LLM 响应，判断是否需要调用工具
8. **工具执行**:
   - 如果是敏感工具（需要 Admin 权限），状态转换为 `waiting_hitl`，发送确认卡片
   - 如果是普通工具，状态转换为 `tool_executing`，执行工具
9. **结果处理**: 工具执行完成后，状态转换回 `processing`，将结果反馈给 LLM
10. **生成回复**: LLM 生成最终回复，状态转换为 `idle`
11. **更新上下文**: 将本轮对话追加到 Redis，检查是否需要触发摘要

**异常处理**:
- LLM 调用失败：重试 3 次（指数退避），失败后状态转换为 `error`
- 工具执行超时：超过 30s 自动取消，状态转换为 `error`
- 状态不一致：定期扫描僵尸会话（状态为 `processing` 但超过 5 分钟未更新），自动重置为 `idle`

#### C. 配置加载与 Client 初始化
**BYOK 模式**:
1. 用户通过 `/key <provider>` 命令设置 API Key
2. Key 使用 AES-GCM 加密后存储到 MySQL `user_bot_settings` 表
3. Agent 处理消息时，优先从 Redis 缓存读取用户配置（Cache-Aside 模式）
4. 如果用户配置了 BYOK，解密 Key 后创建专属 LLM Client
5. 否则使用系统默认 Client（共享连接池）

**配置优先级**: 用户 BYOK > 系统默认配置

#### D. 工具鉴权 (Permission Check)
**鉴权流程**:
1. Agent 根据用户 Role 从 Registry 获取可用工具列表
2. 普通用户仅能访问基础工具（闲聊、搜索文档）
3. Admin 用户可访问所有工具（包括重启服务、查看日志、管理 Pod）
4. 工具执行前二次校验：检查 Redis 中存储的 `user_role` 字段

**工具定义**:
- 每个工具实现 `ToolPlugin` 接口，声明 `RequiredRole()` 方法
- Registry 在注册时自动构建 Role → Tools 的映射表
- 支持动态加载工具（通过配置文件或插件机制）

#### E. HITL 确认流程
**流程**:
1. LLM 决定调用敏感工具（如 `restart_gateway`）
2. Agent 生成唯一 ActionID，将工具调用参数存储到 Redis `bot:action:{action_id}`
3. 状态转换为 `waiting_hitl`
4. 发送交互卡片到前端，包含"确认"和"取消"按钮
5. 用户点击按钮后，前端发送 `action_response` 消息
6. Agent 从 Redis 读取 ActionID 对应的工具调用参数
7. **完全绕过 LLM**，直接执行工具
8. 执行完成后，状态转换为 `idle`，发送执行结果

**超时处理**: 5 分钟未确认，自动删除 Redis 中的 Action，状态重置为 `idle`

#### F. 状态恢复机制
**背景**: 防止因服务重启、网络异常导致的僵尸会话（状态卡在 `processing` 或 `tool_executing`）

**恢复策略**:
1. 启动定时任务（每 5 分钟执行一次）
2. 扫描 Redis 中所有会话状态，筛选出中间状态（`processing`, `tool_executing`）
3. 检查 `updated_at` 字段，超过 5 分钟未更新的会话视为僵尸会话
4. 自动重置为 `idle` 状态，清空 `retry_count`
5. 记录日志和 Metrics，便于排查问题

**实现位置**: `bot/agent/state/recovery.go`

---

## 四、容错与可靠性设计

### 4.1 LLM 调用重试策略

**重试配置**:
- 最大重试次数: 3 次
- 初始退避时间: 1s
- 最大退避时间: 10s
- 退避倍数: 2.0（指数退避）

**可重试错误**:
- 5xx 服务器错误
- 超时错误
- 限流错误（429 Too Many Requests）

**不可重试错误**:
- 4xx 客户端错误（除 429 外）
- 认证失败
- 配额耗尽

**实现位置**: `bot/agent/llm/retry.go`

### 4.2 工具执行超时控制

**超时策略**:
- 默认超时: 30s
- 工具级超时: 每个工具可自定义超时时间（如部署工具 5 分钟）
- 超时后自动取消执行，状态转换为 `error`

**异步工具支持**:
- 长时间运行的工具（如部署）返回 `execution_id`
- Agent 定期轮询工具状态（从 Redis `bot:tool:{execution_id}` 读取）
- 支持用户主动查询执行进度

**实现位置**: `bot/agent/executor/executor.go`, `bot/agent/executor/async.go`

### 4.3 状态不一致恢复

**问题场景**:
- Bot Service 重启导致内存状态丢失
- Redis 连接中断导致状态更新失败
- 工具执行过程中服务崩溃

**恢复机制**:
- 定时任务扫描僵尸会话（每 5 分钟）
- 自动重置超时会话状态
- 记录恢复事件到 Metrics，便于监控

**实现位置**: `bot/agent/state/recovery.go`

---

## 五、可观测性设计

### 5.1 Metrics 指标体系

#### LLM 调用指标
- `bot_llm_requests_total`: LLM 请求总数（标签: provider, model, status）
- `bot_llm_request_duration_seconds`: LLM 请求耗时分布（标签: provider, model）
- `bot_llm_tokens_used_total`: Token 消耗总数（标签: provider, model, type=input/output）
- `bot_llm_errors_total`: LLM 错误总数（标签: provider, model, error_type）

#### 工具执行指标
- `bot_tool_executions_total`: 工具执行总数（标签: tool_name, status）
- `bot_tool_execution_duration_seconds`: 工具执行耗时分布（标签: tool_name）
- `bot_tool_errors_total`: 工具执行错误总数（标签: tool_name, error_type）

#### 会话指标
- `bot_conversations_total`: 会话总数（标签: user_role, intent）
- `bot_conversation_turns`: 会话轮次分布（标签: user_role）
- `bot_hitl_confirm_rate`: HITL 确认率（标签: action_type）
- `bot_active_sessions`: 当前活跃会话数

#### 状态指标
- `bot_state_transitions_total`: 状态转换总数（标签: from_state, to_state）
- `bot_state_recovery_total`: 状态恢复总数（标签: from_state）

**实现位置**: `bot/agent/metrics/metrics.go`

### 5.2 分布式追踪 (OpenTelemetry)

**追踪范围**:
- 消息处理全流程（从 MQ 消费到回复发送）
- LLM 调用（包括重试）
- 工具执行（包括异步工具）
- 状态转换

**Span 设计**:
- Root Span: `agent.process_message`
- Child Span: `agent.load_state`, `agent.call_llm`, `agent.execute_tool`, `agent.update_context`

**Attributes**:
- `session_id`: 会话 ID
- `user_id`: 用户 ID
- `user_role`: 用户角色
- `state`: 当前状态
- `tool_name`: 工具名称
- `llm_provider`: LLM 提供商
- `llm_model`: LLM 模型

**实现位置**: 在 `bot/agent/agent.go` 的主流程中集成 OpenTelemetry

### 5.3 日志规范

**日志级别**:
- `DEBUG`: 状态转换、上下文加载
- `INFO`: 消息处理开始/结束、工具执行
- `WARN`: LLM 重试、工具超时
- `ERROR`: LLM 调用失败、工具执行失败、状态恢复

**结构化字段**:
- `session_id`: 会话 ID
- `user_id`: 用户 ID
- `msg_id`: 消息 ID
- `state`: 当前状态
- `tool_name`: 工具名称
- `execution_id`: 工具执行 ID

---

## 六、扩展性设计

### 6.1 Multi-Agent 架构预留

**设计思路**:
- 引入 `Orchestrator` 组件，根据意图路由到不同 Agent
- 每个 Agent 专注于特定领域（运维、分析、闲聊）
- Agent 之间通过统一接口通信

**意图识别**:
- 使用轻量级分类模型或规则引擎识别意图
- 支持意图切换（如从闲聊切换到运维）

**实现位置**: `bot/agent/orchestrator.go`（当前预留接口）

### 6.2 工具插件化设计

**插件接口**:
- `Name()`: 工具名称
- `Description()`: 工具描述
- `Schema()`: 参数 Schema（JSON Schema）
- `RequiredRole()`: 所需权限
- `Timeout()`: 超时时间
- `Run(ctx, params)`: 执行逻辑

**动态加载**:
- 支持从配置文件加载工具列表
- 支持运行时注册新工具（通过 gRPC 或 HTTP API）

**实现位置**: `bot/agent/mcp/base.go`, `bot/agent/mcp/registry.go`

---

## 七、配置文件设计

```yaml
# bot/config/config.yaml
bot:
  agent:
    max_context_turns: 20        # 最大上下文轮次
    context_summary_threshold: 10 # 超过 10 轮触发摘要
    state_recovery_interval: 5m   # 状态恢复间隔

  llm:
    default_provider: "openai"
    default_model: "gpt-4-turbo"
    retry:
      max_retries: 3
      initial_backoff: 1s
      max_backoff: 10s
      multiplier: 2.0

  tools:
    default_timeout: 30s
    max_concurrent: 10

  redis:
    state_ttl: 3600              # 会话状态 TTL (1h)
    context_ttl: 86400           # 上下文 TTL (24h)
    action_ttl: 300              # Pending Action TTL (5m)
    memory_ttl: 2592000          # 用户记忆 TTL (30d)

  metrics:
    enabled: true
    port: 9090

  tracing:
    enabled: true
    endpoint: "http://localhost:4318"
```

---

## 八、实施步骤与优先级

### Phase 0: 基础设施准备 (P0 - 必须实现)

#### 步骤 0.1: 数据库 Schema 变更
- [ ] 执行 `ALTER TABLE t_user ADD COLUMN role` 添加角色字段
- [ ] 创建 `user_bot_settings` 表
- [ ] 插入 Bot 用户到 `t_user` 表

#### 步骤 0.2: Protobuf 协议定义
- [ ] 更新 `api/proto/mq/v1/bot.proto`（已存在，检查字段完整性）
- [ ] 添加交互卡片消息类型到 `api/proto/gateway/v1/message.proto`
- [ ] 生成代码：`make gen`

#### 步骤 0.3: 状态机设计与实现
- [ ] 实现 `bot/agent/state/fsm.go`：定义状态机和转换规则
- [ ] 实现 `bot/agent/state/manager.go`：状态持久化到 Redis
- [ ] 实现 `bot/agent/state/recovery.go`：僵尸会话恢复机制
- [ ] 编写单元测试验证状态转换逻辑

**为什么优先**: 状态机是 Agent 系统的核心，所有业务流程都依赖它

### Phase 1: 核心 Agent 实现 (P0 - 必须实现)

#### 步骤 1.1: 上下文管理
- [ ] 实现 `bot/agent/context/manager.go`：上下文存储与检索
- [ ] 实现 Sliding Window 逻辑（保留最近 20 轮）
- [ ] 实现 `bot/agent/context/summarizer.go`：调用 LLM 生成摘要

#### 步骤 1.2: LLM Client 与重试机制
- [ ] 实现 `bot/agent/llm/client.go`：统一 LLM 调用接口
- [ ] 实现 `bot/agent/llm/retry.go`：指数退避重试策略
- [ ] 实现 `bot/agent/llm/factory.go`：支持 BYOK 的 Client 工厂
- [ ] 集成 OpenAI/Anthropic SDK

#### 步骤 1.3: 工具执行器
- [ ] 实现 `bot/agent/mcp/base.go`：定义 `ToolPlugin` 接口
- [ ] 实现 `bot/agent/mcp/registry.go`：工具注册与鉴权
- [ ] 实现 `bot/agent/executor/executor.go`：工具调用与超时控制
- [ ] 实现 2-3 个示例工具（如 `echo`, `get_time`）

#### 步骤 1.4: Agent 核心流程
- [ ] 实现 `bot/agent/agent.go`：消息处理主流程
- [ ] 集成状态机、上下文、LLM、工具执行器
- [ ] 实现 `bot/consumer/bot.go`：MQ 消费者

#### 步骤 1.5: 基础 Metrics
- [ ] 实现 `bot/agent/metrics/metrics.go`：定义 Prometheus 指标
- [ ] 在关键路径埋点（LLM 调用、工具执行、状态转换）
- [ ] 暴露 `/metrics` 端点

**验收标准**: 能够接收用户消息，调用 LLM 生成回复，记录 Metrics

### Phase 2: 权限与 HITL (P1 - 强烈建议)

#### 步骤 2.1: 用户配置管理
- [ ] 实现 `bot/agent/config_manager.go`：加载用户 BYOK 配置
- [ ] 实现 AES-GCM 加密/解密逻辑
- [ ] 实现 Cache-Aside 模式（Redis 缓存）

#### 步骤 2.2: 工具鉴权
- [ ] 在 `ToolPlugin` 接口中添加 `RequiredRole()` 方法
- [ ] 在 Registry 中实现基于 Role 的工具过滤
- [ ] 实现工具执行前的二次校验

#### 步骤 2.3: HITL 交互
- [ ] 实现 ActionID 生成与存储（Redis）
- [ ] 实现交互卡片消息构造
- [ ] 实现 `action_response` 消息处理
- [ ] 实现超时取消逻辑

#### 步骤 2.4: 运维工具实现
- [ ] 实现 `bot/agent/mcp/ops/restart.go`：重启服务工具
- [ ] 实现 `bot/agent/mcp/ops/logs.go`：查看日志工具
- [ ] 集成 Kubernetes Client（如需要）

**验收标准**: Admin 用户可以通过 HITL 确认执行敏感操作

### Phase 3: 高级特性 (P1 - 强烈建议)

#### 步骤 3.1: 用户记忆
- [ ] 实现 `bot/agent/memory/profile.go`：用户画像管理
- [ ] 实现 `bot/agent/memory/facts.go`：事实记忆提取与存储
- [ ] 在 Prompt 中注入用户记忆

#### 步骤 3.2: 异步工具支持
- [ ] 实现 `bot/agent/executor/async.go`：异步工具状态追踪
- [ ] 实现工具执行进度查询接口
- [ ] 实现长时间运行工具（如部署）

#### 步骤 3.3: 分布式追踪
- [ ] 集成 OpenTelemetry SDK
- [ ] 在主流程中创建 Span
- [ ] 配置 Trace Exporter（Jaeger/Zipkin）

**验收标准**: 支持长时间运行的工具，可通过 Trace 查看完整调用链

### Phase 4: 前端与用户体验 (P2 - 可迭代)

#### 步骤 4.1: Slash Command 支持
- [ ] 前端实现命令检测（`/help`, `/key`, `/clear` 等）
- [ ] 后端实现命令处理逻辑
- [ ] 实现命令自动补全

#### 步骤 4.2: 交互卡片渲染
- [ ] 前端实现卡片组件（`BotCard.tsx`）
- [ ] 实现按钮点击回调
- [ ] 实现二次确认弹窗

#### 步骤 4.3: 注册即好友
- [ ] 在 `logic/service/auth.go` 的 `Register` 方法中添加 Hook
- [ ] 自动创建与 Bot 的会话
- [ ] 发送欢迎消息

**验收标准**: 用户注册后自动添加 Bot 好友，可通过 Slash Command 配置

### Phase 5: 扩展性与优化 (P2 - 可迭代)

#### 步骤 5.1: Multi-Agent 编排
- [ ] 实现 `bot/agent/orchestrator.go`：意图路由
- [ ] 实现多个专用 Agent（运维、分析、闲聊）
- [ ] 实现 Agent 间通信协议

#### 步骤 5.2: 工具插件化
- [ ] 支持从配置文件加载工具
- [ ] 实现工具热加载（无需重启服务）
- [ ] 提供工具开发文档和示例

#### 步骤 5.3: 性能优化
- [ ] LLM 响应流式传输（SSE）
- [ ] 上下文压缩优化（减少 Token 消耗）
- [ ] Redis 连接池优化

**验收标准**: 支持动态加载工具，响应时间 < 2s

---

## 九、关键设计决策总结

| 设计点 | 方案 | 理由 |
|--------|------|------|
| **状态存储** | Redis Hash + TTL 分层 | 支持细粒度状态查询，避免全量序列化 |
| **上下文管理** | Sliding Window + 定期摘要 | 控制 Token 成本，保留关键信息 |
| **工具执行** | 同步 + 异步状态追踪 | 支持长时间运行的工具（如部署） |
| **容错策略** | 重试 + 熔断 + 状态恢复 | 保证系统可用性，防止僵尸会话 |
| **可观测性** | OpenTelemetry + Prometheus | 生产级监控，支持全链路追踪 |
| **扩展性** | 插件化工具 + Multi-Agent 预留 | 支持未来功能扩展，避免重构 |
| **安全性** | BYOK + 工具鉴权 + HITL | 保护用户隐私，防止误操作 |

---

## 十、风险与挑战

### 10.1 技术风险

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| LLM 调用不稳定 | 用户体验差 | 重试机制 + 降级策略（返回预设回复） |
| Token 成本过高 | 运营成本高 | 上下文摘要 + 用户配额限制 |
| 状态不一致 | 会话卡死 | 定期状态恢复 + 监控告警 |
| 工具执行失败 | 运维操作失败 | 超时控制 + 错误重试 + 人工介入 |

### 10.2 业务风险

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 误操作（如误删数据） | 数据丢失 | HITL 确认 + 操作审计日志 |
| 权限绕过 | 安全漏洞 | 二次鉴权 + 工具执行前校验 |
| 用户滥用 | 资源耗尽 | 限流 + 配额管理 |

---

## 十一、后续迭代方向

1. **多模态支持**: 支持图片、文件上传，LLM 分析日志文件
2. **主动推送**: Bot 主动推送告警、报表
3. **工作流编排**: 支持多步骤工作流（如发布流程）
4. **知识库集成**: 接入文档、Wiki，提升回答准确性
5. **语音交互**: 支持语音输入/输出

---

## 十二、参考资料

- [OpenAI Function Calling](https://platform.openai.com/docs/guides/function-calling)
- [Anthropic Claude Tool Use](https://docs.anthropic.com/claude/docs/tool-use)
- [OpenTelemetry Go SDK](https://opentelemetry.io/docs/instrumentation/go/)
- [Prometheus Best Practices](https://prometheus.io/docs/practices/naming/)
- [State Machine Design Patterns](https://refactoring.guru/design-patterns/state)

