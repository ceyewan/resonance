# 写扩散与 Inbox 语义

> 本文档描述 Resonance 的写扩散模型、Inbox 的一致性语义和增量拉取设计。阅读完本文后，应该能回答三个问题：为什么选择写扩散而不是读扩散；Inbox 如何同时承担实时推送兜底和离线补偿两个职责；以及客户端如何通过游标实现增量同步。

---

## 1. 写扩散的选择

IM 系统在消息分发上有两种基本模型：写扩散和读扩散。写扩散是在消息产生时，把它写入每个接收者各自的存储视图；读扩散是消息只存一份，接收者读取时再去拉取。

Resonance 选择写扩散，原因是它更适合当前系统的一致性目标。写扩散让每个用户的 Inbox 成为独立的、可游标扫描的事件流，增量拉取只需要一次简单的范围查询，不需要跨多个会话做复杂的 JOIN 或聚合。对于 IM 场景中"用户重连后快速补偿离线消息"这个核心需求，写扩散的查询路径更直接。

写扩散的代价是存储放大：一条消息会在 `t_inbox` 中产生 N 条记录（N 为会话成员数）。当前阶段这个代价是可接受的，因为 Inbox 存储的是 `ChatEvent` 序列化字节，单条记录约 200-300 字节，且 Inbox 本身是可以做冷热分离的。

---

## 2. Inbox 的双重职责

Inbox 在系统中承担两个职责，理解这两个职责的关系是理解整个可靠性设计的关键。

第一个职责是**实时推送的兜底**。Task 在写扩散完成后才发起在线推送，这保证了"用户在线时收到推送"和"用户离线后重连能补偿"这两条路径都以 Inbox 为基础。推送只是加速路径，Inbox 才是事件是否"已送达"的权威判断依据。

第二个职责是**离线补偿的数据源**。客户端通过 `PullInboxDelta` 接口，以自增 `id` 为游标拉取增量事件。这个接口不区分"在线时错过的推送"和"离线期间产生的事件"，统一从 Inbox 中按游标顺序返回，客户端只需要维护一个游标值。

---

## 3. 写扩散流程

```text
MQEvent 到达 Task
  │
  ├── 解析 ChatEvent 和 target_usernames
  │
  ├── 为每个目标用户构造 Inbox 记录
  │     OwnerUsername = target_username
  │     SessionID     = event.session_id
  │     SeqID         = event.seq_id
  │     EventID       = event.event_id
  │     EventType     = 按 payload 类型映射
  │     Payload       = proto.Marshal(ChatEvent)
  │
  ├── 批量写入 t_inbox（ON CONFLICT DO NOTHING）
  │     唯一键：(owner_username, session_id, seq_id)
  │
  └── 写入成功后发起在线推送
```

写扩散的幂等性由 `uniq_owner_sess_seq` 唯一约束保证。当 NATS 重新投递同一条消息时，Task 会再次尝试写入，但唯一键冲突会被静默忽略，不会产生重复记录。

---

## 4. Inbox 记录结构

每条 Inbox 记录对应一个用户需要感知的事件：

```text
Inbox {
  id            → 自增游标，客户端用于增量拉取
  owner_username → 这条记录属于哪个用户
  session_id    → 事件发生在哪个会话
  seq_id        → 事件在会话内的序列号
  event_id      → 全局唯一事件 ID（Snowflake）
  event_type    → 1-Message 2-Recall 3-Edit 4-ReadReceipt 5-SessionUpdate
  payload       → 完整的 ChatEvent（protobuf bytes）
}
```

`payload` 存储完整的 `ChatEvent` 序列化字节，而不是消息 ID 引用。这个设计让客户端拉取 Inbox 时不需要再 JOIN `t_message_content`，一次查询就能拿到所有需要渲染的信息。代价是存储冗余，但对于 IM 场景中"快速补偿离线消息"的需求，这个权衡是合理的。

---

## 5. 增量拉取（PullInboxDelta）

客户端通过以下方式实现增量同步：

```text
客户端维护本地游标 cursor_id（初始为 0）

每次拉取：
  PullInboxDelta(username, cursor_id, limit)
  → SELECT * FROM t_inbox
     WHERE owner_username = ? AND id > ?
     ORDER BY id ASC
     LIMIT ?

收到结果后：
  cursor_id = max(result.id)
  按 session_id 分组，更新本地会话状态
```

这个接口的查询路径走 `idx_owner_id(owner_username, id)` 索引，是 Inbox 表上最频繁的查询，也是索引设计的主要优化目标。

增量拉取不区分事件类型，所有类型的事件（消息、撤回、编辑、已读回执、会话更新）都通过同一个接口返回。客户端根据 `event_type` 字段分发处理，这与 `ChatEvent.oneof payload` 的统一事件模型保持一致。

---

## 6. 已读状态与未读数

已读状态不存储在 Inbox 行上，而是由 `t_session_member.last_read_seq` 统一表达。未读数的计算方式是：

```sql
SELECT COUNT(*) FROM t_inbox
WHERE owner_username = ?
  AND session_id = ?
  AND seq_id > (
    SELECT last_read_seq FROM t_session_member
    WHERE session_id = ? AND username = ?
  )
```

这个设计避免了"用户读到第 80 条消息时需要批量更新 80 条 Inbox 记录"的写放大问题。已读位点是会话维度的概念，`last_read_seq` 是它的正确载体。

当用户调用 `UpdateReadPosition` 时，Logic 更新 `t_session_member.last_read_seq`，并生成一条 `ReadReceipt` 类型的 `ChatEvent` 写入 Outbox，通过 Task 扩散给该用户的其他设备，实现多端已读同步。

---

## 7. 一致性保证与边界

写扩散提供的是**最终一致性**，而不是强一致性。在 Task 完成写扩散之前，接收者的 Inbox 中还没有这条事件，但这个窗口期通常很短（毫秒级）。

系统不保证 Inbox 中的事件顺序与 `seq_id` 完全对应。由于 NATS 的 at-least-once 语义和 Task 的并发消费，同一会话的不同事件可能以不同顺序写入 Inbox。客户端应该按 `seq_id` 排序渲染，而不是按 Inbox 的 `id` 顺序。

写扩散不处理会话成员变更的边界情况。如果用户在消息发送后被移出会话，他的 Inbox 中仍然会有这条消息（因为 `target_usernames` 在 Logic 处理时已经确定）。这是当前阶段的简化处理，未来可以在 Task 侧增加成员资格校验。

---

## 8. 阅读建议

| 文档 | 内容 |
| ---- | ---- |
| `02-database.md` | t_inbox 表结构与索引设计 |
| `12-task.md` | Task 消费语义与写扩散执行 |
| `20-message-flow.md` | 写扩散在消息主链路中的位置 |
| `23-offline-sync.md` | 离线补偿与多端同步的完整设计 |

---

## 9. 小结

写扩散让每个用户拥有独立的、可游标扫描的事件流，这是 Resonance 实现离线补偿和多端同步的基础。Inbox 同时承担推送兜底和离线数据源两个职责，客户端只需要维护一个游标值就能实现增量同步。已读状态由 `last_read_seq` 统一表达，避免了 Inbox 级别的写放大。只要写扩散的幂等性和"先写后推"的顺序约束保持成立，这套模型就能在任意失败场景下维持最终一致。
