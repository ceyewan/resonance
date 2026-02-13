# Issue: 消息历史拉取功能

## 元信息

- **ID**: FEAT-002
- **标题**: 实现消息历史拉取（分页查询）
- **优先级**: P0 - 关键
- **状态**: 待开发
- **负责人**: -
- **创建时间**: 2025-01-25

## 问题描述

当前系统无法拉取历史消息，导致：

- 新用户进入会话看不到历史记录
- 用户切换设备后消息丢失
- 无法上下文查看之前的对话

## 受影响范围

- `api/proto`: API 定义
- `internal/repo`: 数据库查询
- `logic/service`: 业务逻辑

## 根因分析

1. `t_message_content` 表存储了消息，但缺少查询接口
2. 缺少分页查询设计
3. 缺少消息权限校验

## 解决方案

### 1. API 设计

```protobuf
// api/proto/logic/v1/chat.proto

message GetMessagesRequest {
  string session_id = 1;
  string username   = 2;  // 请求者，用于权限校验

  // 分页参数（游标分页）
  int64  seq_id     = 3;  // 游标：从此 seq_id 开始查询（不包含）
  int32  limit      = 4;  // 每页数量，默认 20，最大 100
  bool   ascending  = 5;  // true: 向前查，false: 向后查（默认 false）
}

message GetMessagesResponse {
  repeated MessageInfo messages = 1;
  bool                  has_more = 2;  // 是否还有更多消息
}

message MessageInfo {
  int64  msg_id          = 1;
  int64  seq_id          = 2;
  string sender_username = 3;
  string content         = 4;
  string msg_type        = 5;
  int64  timestamp       = 6;
  bool   is_recalled     = 7;
}

service ChatService {
  rpc GetMessages(GetMessagesRequest) returns (GetMessagesResponse);
}
```

### 2. 分页策略

**游标分页（推荐）**：

- 使用 `seq_id` 作为游标
- 避免传统 `OFFSET` 分页的性能问题
- 支持向前/向后双向查询

```
场景 1: 首次加载（最近的消息）
  seq_id = 0, ascending = false, limit = 20
  返回: seq 101-120

场景 2: 向前翻页（加载更早的消息）
  seq_id = 100, ascending = false, limit = 20
  返回: seq 81-100

场景 3: 向后翻页（加载更晚的消息）
  seq_id = 120, ascending = true, limit = 20
  返回: seq 121-140
```

### 3. 权限校验

```go
func (s *ChatService) GetMessages(ctx context.Context, req *GetMessagesRequest) (*GetMessagesResponse, error) {
    // 1. 检查用户是否是会话成员
    member, err := s.sessionRepo.GetSessionMember(ctx, req.Username, req.SessionId)
    if err != nil || member == nil {
        return nil, status.Errorf(codes.PermissionDenied, "not a session member")
    }

    // 2. 查询消息
    messages, err := s.messageRepo.GetMessages(ctx, req.SessionId, req.SeqId, req.Limit, req.Ascending)
    // ...
}
```

### 4. 数据库查询

```go
// internal/repo/message.go
func (r *messageRepo) GetMessages(ctx context.Context, sessionID string, seqID int64, limit int, ascending bool) ([]*model.MessageContent, error) {
    gormDB := r.db.DB(ctx)

    query := gormDB.Where("session_id = ? AND is_recalled = 0", sessionID)

    // 游标过滤
    if seqID > 0 {
        if ascending {
            query = query.Where("seq_id > ?", seqID)
        } else {
            query = query.Where("seq_id < ?", seqID)
        }
    }

    // 排序
    if ascending {
        query = query.Order("seq_id ASC")
    } else {
        query = query.Order("seq_id DESC")
    }

    // 限制数量
    if limit > 100 {
        limit = 100
    }

    var messages []*model.MessageContent
    if err := query.Limit(limit).Find(&messages).Error; err != nil {
        return nil, fmt.Errorf("failed to get messages: %w", err)
    }

    return messages, nil
}
```

### 5. 性能优化

**索引优化**：

```sql
-- 确保索引支持游标查询
CREATE INDEX idx_session_seq ON t_message_content(session_id, seq_id);
```

**缓存优化**（可选）：

- 最近 100 条消息缓存在 Redis
- Key: `messages:{session_id}:latest`
- TTL: 10 分钟

### 6. 消息内容压缩（可选优化）

对于大量历史消息，可以在客户端缓存后：

- 返回列表时不包含 content 字段
- 客户端按需获取完整内容

## 验收标准

- [ ] `GetMessages` API 已实现
- [ ] 游标分页正常工作
- [ ] 权限校验正确（非成员无法获取）
- [ ] 性能：单次查询 < 100ms
- [ ] 支持向前/向后翻页
- [ ] 撤回的消息不在列表中返回

## 参考链接

- 游标分页最佳实践
- 数据库分页性能优化
