# 离线补偿与重连同步

> 本文档描述 Resonance 的离线补偿、Inbox 游标拉取和重连恢复语义。阅读完本文后，应该能回答三个问题：用户离线期间的事件如何被补回来；为什么系统用 Inbox 自增主键作为游标；以及前端在重连后如何把 WebSocket 推送和增量拉取重新对齐。

---

## 1. 设计目标

离线同步解决的是一个非常具体的问题：用户在断网、切后台、浏览器刷新或 WebSocket 断开期间，系统里发生的消息、撤回、编辑、已读回执和会话更新，如何在用户重新连上来之后完整恢复。

Resonance 不把 WebSocket 推送当作唯一真实来源，而是把 Inbox 作为离线补偿的权威数据源。推送只是在线优化路径，离线补偿必须回到可持久化、可游标扫描的存储视图上，这就是 `t_inbox` 的职责。

---

## 2. 为什么使用 Inbox 游标

系统没有选择"按会话分别拉取未读消息"，而是选择了"按用户维度拉取 Inbox 增量"。原因是用户离线期间错过的并不只有消息，还可能有撤回、编辑、已读回执和会话更新。如果按会话单独拉取，就需要在客户端维护多套游标和多种事件源，复杂度会迅速上升。

`Inbox.id` 是一个简单且稳定的全局游标。对同一个用户而言，它单调递增，天然适合做增量同步的断点续传标记。客户端只需要记住自己已经处理到哪个 `inbox_id`，下次连上来继续拉 `id > cursor` 的记录即可。

---

## 3. 服务端拉取语义

Logic 通过 `SessionService.PullInboxDelta` 暴露统一的增量拉取接口。当前实现位于 `logic/service/inbox.go`，核心流程如下：

```text
客户端请求 PullInboxDelta(cursor_id, limit)
  ├── 从 context 读取已认证 username
  ├── limit <= 0 时默认 100
  ├── limit > 500 时强制截断为 500
  ├── 调用 MessageRepo.GetInboxDelta(username, cursor_id, limit)
  ├── 对每条 Inbox.payload 反序列化成 ChatEvent
  ├── 组装 InboxEvent{inbox_id, event}
  └── 返回 {events, next_cursor_id, has_more}
```

`repo/message.go:GetInboxDelta` 的 SQL 语义非常直接：

```sql
SELECT * FROM t_inbox
WHERE owner_username = ? AND id > ?
ORDER BY id ASC
LIMIT ?
```

这条查询依赖 `idx_owner_id(owner_username, id)` 索引，是离线补偿链路最核心的读路径。

---

## 4. 响应语义

`PullInboxDeltaResponse` 返回三个关键字段：

| 字段 | 含义 |
| ---- | ---- |
| `events` | 本次返回的增量事件列表，每项包含 `inbox_id` 和完整 `ChatEvent` |
| `next_cursor_id` | 本批数据中最大的 `inbox_id`，客户端下次从这里继续 |
| `has_more` | 本次返回条数是否等于请求 limit，等于时说明可能还有下一页 |

这里的 `next_cursor_id` 不是单纯的"请求游标 + 返回条数"，而是由服务端根据真实返回结果计算出来的最大 `inbox_id`。这样即使中间存在空洞或过滤，客户端也始终以服务端给出的真实位置推进游标。

---

## 5. 前端重连同步流程

Web 前端通过 `InboxSyncManager` 统一管理离线补偿，当前实现位于 `web/src/sync/inbox.ts`。它的核心流程是：

```text
WebSocket 断线 → 自动重连
  └── 连接恢复后触发 InboxSyncManager.run()
        ├── 从 Dexie meta 表读取本地 cursor_id
        ├── 调用 sessionClient.pullInboxDelta({cursorId, limit})
        ├── 对每条 InboxEvent 执行 reconcileInboxEvent()
        │      └── applyEvent(ChatEvent)
        │            └── 写入 Dexie events / sessions 表
        ├── 更新本地 cursor_id = response.next_cursor_id
        └── 如果 has_more=true，继续下一轮拉取
```

这条路径和 WebSocket 推送路径最终汇聚到同一个 `applyEvent` 逻辑，因此无论事件来自实时推送还是离线补偿，最终写入本地状态的方式完全一致，不需要两套处理逻辑。

---

## 6. WS 推送与离线补偿如何配合

WebSocket 和 Inbox 拉取不是互斥关系，而是互补关系。

**在线时**：事件通常先通过 Gateway Push 实时送达客户端，客户端立即调用 `reconcileWsEvent` 应用到本地状态。

**断线后**：断线期间错过的事件不会通过推送补发，而是等连接恢复后由 `PullInboxDelta` 批量拉回。

**重连瞬间的竞争**：一个事件可能既通过实时推送到达，又出现在下一轮 Inbox 补偿里。这要求前端本地状态写入必须是幂等的。当前前端通过 `event_id`、`session_id + seq_id` 等键来更新同一条事件，避免重复渲染。

---

## 7. 事件类型与未读数

离线补偿返回的是完整事件流，不只包含消息。当前 `ChatEvent.payload` 已支持：

- `message`
- `recall`
- `edit`
- `read_receipt`
- `session_update`

但是未读角标只统计 `message`。`repo/message.go:GetUnreadMessageCount` 明确只统计 `event_type = InboxEventTypeMessage` 的记录。撤回、编辑、已读回执和会话更新虽然进入 Inbox 并参与同步，但不会增加未读数，避免 badge 被非消息事件污染。

---

## 8. 异常与边界情况

### 8.1 Inbox payload 反序列化失败

如果 `PullInboxDelta` 在 Logic 侧对某条 Inbox 的 `payload` 反序列化失败，当前实现会直接返回 `codes.Internal`。这是一个数据一致性错误，不应该被静默跳过，否则客户端会永久卡在同一个游标位置。

### 8.2 limit 控制

服务端默认 limit 为 100，上限为 500。这样做是为了避免用户长时间离线后一次性拉取过多数据，影响单次请求延迟和内存占用。客户端通过 `has_more` 循环拉取，直到拉空。

### 8.3 断线期间大量消息涌入

如果离线期间积累了大量事件，前端会分批拉取。因为游标推进是单向的，所以这条链路天然支持断点续传：即使中途再次断线，下次仍然从上一次成功写入的 `cursor_id` 继续。

---

## 9. 当前实现结构

| 文件 | 内容 |
| ---- | ---- |
| `logic/service/inbox.go` | `PullInboxDelta` 服务端实现 |
| `repo/message.go` | `GetInboxDelta` 与未读数统计 |
| `web/src/sync/inbox.ts` | InboxSyncManager |
| `web/src/sync/reconcile.ts` | InboxEvent / WS 事件统一入口 |
| `web/src/sync/applier.ts` | ChatEvent → 本地 Dexie |

---

## 10. 阅读建议

| 文档 | 内容 |
| ---- | ---- |
| `21-write-fanout.md` | Inbox 为什么作为离线补偿数据源 |
| `13-web.md` | 前端本地状态与同步实现 |
| `20-message-flow.md` | 在线消息与异步扩散主链路 |

---

## 11. 小结

Resonance 的离线补偿建立在 `t_inbox` 这条用户事件流之上。客户端通过 `Inbox.id` 作为单一游标，统一拉取所有错过的事件；服务端负责按用户维度返回完整事件流，前端则把 WS 推送和 Inbox 拉取统一汇聚到同一个本地状态更新逻辑。只要 Inbox 始终先于推送写入，离线补偿就始终成立。
