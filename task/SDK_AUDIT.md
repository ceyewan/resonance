# internal 审计与持久化方案

## 1. 现状审计 (Audit Report)

经过对 `internal/repo` 的审计，评估如下：

| 模块 | 现状 | 问题点 |
| :--- | :--- | :--- |
| **UserRepo** | GORM 直连 DB。 | 读多写少场景下，缺乏 Redis 缓存，鉴权性能低。 |
| **SessionRepo** | GORM 直连 DB。 | 群成员查询 (`GetMembers`) 和会话列表查询缺乏缓存，高频访问压力大。 |
| **MessageRepo** | 提供了 `SaveMessage` (单条) 和 `SaveInbox` (批量)。 | 缺乏 `BatchSaveMessage` (批量存储消息内容)；缺乏历史消息缓存（漫游性能差）。 |

## 2. 架构验证: 异步持久化 (Async Persistence)

您提出的架构方案是高并发 IM 的主流选择：
> **Logic (MQ Produce)** -> **MQ** -> **Task/Persistence Group (MQ Consume)** -> **internal (DB Save)**

### 2.1 方案可行性分析
*   **优势 (Pros)**:
    *   **削峰填谷**: DB 写入压力不再阻塞 Logic 的 HTTP/RPC 接口，RT (响应时间) 极大降低。
    *   **批量优化**: Consumer 可以聚合多条消息（如每 100ms 或 50条），调用 DB 的 Batch Insert，吞吐量提升数倍。
    *   **解耦**: 消息存储服务独立伸缩，不受 Logic 业务影响。
*   **挑战 (Cons)**:
    *   **最终一致性**: 消息发完后立即查询历史记录，可能查不到（存在毫秒级延迟）。
        *   *解决*: 发送端本地已有消息，无需立即查。接收端收到 Push 时，消息通常已落库（Push 路径通常比 DB 路径慢或并行）。
    *   **数据丢失风险**: 若 MQ 崩溃且未持久化，消息丢失。
        *   *解决*: 使用 NATS JetStream 或 Kafka。

## 3. 缓存策略 (Caching Strategy)

### 3.1 缓存加在哪里？
建议**加在 `internal` 内部**，通过 **Decorator (装饰器) 模式** 实现。
*   **理由**: 对上层调用者（Logic, Task）透明。无论谁调用 SDK，都能享受到缓存加速。
*   **实现**: `CachedUserRepo` 包装 `UserRepo`。

### 3.2 缓存设计
1.  **User/Session Cache (Read-Through)**:
    *   读: 先查 Redis -> Miss 查 DB -> 回填 Redis。
    *   写: 先写 DB -> 再删 Redis (Cache Aside)。

2.  **Message Cache (Write-Through / Write-Behind)**:
    *   **场景**: 消息漫游（查询最新的 N 条）。
    *   **结构**: Redis Sorted Set (`ZSET`)。
        *   Key: `msg:{session_id}`
        *   Score: `seq_id`
        *   Value: Message JSON
    *   **写流程 (配合异步持久化)**:
        *   Task Consumer 收到消息 -> **写入 Redis ZSET** (保证漫游立即可见) -> 异步/批量写入 DB (冷数据)。
    *   **读流程**:
        *   `GetHistory`: 先查 Redis ZSET -> 不够再查 DB。

## 4. internal 优化建议

1.  **增加 Batch 方法**:
    *   `MessageRepo` 需要增加 `BatchSaveMessages(ctx, msgs []*model.MessageContent) error`，以便 Consumer 进行批量落库。

2.  **实现 CachedRepo**:
    *   在 `internal` 中引入 `redis` 依赖（或通过接口注入），实现缓存层。

3.  **SeqID 生成器**:
    *   将 `IncrSeqID` 逻辑封装进 `internal`，作为标准能力提供给 Logic。

---

## 5. 总结

`internal` 目前作为一个纯 DAL (Data Access Layer) 是合格的，但为了支撑高性能架构，它需要进化为 **Smart DAL**（集成缓存 + 批量能力）。
您的“Logic 只管写 MQ，Task 负责持久化”的策略是完全正确的方向。
