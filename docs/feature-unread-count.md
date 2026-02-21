# Issue: 未读消息计数功能

## 元信息

- **ID**: FEAT-001
- **标题**: 实现未读消息计数功能
- **优先级**: P0 - 关键
- **状态**: 待开发
- **负责人**: -
- **创建时间**: 2025-01-25

## 问题描述

当前系统无法获取用户的未读消息数，导致：

- 会话列表无法显示未读数小红点
- 用户无法直观了解有多少未读消息
- 必须进入会话才能确认是否有新消息

## 受影响范围

- `model`: 数据模型
- `repo`: 数据访问层
- `logic/service`: 业务逻辑层
- `api/proto`: API 定义

## 根因分析

1. `t_inbox` 表存在但未被使用
2. `t_session_member.last_read_seq` 虽然记录了已读位置，但未同步更新
3. 缺少计算未读数的接口

## 解决方案

### 1. 未读数计算逻辑

```
未读数 = 会话 MaxSeqID - 用户 LastReadSeq
```

**边界情况处理**：

- 用户从未进入会话：`LastReadSeq = 0`，未读数 = `MaxSeqID`
- LastReadSeq >= MaxSeqID：未读数 = 0

### 2. 数据结构利用

**已有表结构**：

- `t_session_member.last_read_seq`: 用户在会话中的已读位置
- `t_session.max_seq_id`: 会话当前最大消息序号

**Inbox 表（可选）**：

- 用于记录每条消息的已读状态
- 支持查询具体哪些消息未读

### 3. API 设计

```protobuf
// api/proto/logic/v1/session.proto

message GetUnreadCountRequest {
  string username = 1;
}

message GetUnreadCountResponse {
  // 会话级未读数
  map<string, int64> session_unreads = 1;  // session_id -> unread_count
  // 全局未读数
  int64 total_unread = 2;
}

service SessionService {
  rpc GetUnreadCount(GetUnreadCountRequest) returns (GetUnreadCountResponse);
}
```

### 4. 实现方案

#### 方案 A: 实时计算（推荐初期使用）

```go
// logic/service/session.go
func (s *SessionService) GetUnreadCount(ctx context.Context, username string) (*UnreadCount, error) {
    // 1. 获取用户所有会话
    sessions, _ := s.sessionRepo.GetUserSessions(ctx, username)

    result := &UnreadCount{
        SessionUnreads: make(map[string]int64),
    }

    for _, session := range sessions {
        // 2. 获取用户在该会话的已读位置
        member, _ := s.sessionRepo.GetSessionMember(ctx, username, session.SessionID)
        if member == nil {
            continue
        }

        // 3. 计算未读数
        unread := session.MaxSeqID - member.LastReadSeq
        if unread > 0 {
            result.SessionUnreads[session.SessionID] = unread
            result.TotalUnread += unread
        }
    }

    return result, nil
}
```

#### 方案 B: Redis 缓存（性能优化）

```go
// 缓存结构
// Key: unread:{username}
// Value: Hash {session_id: unread_count}
// TTL: 5 分钟

// 更新时机：
// 1. 发送消息时：接收者缓存 +1
// 2. 标记已读时：重置为 0
// 3. 缓存失效时：从 DB 重新计算
```

### 5. 实时更新机制

**发送消息时**：

```go
// logic/service/chat.go - SendMessage
func (s *ChatService) SendMessage(...) {
    // ... 发送消息逻辑 ...

    // 异步更新接收者未读数（如果使用 Redis 缓存）
    for _, member := range members {
        if member.Username != req.FromUsername {
            s.unreadCache.Increment(ctx, member.Username, req.SessionId)
        }
    }
}
```

**标记已读时**：

```go
// logic/service/session.go - MarkAsRead
func (s *SessionService) MarkAsRead(...) {
    // 1. 更新 last_read_seq
    s.sessionRepo.UpdateLastReadSeq(ctx, username, sessionID, seqID)

    // 2. 清除未读数（如果使用 Redis 缓存）
    s.unreadCache.Reset(ctx, username, sessionID)
}
```

## 验收标准

- [ ] `GetUnreadCount` API 已实现
- [ ] 单聊场景未读数计算正确
- [ ] 群聊场景未读数计算正确
- [ ] 标记已读后未读数清零
- [ ] 性能：100 个会话未读数计算 < 50ms

## 后续优化

- 引入 Redis 缓存提升性能
- 支持按会话类型筛选未读（单聊/群聊）
- 支持 @提及消息未读单独计数
