# 撤回、编辑与已读事件

> 本文档描述 `recall`、`edit`、`read_receipt` 三类事件在统一 `ChatEvent` 模型中的位置、当前实现状态和处理边界。阅读完本文后，应该能回答三个问题：为什么这三类能力也要建模成事件；它们与 message 主链路的关系是什么；以及当前代码已经落地到什么程度。

---

## 1. 为什么把这些能力统一成事件

如果系统只把"消息内容"看作唯一对象，那么撤回、编辑和已读就会被迫长成三套独立协议、三条独立推送链路和三种独立同步逻辑。这样短期看似简单，长期会让前后端状态模型迅速分裂。

Resonance 选择把这些变化都统一建模成 `ChatEvent.payload` 的不同分支。这样做的好处是：Logic 生成的是统一事件，MQ 里流转的是统一事件，Task 写入 Inbox 的也是统一事件，前端本地状态应用层面对的仍然是统一事件。新增能力时，主要扩展的是 payload 分支和对应处理逻辑，而不是再创造一条新链路。

---

## 2. 三类事件在协议中的位置

当前 `ChatEvent.oneof payload` 已包含以下分支：

```text
ChatEvent.payload
  ├── message
  ├── recall
  ├── edit
  ├── read_receipt
  └── session_update
```

其中：

- `message` 表示新消息产生
- `recall` 表示某条既有消息被撤回
- `edit` 表示某条既有消息被修改
- `read_receipt` 表示某个用户的已读位点推进

这三个事件都依赖 `message` 主链路，但语义并不相同。`message` 创建的是一条新的主事实，而 `recall` 和 `edit` 是对既有主事实的派生变化，`read_receipt` 则主要推进的是会话成员状态。

---

## 3. recall（撤回）

### 3.1 语义

撤回事件的核心不是"删除一条消息"，而是"在系统里确认某条消息已被撤回"。对于历史拉取来说，权威状态应该体现在消息主表上；对于实时同步来说，在线客户端需要尽快知道有一条撤回发生了。

### 3.2 正确的职责拆分

当前代码里已经明确写出了边界：

- Logic 负责在主事务内更新 `message_content.recalled_at`
- Task 只负责把 Recall 事件写入 Inbox 并推送

`task/dispatcher/handler_recall.go` 中的注释已经写得很明确：

```go
// handleRecall 只负责写扩散 + 推送。
// 主事实(message_content.recalled_at)应在 Logic 主事务内更新后再发 MQ,Task 不做业务主事实变更。
```

这条边界非常重要，因为一旦让 Task 去改 `message_content`，异步重试和重复消费就会开始污染主事实。

### 3.3 当前实现状态

Recall 已完整打通端到端闭环：

- **Logic** — `logic/service/recall.go` 实现 5 条业务校验（消息存在、发送者匹配、会话匹配、未撤回、2 分钟窗口），在同一数据库事务内完成 `UPDATE recalled_at` 和写 `t_message_outbox`，并异步投递 MQ
- **Task** — `handler_recall.go` 完成写扩散和推送
- **Gateway** — `ChatRequest.recall` 字段已加入 proto，`logicclient.SendEvent` 转发 recall payload
- **前端** — 已撤回消息显示占位文字；自己发的消息 2 分钟内悬浮出现撤回按钮；`sync/applier.ts` 在收到 recall 事件时标记目标消息 `recalled: true`

---

## 4. edit（编辑）

### 4.1 语义

编辑事件表示一条已存在消息的内容发生变化。和撤回一样，它既有主事实层的一面，也有同步层的一面：

- 历史拉取时，需要知道当前最终内容以及是否被编辑过
- 在线客户端，需要及时收到一条 Edit 事件更新本地展示

### 4.2 正确的职责拆分

当前边界与 Recall 相同：

- Logic 负责更新 `message_content.content`、`edited_at`、`edit_count`
- Task 只负责写扩散 + 推送，不碰主事实

`task/dispatcher/handler_edit.go` 也明确写了这一点：

```go
// handleEdit 只负责写扩散 + 推送。
// 主事实(message_content.content / edited_at)应在 Logic 主事务内更新后再发 MQ,Task 不做业务主事实变更。
```

### 4.3 当前实现状态

Edit 已在 Phase 6 完整打通端到端闭环：

- **Gateway** — `api/proto/gateway/v1/packet.proto` 的 `ChatRequest` 已加入 `edit` 字段，`gateway/logicclient/services.go` 会把 `edit` payload 转发到 Logic
- **Logic** — `logic/service/chat.go` 已接入 `edit` 分支，`logic/service/edit.go` 完成业务校验（消息存在、发送者匹配、会话匹配、未撤回、仅文本可编辑、2 分钟窗口、内容实际变化）并构造 `ChatEvent{Edit}`
- **Repo** — `repo/message.go` 提供 `EditMessageWithOutbox(...)`，在同一事务内更新 `message_content.content / edited_at / edit_count` 并写入 `t_message_outbox`
- **Task** — `handler_edit.go` 负责写扩散和推送，不改主事实
- **前端 runtime** — `web/src/sync/applier.ts` 收到 edit 事件后更新目标消息内容并标记 `edited`
- **前端 UI** — `web/src/services/chat.ts`、`web/src/hooks/useSendMessage.ts`、`web/src/features/chat/MessageBubble.tsx` 已接入编辑动作、展示“已编辑”标记，并为本人消息提供编辑入口
- **测试** — `logic/service/edit_test.go` 与 `test/integration/edit_test.go` 已覆盖规则校验、实时推送、Inbox 落地与离线补拉

