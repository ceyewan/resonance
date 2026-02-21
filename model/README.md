# Model

`model` 定义 Resonance 的核心业务数据模型。

## 数据模型

| 模型 | 表名 | 用途 |
|------|------|------|
| `User` | `t_user` | 用户账户信息 |
| `Session` | `t_session` | 会话（单聊/群聊） |
| `SessionMember` | `t_session_member` | 会话成员关系 |
| `MessageContent` | `t_message_content` | 消息内容 |
| `Inbox` | `t_inbox` | 用户信箱（写扩散） |
| `MessageOutbox` | `t_message_outbox` | 本地消息表（可靠投递） |
| `Router` | Redis | 用户与网关映射 |

## Schema 管理

GORM model tag 是数据库表结构的**唯一真相来源 (Single Source of Truth)**。
表结构通过 `go run main.go -module init` 调用 GORM AutoMigrate 自动创建/更新。

## 文件结构

```text
model/
├── model.go      # 所有数据模型定义（含 AllModels() 辅助函数）
└── README.md
```

## 使用方式

```go
import "github.com/ceyewan/resonance/model"

// 创建用户
user := &model.User{
    Username: "alice",
    Nickname: "Alice",
    Password: "hashed_password",
}

// 创建会话
session := &model.Session{
    SessionID: "sess_001",
    Type:      1, // 1-单聊, 2-群聊
    Name:      "Alice & Bob",
}
```

## 设计原则

- **单一职责**：每个模型对应一个数据表或缓存结构
- **GORM 标签**：使用 GORM 标签定义数据库映射
- **业务语义**：字段名称直接反映业务含义，避免过度抽象

