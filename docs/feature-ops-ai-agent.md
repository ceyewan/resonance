# Ops AI Agent 实施计划

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
                                │ │  Redis  │ (State/Action)
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

---

## 二、数据模型设计

### 2.1 用户体系与权限

**1. Bot 账号 (Built-in)**
```sql
-- t_user 表中添加 Bot 用户
INSERT INTO t_user (username, nickname, is_bot, created_at)
VALUES ('ops-bot', '运维助手', 1, NOW());
```

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

### 2.2 Redis 数据模型

**1. 上下文缓存**
*   Key: `bot:context:{session_id}`
*   Type: List (JSON)
*   TTL: 24h

**2. 挂起动作 (Pending Action)**
*   Key: `bot:action:{action_id}`
*   Type: String (JSON)
*   TTL: 5m
*   Value: `{"tool": "restart", "params": {...}, "user_role": "admin"}`

### 2.3 消息协议扩展

*   **`interactive`**: 确认卡片 (JSON Payload)
*   **`action_response`**: 按钮点击回调 (ActionID)

---

## 三、服务设计

### 3.1 Bot Service 架构

```
bot/
├── main.go
├── config/                 # 系统配置
├── consumer/               # MQ 消费者
│   └── bot.go            # 消息分流
├── agent/
│   ├── agent.go          # Agent 核心
│   ├── config_manager.go # 用户配置加载 (User Settings)
│   ├── executor/         # Action 执行器
│   ├── state/            # Redis 状态管理
│   ├── llm/              # LLM Client Factory
│   └── mcp/              # MCP 工具链
│       └── registry.go   # 工具注册 (支持基于 Role 过滤)
└── logic/                  # Logic 客户端
```

### 3.2 关键业务流程

#### A. 注册即好友 (Onboarding)
在 `User Service` 或 `Logic` 的注册流程中注入 Hook：
1.  用户注册成功。
2.  自动插入 `t_session_member`，建立用户与 `ops-bot` 的会话。
3.  Bot 发送欢迎语：“你好，我是你的 AI 助手...”。

#### B. 配置加载与 Client 初始化
```go
func (a *Agent) GetLLMClient(ctx context.Context, userID string) (LLMClient, error) {
    // 1. 获取用户配置 (Cache-Aside)
    settings := a.configMgr.GetSettings(userID)
    
    // 2. 决定 Client 策略
    if settings != nil && settings.ApiKey != "" {
        // BYOK 模式：解密 Key，使用用户配置
        return llm.NewClient(settings.Provider, decrypt(settings.ApiKey), settings.Endpoint)
    }
    
    // 3. 默认模式：使用系统配置
    return a.systemClient
}
```

#### C. 工具鉴权 (Permission Check)
```go
func (r *Registry) GetToolsForUser(role string) []Tool {
    tools := []Tool{ToolChat, ToolSearch} // 基础工具
    
    if role == "admin" {
        // 仅 Admin 可见运维工具
        tools = append(tools, ToolRestartGateway, ToolViewLogs, ToolManagePods)
    }
    
    return tools
}
```

---

## 四、交互流程

### 4.1 敏感操作流程 (HITL - Interactive)

```
User (Admin)    BotService          Redis            LLM
 |                 |                 |               |
 |--"重启 Gateway"->|                 |               |
 |                 |--1. Check Role->|               |
 |                 |  (if user!=admin return 403)|
 |                 |                 |               |
 |                 |--2. 分析意图------------------->|
 |                 |<--3. 建议重启-------------------|
 |                 |                 |               |
 |                 |--4. Gen ActionID & Store ------>|
 |<--[确认卡片]-----|                 |               |
 |                 |                 |               |
 |--[点击确认]----->|                 |               |
 | (action_resp)   |                 |               |
 |                 |--5. Get & Del Action----------->|
 |                 |--6. Exec Tool (Bypass LLM)----->|
 |<--"已重启"-------|                 |               |
```

---

## 五、实施步骤

### Phase 1: 基础设施与 Schema