---

## 5. read_receipt（已读回执）

### 5.1 语义

已读回执和 Recall/Edit 不同，它不直接作用于消息主表，而是推进会话成员的已读位点。对系统来说，权威状态不是"某条消息被读了"，而是"某个用户在某个会话中已经读到哪个 seq_id"。

因此，已读的权威存储落在 `t_session_member.last_read_seq`，而不是 Inbox 上的某个 `is_read` 字段。

### 5.2 当前实现状态

ReadReceipt 也已在 Phase 6 打通完整闭环：

1. `logic/service/session.go:UpdateReadPosition` 先校验成员关系，再推进 `t_session_member.last_read_seq`
2. 当读游标真正前进时，Logic 生成新的 `event_id` / `seq_id`，构造 `ChatEvent{ReadReceipt}`
3. `repo/session.go` 通过 `AdvanceLastReadSeqWithOutbox(...)` 在同一事务内完成读位点推进和 `t_message_outbox` 写入
4. 事务提交后异步投递 MQ，由 Task 继续写扩散和在线推送
5. 客户端仍同步收到 `UpdateReadPositionResponse`，其中带回最新未读数

这意味着已读的两层语义都已成立：

- **权威状态**：`t_session_member.last_read_seq` 是服务端的真实读位点
- **统一事件同步**：`read_receipt` 会进入 MQ / Inbox / WebSocket 链路，供其他端或其他成员恢复与展示

当前各层实现如下：

- **Logic** — `logic/service/session.go` 仅在读位点单调前进时产出事件，避免重复 read receipt
- **Repo** — `repo/session.go` 提供原子推进接口，保证“读位点更新 + Outbox”一致性
- **Task** — `task/dispatcher/handler_read.go` 负责把 read receipt 写入 Inbox 并推送给在线目标
- **前端 runtime** — `web/src/sync/applier.ts` 在单聊中更新 `readUptoSeqId`，在群聊中更新 `readUptoSeqByUser`
- **前端 UI** — `web/src/features/chat/MessageBubble.tsx` 展示单聊“已读/未读”和群聊“X 人已读”，`web/src/features/session-detail/SessionDetailPanel.tsx` 展示会话级读状态摘要
- **测试** — `logic/service/session_update_read_position_test.go` 与 `test/integration/read_receipt_realtime_test.go` 已覆盖单调推进、无重复事件、实时推送、Inbox 落地与离线补拉

---

## 6. 这三类事件与 message 的关系

可以把它们理解成三种不同层级的变化：

| 事件类型 | 改变什么 | 权威状态落点 |
| -------- | -------- | ------------ |
| `message` | 新增一条消息主事实 | `t_message_content` |
| `recall` | 标记某条消息已撤回 | `t_message_content.recalled_at` |
| `edit` | 更新某条消息内容与编辑状态 | `t_message_content.content / edited_at / edit_count` |
| `read_receipt` | 推进某用户的会话已读位点 | `t_session_member.last_read_seq` |

它们的共同点是：都应该通过统一 `ChatEvent` 在异步链路中传播；不同点是：真正被谁视为主事实，需要分别落在不同的权威表上。

---

## 7. 前端视角

前端本地状态层（`web/src/sync/applier.ts`）已经按统一事件模型处理这三类变化：

- `recall`：标记目标消息为已撤回
- `edit`：更新目标消息内容并标记 `edited`
- `read_receipt`：更新本地会话的 `readUptoSeqByUser`

这说明前端同步骨架已经不再把这些能力当成特殊 RPC，而是把它们看成统一事件流中的不同分支。这正是 `ChatEvent` 设计的价值所在。

---

## 8. 当前实现边界总结

| 层 | message | recall | edit | read_receipt |
|---|---|---|---|---|
| 协议层 | ✅ | ✅ | ✅ | ✅ |
| Task 写扩散 + 推送 | ✅ | ✅ | ✅ | ✅ |
| Web 本地状态应用 | ✅ | ✅ | ✅ | ✅ |
| Logic 业务主链路 | ✅ | ✅ | ✅ | ✅ |
| 前端 UI | ✅ | ✅ | ✅ | ✅ |

Recall 在 Phase 5 中完成闭环；Edit 与 read_receipt 已在 Phase 6 中补齐为完整事件链路。

---

## 9. 阅读建议

| 文档 | 内容 |
| ---- | ---- |
| `01-protocol.md` | ChatEvent 的统一协议设计 |
| `11-logic.md` | 主事实应该在哪一层成立 |
| `12-task.md` | Recall/Edit/ReadReceipt 在 Task 中的处理位置 |
| `13-web.md` | 前端如何应用这些事件 |

---

## 10. 小结

撤回、编辑和已读并不是附加在消息链路旁边的零散能力，而是统一事件模型中必须正视的会话变化。当前系统已经把它们全部接入统一 `ChatEvent` 骨架，并在 Logic、Task 与 Web 三层形成稳定闭环：主事实在 Logic 成立，Task 只做写扩散与推送，前端只消费统一事件。后续继续扩展新的会话事件类型时，仍应沿用这条边界，而不是再长出旁路协议或特判链路。
