# Logic 服务审计与优化方案

## 1. 现状审计 (Audit Report)

经过对 `logic` 服务的深度审计，发现以下关键问题，其中 **SeqID 生成竞态** 和 **明文密码存储** 是极其严重的漏洞。

| 问题点 | 严重程度 | 描述 | 影响 |
| :--- | :---: | :--- | :--- |
| **SeqID 生成竞态** | 🔴 致命 | `handleMessage` 中采用 "Read-Modify-Write" 模式。即使 Repo 层有 CAS 保护 Session 表，但无法防止多条消息获得相同的 SeqID 并插入 Message 表。 | 消息乱序、重复，客户端同步异常。 |
| **明文密码存储** | 🔴 致命 | `AuthService` 直接存储和对比明文密码。 | 数据库泄露即导致所有用户账户失窃，极度不安全。 |
| **DB 强依赖 (无缓存)** | 🟠 高 | `GetUser` (鉴权/查询)、`GetMembers` (发消息) 均直连 DB。 | 数据库成为系统瓶颈，Login/SendMessage 延迟高。 |
| **复杂查询低效** | 🟡 中 | `GetContactList` 使用多表 JOIN + IN 查询。 | 随着数据量增长，联系人列表加载变慢。 |
| **路由上报瓶颈** | 🟠 高 | `SyncState` 仅支持单条事件处理。 | 无法应对 Gateway 重连风暴。 |

### 1.1 SeqID 竞态代码分析
```go
// logic/service/chat.go
// 1. Read
session, _ := s.sessionRepo.GetSession(ctx, req.SessionId)
// 2. Modify
seqID := session.MaxSeqID + 1
// 3. Save Message with seqID (若此时并发，两个请求都用同一个 seqID)
s.messageRepo.SaveMessage(..., seqID)
// 4. Update Session (CAS)
s.sessionRepo.UpdateMaxSeqID(..., seqID)
```
CAS 仅保护了 Session 表不回退，但无法阻止多条消息使用相同的 SeqID 落库。

---

## 2. 优化方案 (Optimization Strategy)

### 2.1 核心修复: 原子化 SeqID 生成

**目标**: 保证会话内 SeqID 严格单调递增，无碰撞。

**方案**: 利用 Redis 的 `INCR` 命令或 Lua 脚本实现原子递增。

**伪代码**:
```go
// Repo 层
func (r *redisSessionRepo) IncrSeqID(ctx context.Context, sessionID string) (int64, error) {
    key := fmt.Sprintf("session:%s:seq", sessionID)
    return r.redis.Incr(ctx, key).Result()
}
```

### 2.2 安全修复: 密码哈希化

**目标**: 保护用户凭据安全。

**方案**: 在 `Register` 时使用 `bcrypt` 加密，`Login` 时使用 `bcrypt` 验证。

```go
// Register
hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
user.Password = string(hash)

// Login
err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password))
```

### 2.3 性能优化: 引入多级缓存

**目标**: 降低 DB 压力，提升热点数据读取速度。

1.  **用户缓存 (User Cache)**:
    *   Key: `user:{username}`
    *   Value: Protobuf/JSON 序列化的 User 对象。
    *   TTL: 1小时 + 自动续期。
    *   生效点: `ValidateToken`, `Login`。

2.  **会话成员缓存 (Member Cache)**:
    *   Key: `session:{session_id}:members`
    *   Value: Redis Set 或 Hash (field=username)。
    *   生效点: `handleMessage` (判断用户是否在群、获取群成员列表)。

### 2.4 架构优化: 异步与批量

1.  **异步落库**: 引入 `persistence-service` 消费 MQ 实现异步消息落库。
2.  **批量路由同步**: 修改 Proto 支持 `repeated UserOnline`，Logic 使用 Pipeline 写入 Redis。

---

## 3. 实施路线图 (Implementation Roadmap)

### Phase 1: 紧急修复 (Hotfix)
- [ ] **SeqID**: 引入 Redis 实现 `IncrSeqID`。
- [ ] **Auth**: 引入 `bcrypt` 对密码进行加解密（需处理存量数据清洗）。

### Phase 2: 性能提升 (Performance)
- [ ] **Cache**: 在 `UserRepo` 和 `SessionRepo` 上层增加 Redis 缓存装饰器。
- [ ] **Member Cache**: 优化 `GetMembers` 逻辑，优先查 Redis。

### Phase 3: 协议与架构 (Architecture)
- [ ] **GatewayOps**: 升级 Proto 支持 Batch Sync。
- [ ] **Async DB**: 评估消息异步落库方案。

---

## 4. 总结

Logic 服务的当务之急是**修复安全漏洞（明文密码）和逻辑漏洞（SeqID 竞态）**。缓存层优化虽然重要，但应排在正确性和安全性之后。
