# Resonance SDK 设计规范 v1.0

本文档整合了 `repo-design.md` 和 `proto-mq.md` 的核心思想，并根据最新的需求（Username as ID）进行了重构。本设计旨在提供一个轻量级、嵌入式的 SDK，供业务层直接调用，屏蔽底层存储细节。

## 1. 核心变更：Username as Identity

系统将废弃原有的 Snowflake ID (`int64 uid`) 作为用户主键，全面转向使用 **Username** (`string`) 作为用户的唯一标识。

*   **Username**: 用户名，全局唯一，不可变更，作为数据库主键和外键。
*   **Nickname**: 昵称，可重复，可修改，用于展示。

## 2. 协议定义 (Proto Contract)

Proto 定义将作为数据交换的标准格式。

### 2.1 公共类型 (`common/v1/types.proto`)

```protobuf
syntax = "proto3";
package resonance.common.v1;
option go_package = "github.com/ceyewan/resonance/im-api/gen/go/common/v1;commonv1";

message User {
  string username   = 1; // 唯一标识
  string nickname   = 2; // 展示名称
  string avatar_url = 3; // 头像
}
```

### 2.2 网关消息 (`gateway/v1/packet.proto`)

增加发送者/接收者的详细信息，减少客户端二次查询。

```protobuf
// ChatRequest 是用户发送的消息 (上行)
// 注意：上行消息尚未持久化，因此没有 msg_id 和 seq_id
message ChatRequest {
  string session_id    = 1; // 会话ID
  string content       = 2; // 内容
  string type          = 3; // 类型
  string from_username = 4; // 发送者 (客户端可不填，由网关填充)
  string from_nickname = 5; // 发送者昵称 (同上)
  string to_username   = 6; // 目标用户 (私聊时可能需要)
  string to_nickname   = 7; // 目标昵称
}

// PushMessage 是推送给用户的消息 (下行，已持久化)
// 注意：下行消息必须包含 msg_id 和 seq_id
message PushMessage {
  int64  msg_id         = 1; // 全局唯一物理ID (Snowflake)
  int64  seq_id         = 2; // 会话内逻辑时钟
  string session_id     = 3; // 会话ID
  string from_username  = 4; // 发送者
  string from_nickname  = 5; // 发送者昵称
  string to_username    = 6; // 接收者/目标用户
  string to_nickname    = 7; // 接收者昵称
  string content        = 8; // 内容
  string type           = 9; // 类型
  int64  send_time      = 10;// 时间戳
}
```

### 2.3 业务逻辑 (`logic/v1/chat.proto`, `event.proto`)

逻辑层和 MQ 事件层也需同步包含这些富文本字段。

## 3. 数据库设计 (MySQL Schema)

所有涉及用户ID的字段类型从 `BIGINT` 变更为 `VARCHAR(64)`。

### 3.1 用户表 (`t_user`)

```sql
CREATE TABLE t_user (
    username    VARCHAR(64) PRIMARY KEY COMMENT '用户名，唯一标识',
    nickname    VARCHAR(64) COMMENT '昵称',
    password    VARCHAR(128) NOT NULL COMMENT '加密密码',
    avatar      VARCHAR(255) COMMENT '头像URL',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户基础表';

-- 2025-05-01 新增需求：默认全员群
-- INSERT INTO t_session (session_id, type, name) VALUES ('0', 2, 'Resonance Room');
-- 注册逻辑变更：新用户自动插入 t_session_member (session_id='0', username=...)
```

### 3.2 会话元数据表 (`t_session`)

```sql
CREATE TABLE t_session (
    session_id  VARCHAR(64) PRIMARY KEY COMMENT '会话ID',
    type        TINYINT UNSIGNED NOT NULL COMMENT '1-单聊, 2-群聊',
    name        VARCHAR(128) COMMENT '群名',
    owner_username VARCHAR(64) COMMENT '群主',
    max_seq_id  BIGINT UNSIGNED DEFAULT 0 COMMENT '最新SeqID',
    updated_at  DATETIME ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='会话元数据表';
```

### 3.3 会话成员表 (`t_session_member`)