- [ ] **步骤 1.1**: 更新 `api/proto/mq/v1/bot.proto`
- [ ] **步骤 1.2**: 执行 SQL Schema 变更 (`t_user.role`, `user_bot_settings`)
- [ ] **步骤 1.3**: 初始化 Bot Service

### Phase 2: 核心逻辑改造

- [ ] **步骤 2.1**: 实现“注册即好友”逻辑
- [ ] **步骤 2.2**: 实现 Logic 消息路由与落库适配

### Phase 3: Bot Service 实现

- [ ] **步骤 3.1**: 实现 `ConfigManager` (用户配置加载)
- [ ] **步骤 3.2**: 实现 `LLMClientFactory` (支持 BYOK)
- [ ] **步骤 3.3**: 实现 MCP 工具鉴权 (Role-based)
- [ ] **步骤 3.4**: 实现 HITL 交互逻辑

### Phase 4: 前端与设置

- [ ] **步骤 4.1**: 实现 Slash Command 支持
- [ ] **步骤 4.2**: Web 端支持交互卡片渲染

### 4.1 Slash Command 设计

| 命令 | 功能 | 权限 | 说明 |
|------|------|------|------|
| `/help` | 显示帮助 | 所有用户 | 列出所有可用命令 |
| `/model` | 查看当前模型 | 所有用户 | 显示正在使用的 LLM 模型 |
| `/models` | 列出可用模型 | 所有用户 | 列出系统支持的模型列表 |
| `/key <provider>` | 设置 API Key | 所有用户 | 绑定用户的 LLM Provider |
| `/key remove` | 移除 API Key | 所有用户 | 切换回系统默认配置 |
| `/clear` | 清空对话上下文 | 所有用户 | 重置 Bot 记忆，开始新对话 |
| `/admin` | 进入 Admin 模式 | 仅 Admin | 解锁运维工具权限 |

**实现示例**：

```typescript
// web/src/hooks/useSlashCommand.ts

const slashCommands = [
  {
    command: '/help',
    description: '显示帮助信息',
    handler: handleHelp,
  },
  {
    command: '/model',
    description: '查看当前 AI 模型',
    handler: handleModel,
  },
  {
    command: '/key',
    description: '设置 API Key (BYOK)',
    handler: handleSetKey,
    params: [{ name: 'provider', description: 'Provider (openai/anthropic)' }],
  },
  {
    command: '/clear',
    description: '清空对话上下文',
    handler: handleClear,
  },
  {
    command: '/admin',
    description: '进入 Admin 模式（需验证）',
    handler: handleAdmin,
  },
];

// 命令检测
function parseSlashCommand(content: string): { command: string; args: string[] } | null {
  if (!content.startsWith('/')) return null;
  const parts = content.trim().split(/\s+/);
  return { command: parts[0], args: parts.slice(1) };
}
```

### 4.2 交互卡片 (Interactive Card)

```typescript
// web/src/components/BotMessage.tsx

interface BotCard {
  type: 'action' | 'form' | 'list';
  title: string;
  description?: string;
  actions: Action[];
}

interface Action {
  id: string;           // ActionID
  label: string;         // 按钮文案
  style: 'primary' | 'danger' | 'default';
  confirm?: string;     // 二次确认文案（可选）
}

function BotCard({ card }: { card: BotCard }) {
  return (
    <div className="bot-card">
      <h3>{card.title}</h3>
      {card.description && <p>{card.description}</p>}
      {card.actions.map(action => (
        <button
          key={action.id}
          className={action.style}
          onClick={() => handleActionClick(action)}
        >
          {action.label}
        </button>
      ))}
    </div>
  );
}
```

---

## 六、关键文件清单

| 文件 | 说明 |
|------|------|
| `bot/agent/config_manager.go` | 用户配置管理 |
| `bot/agent/mcp/registry.go` | 工具注册与鉴权 |
| `internal/schema/schema.sql` | 数据库变更脚本 |

---

## 七、技术选型

- **Store**: Redis (Cache), MySQL (Settings)
- **Encryption**: AES-GCM (API Key Storage)
