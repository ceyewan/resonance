# IM SDK Repository 实现说明

本文档说明 IM SDK 中各个 Repository 接口的实现，包括基于 Genesis 组件的 MySQL 和 Redis 实现。

## 📁 文件结构

```
im-sdk/repo/
├── repo.go              # Repository 接口定义
├── router_repo.go       # RouterRepo 的 Redis 实现
├── user_repo.go         # UserRepo 的 MySQL 实现
├── session_repo.go      # SessionRepo 的 MySQL 实现
├── message_repo.go      # MessageRepo 的 MySQL 实现
├── testutil.go          # 测试工具和连接管理
├── router_test.go       # RouterRepo 测试
├── user_test.go         # UserRepo 测试
├── session_test.go      # SessionRepo 测试
├── message_test.go      # MessageRepo 测试
└── README.md            # 本文档
```

## 📋 Repository 接口总览

| Repository                       | 存储介质 | 核心功能                       | 主要场景            |
| -------------------------------- | -------- | ------------------------------ | ------------------- |
| [RouterRepo](#routerrepo-实现)   | Redis    | 用户网关映射、批量路由         | 消息推送负载均衡    |
| [UserRepo](#userrepo-实现)       | MySQL    | 用户 CRUD、搜索                | 用户管理、好友查找  |
| [SessionRepo](#sessionrepo-实现) | MySQL    | 会话管理、成员管理、联系人列表 | 单聊/群聊、会话列表 |
| [MessageRepo](#messagerepo-实现) | MySQL    | 消息存储、信箱写扩散、历史查询 | 消息收发、离线推送  |

---

## 🔧 RouterRepo 实现

### 概述

`RouterRepo` 负责管理用户与网关实例的映射关系，通常存储在 Redis 中以支持快速的读写操作。实现基于 Genesis 的 `cache` 和 `connector` 组件，确保了高性能和可靠性。

### 核心特性

- **依赖注入设计**: 支持灵活的依赖注入，调用方提供 logger、redisConn 等依赖
- **基于 Genesis 组件**: 使用 `cache.Cache` 和 `connector.RedisConnector` 统一接口
- **自动过期机制**: 用户网关映射 24 小时自动过期，防止僵尸连接
- **批量操作支持**: 支持批量获取用户网关映射，提高群发消息性能
- **完善的日志记录**: 集成结构化日志，便于调试和监控
- **错误处理**: 完整的参数验证和错误处理

### 数据结构

```go
type Router struct {
    Username  string `json:"username"`   // 用户名
    GatewayID string `json:"gateway_id"` // 网关实例 ID
    RemoteIP  string `json:"remote_ip"`  // 客户端 IP 地址
    Timestamp int64  `json:"timestamp"`  // 建立连接的时间戳
}
```

### Redis Key 设计

- **Key 格式**: `resonance:router:user:{username}`
- **TTL**: 24 小时
- **序列化**: JSON
- **前缀**: `resonance:router:` (可配置)

### 接口方法

```go
// SetUserGateway 设置用户的网关映射关系
SetUserGateway(ctx context.Context, router *model.Router) error

// GetUserGateway 获取用户的网关映射关系
GetUserGateway(ctx context.Context, username string) (*model.Router, error)

// DeleteUserGateway 删除用户的网关映射关系
DeleteUserGateway(ctx context.Context, username string) error

// BatchGetUsersGateway 批量获取用户的网关映射关系
BatchGetUsersGateway(ctx context.Context, usernames []string) ([]*model.Router, error)
```

### 使用示例

```go
import (
    "github.com/ceyewan/genesis/clog"
    "github.com/ceyewan/genesis/connector"
    "github.com/ceyewan/resonance/im-sdk/repo"
)

// 创建 Redis 连接器
redisConfig := &connector.RedisConfig{
    Addr:     "localhost:6379",
    Password: "",
    DB:       0,
    PoolSize: 10,
}

redisConn, err := connector.NewRedis(redisConfig)
if err != nil {
    return err
}
defer redisConn.Close()

// 创建日志记录器
logger, err := clog.New(&clog.Config{
    Level:  "info",
    Format: "json",
    Output: "stdout",
})
if err != nil {
    return err
}

// 创建 RouterRepo 实例
routerRepo, err := repo.NewRouterRepo(redisConn, repo.WithLogger(logger))
if err != nil {
    return err
}
defer routerRepo.Close()

// 设置用户网关映射
router := &model.Router{
    Username:  "alice",
    GatewayID: "gateway-001",
    RemoteIP:  "192.168.1.100",
    Timestamp: time.Now().Unix(),
}
err = routerRepo.SetUserGateway(ctx, router)

// 批量获取用户网关映射
routers, err := routerRepo.BatchGetUsersGateway(ctx, []string{"alice", "bob", "charlie"})
```

---

## 🔧 UserRepo 实现

### 概述

`UserRepo` 负责用户数据的持久化，使用 MySQL 存储，基于 Genesis 的 `db` 组件实现。

### 核心特性

- **CRUD 完整支持**：创建、查询、搜索、更新用户
- **用户名唯一性**：数据库级别保证 username 唯一
- **模糊搜索**：支持按用户名和昵称进行 LIKE 查询
- **搜索限制**：最多返回 50 条结果，防止数据过载

### 数据结构

```go
type User struct {
    Username   string    `gorm:"column:username;primaryKey" json:"username"`
    Nickname   string    `gorm:"column:nickname;type:varchar(64)" json:"nickname"`
    Password   string    `gorm:"column:password;type:varchar(128)" json:"password"`
    Avatar     string    `gorm:"column:avatar;type:varchar(255)" json:"avatar"`
    CreatedAt  time.Time `gorm:"column:created_at" json:"created_at"`
    UpdatedAt  time.Time `gorm:"column:updated_at" json:"updated_at"`
}
```

### 接口方法

```go
// CreateUser 创建新用户
CreateUser(ctx context.Context, user *model.User) error

// GetUserByUsername 根据用户名获取用户
GetUserByUsername(ctx context.Context, username string) (*model.User, error)

// SearchUsers 搜索用户（按用户名或昵称模糊匹配）
SearchUsers(ctx context.Context, query string) ([]*model.User, error)

// UpdateUser 更新用户信息
UpdateUser(ctx context.Context, user *model.User) error
```

### 使用示例

```go
// 创建 MySQL 连接
mysqlConn, _ := connector.NewMySQL(&connector.MySQLConfig{
    Host:     "localhost:3306",
    Username: "resonance",
    Password: "resonance123",
    Database: "resonance",
})

// 创建 DB 组件
database, _ := db.New(mysqlConn, &db.Config{})

// 创建 UserRepo
userRepo, _ := repo.NewUserRepo(database, repo.WithUserRepoLogger(logger))
defer userRepo.Close()

// 创建用户
user := &model.User{
    Username: "alice",
    Nickname: "爱丽丝",
    Password: "hashed_password",
}
err := userRepo.CreateUser(ctx, user)

// 搜索用户
users, err := userRepo.SearchUsers(ctx, "alice")
```

### 性能特点

- **主键查询**：O(1) - 基于 username 主键
- **模糊搜索**：O(n) - LIKE %query% 无法利用索引，适合小规模数据
- **更新操作**：O(1) - 基于 username 主键

### 测试覆盖

- ✅ 创建用户（正常、重复、空用户名、nil）
- ✅ 获取用户（存在、不存在、空用户名）
- ✅ 搜索用户（用户名、昵称、空字符串、不存在）
- ✅ 更新用户（正常、不存在、空用户名、nil）
- ✅ 并发创建（10 goroutines × 10 users）

---

## 🔧 SessionRepo 实现

### 概述

`SessionRepo` 负责会话和成员管理，使用 MySQL 存储，支持单聊（Type=1）和群聊（Type=2）。

### 核心特性

- **会话类型支持**：单聊（Type=1）、群聊（Type=2）
- **成员管理**：添加成员、获取成员列表、角色管理
- **CAS 乐观锁**：UpdateMaxSeqID 使用 `WHERE max_seq_id < newSeqID` 防止并发覆盖
- **联系人列表**：原生 SQL 三表联查，只返回单聊关系用户
- **用户会话列表**：获取用户的所有会话（含最后阅读位置）

### 数据结构

```go
// Session 会话元数据
type Session struct {
    SessionID     string    `gorm:"column:session_id;primaryKey" json:"session_id"`
    Type          int       `gorm:"column:type;type:tinyint" json:"type"`           // 1-单聊, 2-群聊
    Name          string    `gorm:"column:name;type:varchar(128)" json:"name"`       // 群名
    OwnerUsername string    `gorm:"column:owner_username;type:varchar(64)" json:"owner_username"`
    MaxSeqID      int64     `gorm:"column:max_seq_id;type:bigint" json:"max_seq_id"` // 最新消息序号
    CreatedAt     time.Time `gorm:"column:created_at" json:"created_at"`
    UpdatedAt     time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// SessionMember 会话成员
type SessionMember struct {
    SessionID   string    `gorm:"column:session_id;primaryKey" json:"session_id"`
    Username    string    `gorm:"column:username;primaryKey" json:"username"`
    Role        int       `gorm:"column:role;type:tinyint;default:0" json:"role"`     // 0-成员, 1-管理员
    LastReadSeq int64     `gorm:"column:last_read_seq;type:bigint;default:0" json:"last_read_seq"`
    CreatedAt   time.Time `gorm:"column:created_at" json:"created_at"`
}
```

### 接口方法

```go
// CreateSession 创建会话
CreateSession(ctx context.Context, session *model.Session) error

// GetSession 获取会话详情
GetSession(ctx context.Context, sessionID string) (*model.Session, error)

// GetUserSession 获取用户的特定会话（含最后阅读位置）
GetUserSession(ctx context.Context, username, sessionID string) (*model.SessionMember, error)

// GetUserSessionList 获取用户的所有会话列表
GetUserSessionList(ctx context.Context, username string) ([]*model.Session, error)

// AddMember 添加成员
AddMember(ctx context.Context, member *model.SessionMember) error

// GetMembers 获取会话成员
GetMembers(ctx context.Context, sessionID string) ([]*model.SessionMember, error)

// UpdateMaxSeqID 更新会话最新序列号（CAS 操作）
UpdateMaxSeqID(ctx context.Context, sessionID string, newSeqID int64) error

// GetContactList 获取联系人列表（仅单聊关系）
GetContactList(ctx context.Context, username string) ([]*model.User, error)
```

### 使用示例

```go
// 创建会话
session := &model.Session{
    SessionID: "group_001",
    Type:      2, // 群聊
    Name:      "测试群组",
    OwnerUsername: "alice",
}
err := sessionRepo.CreateSession(ctx, session)

// 添加成员
member := &model.SessionMember{
    SessionID: "group_001",
    Username:  "bob",
    Role:      0, // 成员
}
err := sessionRepo.AddMember(ctx, member)

// 更新序列号（CAS 保护）
err := sessionRepo.UpdateMaxSeqID(ctx, "group_001", 100)

// 获取联系人列表（仅单聊）
contacts, err := sessionRepo.GetContactList(ctx, "alice")
```

### CAS 乐观锁机制

```go
// UpdateMaxSeqID 实现（防止并发覆盖）
UPDATE t_session
SET max_seq_id = ?, updated_at = ?
WHERE session_id = ? AND max_seq_id < ?

// 示例：
// 当前 max_seq_id = 100
// 线程1: UpdateMaxSeqID(ctx, "sess1", 150) -> 成功，max_seq_id = 150
// 线程2: UpdateMaxSeqID(ctx, "sess1", 120) -> 失败，120 < 150，保持 150
```

### 联系人列表查询

GetContactList 使用原生 SQL 三表联查：

```sql
SELECT DISTINCT u.*
FROM t_user u
INNER JOIN t_session_member sm1 ON u.username = sm1.username
INNER JOIN t_session s ON sm1.session_id = s.session_id
INNER JOIN t_session_member sm2 ON s.session_id = sm2.session_id
WHERE sm2.username = ?   -- 当前用户
  AND s.type = 1          -- 仅单聊
  AND u.username != ?     -- 排除自己
```

### 测试覆盖

- ✅ 创建会话（单聊、群聊、重复、空 session_id、nil）
- ✅ 获取会话（存在、不存在、空 session_id）
- ✅ 成员管理（添加、获取列表、重复、空 session_id）
- ✅ 用户会话（获取、不存在、空用户名）
- ✅ 会话列表（正常、空用户、不存在用户）
- ✅ CAS 更新（更大值、相同值、更小值、不存在会话）
- ✅ 联系人列表（仅单聊、无单聊、不存在用户）
- ✅ 并发添加成员（10 goroutines × 5 members）

---

## 🔧 MessageRepo 实现

### 概述

`MessageRepo` 负责消息存储和信箱管理，使用 MySQL 存储，采用**写扩散模式**（主动写入用户信箱）。

### 核心特性

- **消息持久化**：保存消息内容到 t_message_content 表
- **写扩散模式**：SaveInbox 批量写入用户信箱（t_inbox 表）
- **历史消息查询**：支持序列号分页，默认 50 条，最大 1000 条
- **最后一条消息**：快速获取会话的最后一条消息（用于会话列表展示）
- **未读消息**：按时间倒序查询用户未读消息（小群信箱模式）
- **事务保证**：SaveInbox 使用事务保证批量写入原子性

### 数据结构

```go
// MessageContent 消息内容
type MessageContent struct {
    MsgID          int64     `gorm:"column:msg_id;primaryKey" json:"msg_id"`           // Snowflake ID
    SessionID      string    `gorm:"column:session_id;index:idx_sess_seq" json:"session_id"`
    SenderUsername string    `gorm:"column:sender_username;type:varchar(64)" json:"sender_username"`
    SeqID          int64     `gorm:"column:seq_id;index:idx_sess_seq" json:"seq_id"`   // 会话内序号
    Content        string    `gorm:"column:content;type:text" json:"content"`
    MsgType        string    `gorm:"column:msg_type;type:varchar(32)" json:"msg_type"` // text/image/etc
    CreatedAt      time.Time `gorm:"column:created_at;index" json:"created_at"`
}

// Inbox 用户信箱（写扩散）
type Inbox struct {
    ID            int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
    OwnerUsername string    `gorm:"column:owner_username;index:idx_owner_read" json:"owner_username"`
    SessionID     string    `gorm:"column:session_id" json:"session_id"`
    MsgID         int64     `gorm:"column:msg_id" json:"msg_id"`
    SeqID         int64     `gorm:"column:seq_id" json:"seq_id"`
    IsRead        int       `gorm:"column:is_read;type:tinyint;default:0;index:idx_owner_read" json:"is_read"`
    CreatedAt     time.Time `gorm:"column:created_at" json:"created_at"`
}
```

### 接口方法

```go
// SaveMessage 保存消息内容
SaveMessage(ctx context.Context, msg *model.MessageContent) error

// SaveInbox 批量写入信箱（写扩散）
SaveInbox(ctx context.Context, inboxes []*model.Inbox) error

// GetHistoryMessages 拉取历史消息（分页）
GetHistoryMessages(ctx context.Context, sessionID string, startSeq int64, limit int) ([]*model.MessageContent, error)

// GetLastMessage 获取会话的最后一条消息
GetLastMessage(ctx context.Context, sessionID string) (*model.MessageContent, error)

// GetUnreadMessages 获取用户未读消息
GetUnreadMessages(ctx context.Context, username string, limit int) ([]*model.Inbox, error)
```

### 使用示例

```go
// 1. 保存消息
msg := &model.MessageContent{
    MsgID:          idgen.NextID(),
    SessionID:      "single_chat_alice_bob",
    SenderUsername: "alice",
    SeqID:          1,
    Content:        "Hello, Bob!",
    MsgType:        "text",
}
err := messageRepo.SaveMessage(ctx, msg)

// 2. 写入信箱（写扩散）
inboxes := []*model.Inbox{
    {OwnerUsername: "bob", SessionID: "single_chat_alice_bob", MsgID: msg.MsgID, SeqID: 1, IsRead: 0},
}
err := messageRepo.SaveInbox(ctx, inboxes)

// 3. 拉取历史消息
messages, err := messageRepo.GetHistoryMessages(ctx, "single_chat_alice_bob", 0, 50)

// 4. 获取最后一条消息
lastMsg, err := messageRepo.GetLastMessage(ctx, "single_chat_alice_bob")

// 5. 获取未读消息
unread, err := messageRepo.GetUnreadMessages(ctx, "bob", 10)
```

### 写扩散模式

```go
// 发送消息流程
func SendMessage(ctx context.Context, sessionID, sender string, content string) error {
    // 1. 保存消息内容
    msg := &model.MessageContent{
        MsgID:          idgen.NextID(),
        SessionID:      sessionID,
        SenderUsername: sender,
        SeqID:          getNextSeqID(ctx, sessionID),
        Content:        content,
    }
    messageRepo.SaveMessage(ctx, msg)

    // 2. 获取会话成员
    members := sessionRepo.GetMembers(ctx, sessionID)

    // 3. 批量写入信箱（写扩散）
    inboxes := make([]*model.Inbox, 0, len(members))
    for _, member := range members {
        if member.Username != sender { // 不给自己发
            inboxes = append(inboxes, &model.Inbox{
                OwnerUsername: member.Username,
                SessionID:     sessionID,
                MsgID:         msg.MsgID,
                SeqID:         msg.SeqID,
                IsRead:        0,
            })
        }
    }
    return messageRepo.SaveInbox(ctx, inboxes)
}
```

### 分页限制

```go
// GetHistoryMessages 分页逻辑
limit <= 0:  默认 50 条
limit > 1000: 最大 1000 条

// 查询条件
WHERE session_id = ? AND seq_id >= startSeq
ORDER BY seq_id ASC
LIMIT ?
```

### 未读消息查询

```sql
-- GetUnreadMessages 查询（小群信箱模式）
SELECT *
FROM t_inbox
WHERE owner_username = ?
  AND is_read = 0
ORDER BY created_at DESC  -- 时间倒序
LIMIT ?
```

### 性能特点

- **消息保存**：O(1) - 单条插入
- **信箱写入**：O(n) - n 为会话成员数，使用事务批量插入
- **历史查询**：O(log n) - 利用复合索引 idx_sess_seq(session_id, seq_id)
- **未读查询**：O(log n) - 利用复合索引 idx_owner_read(owner_username, is_read)

### 测试覆盖

- ✅ 保存消息（正常、多条、重复 MsgID、空字段、nil）
- ✅ 批量信箱（正常、空列表、100 条批量）
- ✅ 历史消息（默认限制、自定义限制、序列号过滤、排序、不存在会话、超限）
- ✅ 最后消息（正常、不存在会话）
- ✅ 未读消息（正常、限制数量、不存在用户、空用户名、时间排序）
- ✅ 完整生命周期（保存→信箱→历史→最后→未读）
- ✅ 并发保存消息（10 goroutines × 10 messages）

---

## 🧪 测试指南

### 环境准备

#### 1. 启动 MySQL 和 Redis（使用 Docker Compose）

```bash
cd deploy
docker-compose up -d mysql redis

# 等待服务就绪
docker-compose ps
```

#### 2. 配置环境变量

在项目根目录创建 `.env` 文件：

```bash
# MySQL 配置
MYSQL_ROOT_PASSWORD=root123
MYSQL_DATABASE=resonance
MYSQL_HOST=127.0.0.1
MYSQL_PORT=3306

# Redis 配置
REDIS_ADDR=127.0.0.1:6379
REDIS_DB=1  # 测试环境使用 DB1
```

### 运行测试

```bash
# 运行所有测试
go test ./im-sdk/repo/... -v

# 运行 MySQL 测试（user、session、message）
go test ./im-sdk/repo/... -run="TestUserRepo|TestSessionRepo|TestMessageRepo" -v

# 运行 Redis 测试（router）
go test ./im-sdk/repo/... -run="TestRouterRepo" -v

# 运行并发测试
go test ./im-sdk/repo/... -run="Concurrency" -v

# 跳过集成测试（快速模式）
go test ./im-sdk/repo/... -short
```

### 数据清理机制

**MySQL 数据清理**：

- 测试前和测试后自动调用 `cleanupTestData()`
- 使用 `DELETE FROM` 按依赖顺序清空表（兼容性优先）
- 清理顺序：t_inbox → t_message_content → t_session_member → t_session → t_user

**Redis 数据清理**：

- RouterRepo 测试后自动调用 `cleanupRedisData()`
- 使用 `KEYS resonance:*` + `DEL` 批量删除测试数据
- 统一使用 DB1，避免干扰生产数据（DB0）

### 测试覆盖

| Repository  | CRUD | 并发        | 错误处理 | 边界条件 |
| ----------- | ---- | ----------- | -------- | -------- |
| RouterRepo  | ✅   | ✅ (10×100) | ✅       | ✅       |
| UserRepo    | ✅   | ✅ (10×10)  | ✅       | ✅       |
| SessionRepo | ✅   | ✅ (10×5)   | ✅       | ✅       |
| MessageRepo | ✅   | ✅ (10×10)  | ✅       | ✅       |

---

## 📊 连接池配置

### MySQL 连接池

```go
MySQLConfig{
    MaxIdleConns:    10,  // 最大空闲连接数
    MaxOpenConns:    20,  // 最大打开连接数（支持并发测试）
    ConnMaxLifetime: 1 * time.Hour,  // 连接最大生命周期
}
```

### Redis 连接池

```go
RedisConfig{
    PoolSize:     20,  // 连接池大小（支持并发测试）
    MinIdleConns: 10,  // 最小空闲连接数
    DialTimeout:  5 * time.Second,
    ReadTimeout:  3 * time.Second,
    WriteTimeout: 3 * time.Second,
}
```

### 并发测试压力

- **UserRepo**: 10 goroutines × 10 users = 100 ops
- **SessionRepo**: 10 goroutines × 5 members = 50 ops
- **MessageRepo**: 10 goroutines × 10 messages = 100 ops
- **RouterRepo**: 10 goroutines × 100 ops = 1000 ops（最压力）

连接池大小 20 可满足上述并发测试需求。

---

## 📝 最佳实践

### 1. 依赖注入

- ✅ 由调用方（logic、task 服务）提供 `connector`
- ✅ 注入 `clog.Logger` 用于结构化日志记录
- ✅ 支持可选的指标收集器注入

### 2. 错误处理

```go
// ✅ 推荐：完整的错误处理
user, err := userRepo.GetUserByUsername(ctx, username)
if err != nil {
    logger.ErrorContext(ctx, "Failed to get user",
        clog.String("username", username),
        clog.Error(err),
    )
    return err
}
```

### 3. 资源管理

```go
// ✅ 推荐：正确关闭资源
func (s *Service) Close() error {
    if s.userRepo != nil {
        return s.userRepo.Close()
    }
    return nil
}

// ✅ 推荐：使用 defer 确保资源释放
userRepo, err := repo.NewUserRepo(database, repo.WithUserRepoLogger(logger))
if err != nil {
    return err
}
defer userRepo.Close()
```

---

## 🚨 注意事项

1. **数据库依赖**: MySQL 和 Redis 需要提前启动并配置
2. **连接管理**: 所有连接由 testutil.go 统一管理，测试结束后自动关闭
3. **数据清理**: 测试后自动清理数据，避免对后续测试造成干扰
4. **并发安全**: 所有 Repository 实现都是并发安全的