```sql
CREATE TABLE t_session_member (
    session_id    VARCHAR(64) NOT NULL,
    username      VARCHAR(64) NOT NULL,
    role          TINYINT DEFAULT 0 COMMENT '0-成员, 1-管理员',
    join_time     DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_read_seq BIGINT UNSIGNED DEFAULT 0,
    PRIMARY KEY (session_id, username),
    INDEX idx_user (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='会话成员表';
```

### 3.4 消息内容表 (`t_message_content`)

```sql
CREATE TABLE t_message_content (
    msg_id          BIGINT UNSIGNED PRIMARY KEY COMMENT 'Snowflake ID',
    session_id      VARCHAR(64) NOT NULL,
    sender_username VARCHAR(64) NOT NULL,
    seq_id          BIGINT UNSIGNED NOT NULL,
    content         TEXT COMMENT '消息内容',
    msg_type        VARCHAR(32) COMMENT 'text/image/etc',
    create_time     DATETIME DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_sess_seq (session_id, seq_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='消息全量表';
```

### 3.5 用户信箱表 (`t_inbox`)

```sql
CREATE TABLE t_inbox (
    id             BIGINT AUTO_INCREMENT PRIMARY KEY,
    owner_username VARCHAR(64) NOT NULL COMMENT '信箱所属用户',
    session_id     VARCHAR(64) NOT NULL,
    msg_id         BIGINT UNSIGNED NOT NULL,
    seq_id         BIGINT UNSIGNED NOT NULL,
    is_read        TINYINT DEFAULT 0,
    create_time    DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uniq_owner_sess_seq (owner_username, session_id, seq_id),
    INDEX idx_owner_read (owner_username, is_read)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='写扩散信箱表';
```

## 4. SDK 接口定义 (Golang Interface)

我们将生成一个 `im-sdk` 库，提供以下 Data Access Object (DAO) 接口，供业务层实现或调用。

### 4.1 目录结构

```text
im-sdk/
├── model/          # 对应数据库表结构的 Go Struct
│   ├── user.go
│   ├── session.go
│   └── message.go
└── repo/           # 接口定义
    ├── user_repo.go
    ├── session_repo.go
    └── message_repo.go
```

### 4.2 接口定义 (`repo`)

**UserRepo**

```go
package repo

import (
    "context"
    "github.com/ceyewan/resonance/im-sdk/model"
)

type UserRepo interface {
    // CreateUser 创建新用户
    CreateUser(ctx context.Context, user *model.User) error
    // GetUserByUsername 根据用户名获取用户
    GetUserByUsername(ctx context.Context, username string) (*model.User, error)
    // UpdateUser 更新用户信息
    UpdateUser(ctx context.Context, user *model.User) error
}

// 命名说明：Repo (Repository) 模式用于屏蔽底层数据存储细节 (MySQL/Redis)。
// 它是领域层与数据层的适配器，比 DAO 更强调“仓库”的概念。
```

**SessionRepo**

```go
package repo

import (
    "context"
    "github.com/ceyewan/resonance/im-sdk/model"
)

type SessionRepo interface {
    // CreateSession 创建会话
    CreateSession(ctx context.Context, session *model.Session) error
    // GetSession 获取会话详情
    GetSession(ctx context.Context, sessionID string) (*model.Session, error)
    // AddMember 添加成员
    AddMember(ctx context.Context, member *model.SessionMember) error
    // GetMembers 获取会话成员
    GetMembers(ctx context.Context, sessionID string) ([]*model.SessionMember, error)
    // UpdateMaxSeqID 更新会话最新序列号 (CAS操作)
    UpdateMaxSeqID(ctx context.Context, sessionID string, newSeqID int64) error
}
```

**MessageRepo**

```go
package repo

import (
    "context"
    "github.com/ceyewan/resonance/im-sdk/model"
)

type MessageRepo interface {
    // SaveMessage 保存消息内容
    SaveMessage(ctx context.Context, msg *model.MessageContent) error
    // SaveInbox 批量写入信箱 (写扩散)
    SaveInbox(ctx context.Context, inboxes []*model.Inbox) error
    // GetHistoryMessages 拉取历史消息
    GetHistoryMessages(ctx context.Context, sessionID string, startSeq int64, limit int) ([]*model.MessageContent, error)
    // GetUnreadMessages 获取用户未读消息 (从小群信箱)
    GetUnreadMessages(ctx context.Context, username string, limit int) ([]*model.Inbox, error)
}
```
