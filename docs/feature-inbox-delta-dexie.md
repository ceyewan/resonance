# Feature: Inbox 增量同步 + Dexie 本地存储

## 目标

实现以下能力：

1. 首次登录后建立本地快照（会话 + 消息 + 同步游标）。
2. 后续刷新优先从 IndexedDB 渲染，不再全量拉取。
3. 在线阶段以 WS 为主，断线重连后基于 `inbox` 游标增量补齐。
4. 支持序列号不连续时触发增量补偿。

## 范围

### 后端

1. 基于 `t_inbox` 提供“按用户游标拉增量”的 API。
2. 修复 `SaveInbox` 幂等写入，避免重复写导致错误重试。
3. 为增量查询补充必要索引，确保按 `owner + id` 高效扫描。
4. 重命名为 `GetHistoryMessages`，并统一历史分页语义（`before_seq`）。

### 前端

1. 引入 Dexie，建立本地数据库（sessions/messages/sync_state）。
2. 启动时本地 hydrate -> 增量补齐 -> WS 实时。
3. WS push 落地到 Dexie，并更新内存状态。
4. 断线重连后自动触发 inbox 增量拉取。

## 分阶段计划

### Phase 1: 协议与后端最小闭环

1. 在 `logic/gateway` 的 SessionService 中新增 `PullInboxDelta` RPC。
2. 新增 `repo.MessageRepo` 能力：按用户名 + cursor 拉取 inbox 增量（含消息体）。
3. 任务侧 `SaveInbox` 改为幂等写（冲突忽略）。
4. 新增单测：
   - 重复写 inbox 不报错
   - cursor 增量拉取顺序正确
   - limit / has_more / next_cursor 正确

验收：

1. 用 curl/Connect 可按 cursor 拉到增量消息。
2. 重复消费不会导致 inbox 写失败重试风暴。

### Phase 2: 前端 Dexie 落地

1. 新增 `web/src/localdb`：Dexie schema 和访问层。
2. 启动时优先从 Dexie hydrate 到 Zustand。
3. 在 `useSession/useWsMessageHandler` 接入本地持久化。
4. 维护 `inbox_cursor`，增量补齐后更新游标。

验收：

1. 刷新页面能立即看到本地会话/消息。
2. 不触发全量拉取，仍可补齐离线消息。

### Phase 3: 重连与不连续补偿

1. 在 WS 重连成功后触发 `PullInboxDelta`。
2. 在消息处理路径加入“可选 gap 检测”，检测到不连续时触发增量补偿。
3. 增加日志与指标（补偿次数、补偿条数、耗时）。

验收：

1. 模拟断线期间发消息，重连后能自动补齐。
2. 模拟漏包（跳 seq），可自动恢复一致。

## 数据与接口设计草案

### 新增 RPC

`PullInboxDeltaRequest`

- `int64 cursor_id`：客户端已同步到的 inbox 主键游标（首次 0）
- `int64 limit`：批量大小
- `string username`：Logic 内部请求使用（Gateway 注入）

`PullInboxDeltaResponse`

- `repeated InboxEvent events`
- `int64 next_cursor_id`
- `bool has_more`

`InboxEvent`

- `int64 inbox_id`
- `resonance.gateway.v1.PushMessage message`

### Dexie 表设计

1. `sessions`: `&sessionId, updatedAt, maxSeqId`
2. `messages`: `&[sessionId+seqId], sessionId, timestamp, msgId`
3. `sync_state`: `&key`（`inbox_cursor`、`last_sync_at`）

## 风险与对策

1. 风险：`int64` 在前端精度丢失。
   - 对策：Dexie 层统一按字符串存储 `msgId/seqId/inboxId`。
2. 风险：WS 与增量并发导致重复插入。
   - 对策：按 `(sessionId, seqId)` 幂等 upsert。
3. 风险：inbox 长期增长。
   - 对策：后续加数据保留策略（按时间归档/清理）。

## 实施顺序（本分支）

1. Phase 1（先后端）
2. Phase 2（前端 Dexie）
3. Phase 3（重连补偿增强）

