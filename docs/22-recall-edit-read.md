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

当前 Task 已具备 Recall 事件的消费、写扩散和推送能力，前端同步模型也已能承接这类事件。但 Logic 主链路当前完整打通的主要还是 `message`，Recall 更接近"异步层框架已就位，业务闭环仍在补齐"的状态。

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

和 Recall 一样，Task 侧已经具备 Edit 事件的处理框架和 Inbox 落地能力，但 Logic 侧并未像 message 那样形成完整业务闭环。因此当前更准确的描述是：协议、异步消费和前端承接骨架已经存在，真正的业务闭环仍在逐步补齐。

---

## 5. read_receipt（已读回执）

### 5.1 语义

已读回执和 Recall/Edit 不同，它不直接作用于消息主表，而是推进会话成员的已读位点。对系统来说，权威状态不是"某条消息被读了"，而是"某个用户在某个会话中已经读到哪个 seq_id"。

因此，已读的权威存储落在 `t_session_member.last_read_seq`，而不是 Inbox 上的某个 `is_read` 字段。

### 5.2 当前实现状态

`logic/service/session.go:UpdateReadPosition` 已经实现了已读位点更新的主链路：

1. 从 context 中读取已认证 username
2. 调用 `sessionRepo.UpdateLastReadSeq` 推进 `last_read_seq`
3. 查询当前未读数并返回给客户端

这说明已读的同步 Ack 语义已经成立：客户端调用 `UpdateReadPosition` 成功后，服务端的权威已读状态已经更新。

与此同时，Task 侧也已经具备 `read_receipt` 事件的写扩散能力：`task/dispatcher/handler_read.go` 会把这类事件写入 Inbox。也就是说，已读事件的异步同步框架已经到位。

不过从当前代码可见，`UpdateReadPosition` 这条同步路径主要完成的是权威状态更新和未读数返回；关于它是否已经完整接入统一 MQ 事件闭环，需要谨慎描述为"框架与测试已就位，统一事件闭环仍在继续完善"，而不是简单说成已经完全成熟。

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

当前阶段最准确的描述是：

- 协议层：三类事件都已进入 `ChatEvent.payload`
- Task 层：三类事件都已具备写扩散和推送处理入口
- Web 层：三类事件都已具备本地状态应用入口
- Logic 层：完整打通最成熟的仍然是 `message`，Recall/Edit 的业务主链路还在补齐，ReadReceipt 的已读位点更新已经成立，但统一异步事件闭环仍在继续完善

这意味着系统已经拥有统一事件骨架，但不同 payload 的成熟度并不完全相同。

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

撤回、编辑和已读并不是附加在消息链路旁边的零散能力，而是统一事件模型中必须正视的会话变化。当前系统已经把它们接入了统一 `ChatEvent` 骨架，并在 Task 和 Web 侧建立了稳定的处理入口；真正仍在继续补齐的，主要是 Logic 侧不同 payload 的业务闭环成熟度。只要继续坚持"主事实在 Logic 成立，Task 只做扩散，前端只消费统一事件"这条边界，后续把这几类能力补完整并不需要再长出另一套系统。
