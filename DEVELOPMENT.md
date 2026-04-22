# Resonance 开发路线图：Phase 5–8

> Phase 0–4（事件驱动骨架改造）已完成。本文档只跟踪 Phase 5 起的功能扩展阶段。
> 每个 Phase 完成后在对应的 `[ ]` 上打勾，并更新 `docs/` 对应文档。

---

## 当前进度一览

| Phase | 内容 | 状态 |
|-------|------|------|
| 0–4 | 事件驱动骨架（Proto / DB / Task / 身份） | ✅ 完成 |
| **5** | **消息撤回** | ✅ 完成 |
| **6** | **消息编辑 + 多端已读同步** | ✅ 完成 |
| 7 | AI Service V1（非流式） | ⬜ 待开始 |
| 8 | AI 流式响应 | ⬜ 待开始 |

---

## Phase 5：消息撤回 ✅

### 目标

验证事件驱动框架对"非消息"事件的端到端处理。撤回是最简单的"修改既有主事实"场景，走通它意味着 Logic → Task → Web 的统一事件链路已完全通用。

### 完成状态

| 层 | 状态 |
|---|---|
| Proto `ChatEvent.Recall` + `ChatRequest.recall` | ✅ |
| `repo.GetMessageByEventID` / `RecallMessageWithOutbox` | ✅ |
| `logic/service/recall.go` — 5 条业务校验 + Outbox 事务 | ✅ |
| `logic/service/chat.go` — SendEvent dispatch 分支 | ✅ |
| `gateway/logicclient` — 转发 recall payload 到 Logic | ✅ |
| Task `handler_recall.go` — 写扩散 + 推送 | ✅ |
| Frontend `sync/applier.ts` — recall 标记目标消息 | ✅ |
| Frontend 撤回 UI — 悬浮按钮 + 已撤回占位文字 | ✅ |

### 关键实现文件

- `logic/service/recall.go` — handleRecall 主逻辑
- `logic/service/recall_test.go` — 7 个单元测试
- `test/integration/recall_test.go` — 3 个集成测试（GoldenPath / PermissionDenied / OfflineSync）
- `web/src/features/chat/MessageBubble.tsx` — 撤回 UI

### 验证标准

- [x] 用户 A 撤回自己的消息 → 会话双方看到"此消息已撤回"
- [x] 用户 A 尝试撤回用户 B 的消息 → 收到 `PermissionDenied`
- [x] 用户 A 撤回 3 分钟前的消息 → 收到 `FailedPrecondition`
- [x] 撤回后重启 → 历史拉取中该消息仍显示"已撤回"（依赖 `recalled_at` 字段）
- [x] 断线重连后 PullInboxDelta → 补到 Recall 事件，状态正确恢复

---

## Phase 6：消息编辑 + 多端已读同步 ✅

### 完成状态

| 层 | 状态 |
|---|---|
| Proto `ChatRequest.edit` + `ChatEvent.Edit/ReadReceipt` | ✅ |
| `repo/message.go` — `EditMessageWithOutbox` 原子编辑 | ✅ |
| `repo/session.go` — `AdvanceLastReadSeqWithOutbox` 原子推进已读位点 | ✅ |
| `logic/service/edit.go` — 编辑业务校验 + Outbox 事务 | ✅ |
| `logic/service/chat.go` — SendEvent dispatch 接入 edit | ✅ |
| `logic/service/session.go` — UpdateReadPosition 产出 read receipt 事件 | ✅ |
| Task `handler_edit.go` / `handler_read.go` — 写扩散 + 推送 | ✅ |
| Frontend `sync/applier.ts` — edit / read receipt 状态应用 | ✅ |
| Frontend UI — 编辑入口、已编辑标记、单聊/群聊已读展示 | ✅ |
| 单元测试 + 集成测试 | ✅ |

### 关键实现文件

- `logic/service/edit.go` — handleEdit 主逻辑
- `logic/service/edit_test.go` — 编辑规则单测
- `logic/service/session.go` — UpdateReadPosition 补齐 read receipt 事件闭环
- `logic/service/session_update_read_position_test.go` — 已读回执单测
- `repo/message.go` — `EditMessageWithOutbox`
- `repo/session.go` — `AdvanceLastReadSeqWithOutbox`
- `test/integration/edit_test.go` — edit 实时 + 离线补拉集成测试
- `test/integration/read_receipt_realtime_test.go` — read receipt 实时 + 离线补拉集成测试
- `web/src/features/chat/MessageBubble.tsx` — 编辑入口、已编辑标记、消息级读状态
- `web/src/features/session-detail/SessionDetailPanel.tsx` — 会话级读状态摘要

### 验证标准

- [x] 发送者可以在 2 分钟窗口内编辑自己的文本消息
- [x] 非发送者、跨会话、已撤回消息、超时消息不能编辑
- [x] 编辑后目标消息主事实更新，且生成独立 Edit 事件进入 MQ / Inbox / WS 链路
- [x] 在线接收方实时看到编辑后的内容，离线重连后可通过 `PullInboxDelta` 恢复 Edit 事件
- [x] `UpdateReadPosition` 在读位点真正前进时产出 ReadReceipt 事件
- [x] ReadReceipt 同时支持实时推送与离线增量恢复
- [x] 单聊展示“已读/未读”，群聊展示“X 人已读”，详情侧栏展示读回执摘要

---

## Phase 7：AI Service V1（非流式）

> Phase 5 完成后可以并行准备，Phase 6 不是强依赖。

### 目标

走通"用户发消息给 AI Bot → AI 回复"端到端，不做流式。

### 准备工作（需要先完成）

- [ ] **`model/model.go` 补充常量**

  ```go
  const SessionKindAI = 1  // t_session.kind = 1 标记为 AI 会话
  ```

- [ ] **`bootstrap/bootstrap.go` 种子数据**
  - 插入一个 Bot 用户：`username=bot_default, nickname=AI 助手`（幂等）
  - 插入系统会话测试数据（可选）

- [ ] **补充 `docs/14-ai-service.md`**（在开始编码前写清楚边界）

### 任务清单

#### 新建 `ai/` 服务

- [ ] **`ai/ai.go`**：服务生命周期（参考 `task/task.go`）
- [ ] **`ai/consumer/consumer.go`**：订阅 MQ topic `resonance.chat.event.v1`，过滤 `session.kind == 1` 的事件（需查 session 表或让 MQEvent 携带 session kind）
- [ ] **`ai/engine/engine.go`**：模型调用封装
  - 先 hardcode 一个 provider（OpenAI 或 Anthropic SDK），通过 config 注入 API Key
  - 接口：`Generate(ctx, messages []Message) (string, error)`
- [ ] **`ai/sender/sender.go`**：以 Bot 身份调用 Logic `SendEvent`
  - 通过 gRPC 连接 Logic，在 metadata 注入 `x-username: bot_default`
  - Logic 侧不需要改动（Bot 是正常会话成员）

#### main.go

- [ ] 新增 `-module ai` 启动入口

#### 前端

- [ ] "新建 AI 会话"按钮：创建 `kind=1` 的会话
- [ ] UI 与普通会话一致（AI 回复的消息就是 Bot 用户发的普通消息）

### 验证标准

- [ ] 创建 AI 会话 → 发消息 → 等几秒 → 收到 Bot 回复
- [ ] 历史拉取 AI 会话能看到来回记录
- [ ] Bot 用户在 t_user 表中存在，不能被普通用户创建

---

## Phase 8：AI 流式响应

> Phase 7 完成后再开始。

### 当前已就位

- Proto 里 `WsPacket` 已预留 `StreamBegin/StreamChunk/StreamEnd` 的 packet 类型（Phase 1 占位）
- Push gRPC 的 `PushStream` 接口已占位

### 任务清单

#### 协议（确认已就位）

- [ ] 确认 `api/proto/gateway/v1/packet.proto` 的 stream 相关包已定义
- [ ] 确认 `api/proto/gateway/v1/push.proto` 的 `PushStream` RPC 已定义
- [ ] 如未完整实现，按 `docs/archive/architecture/04-flows.md` 流程 5 补充

#### Gateway

- [ ] **`gateway/pushserver/service.go`**：实现 `PushStream` RPC
  - 根据目标 username 找到本机在线连接
  - 把 StreamBegin/Chunk/End 封装成 WsPacket 下发

#### AI Service

- [ ] **`ai/engine/stream.go`**：流式输出封装
  - 流开始前：用 Snowflake 分配 `parent_event_id`
  - 调 Gateway `PushStream{StreamBegin, parent_event_id, users=[A]}`
  - 每个 token：`PushStream{StreamChunk, seq=i, delta=...}`
  - 模型输出完成：以 Bot 身份调 `Logic.SendEvent(Message{完整内容, event_id=parent_event_id})`
  - 随后：`PushStream{StreamEnd, parent_event_id}`

#### 前端

- [ ] `ws/dispatcher` 处理 StreamBegin/Chunk/End
- [ ] UI：维护流式消息缓冲区
  - StreamBegin → 新建 placeholder 消息（id=parent_event_id，内容为空）
  - StreamChunk → 追加 delta，实时渲染
  - StreamEnd → 停止追加，等待最终 ChatEvent
  - 收到最终 `ChatEvent{Message, event_id=parent_event_id}` → 替换 placeholder（保证 Inbox 一致性）

### 验证标准

- [ ] AI 回复时能看到逐字吐字效果
- [ ] 断网中断 → 重连 → 从 Inbox 拉到完整最终消息，无乱序
- [ ] 模型超时 → StreamEnd(error) → UI 显示"AI 回复失败"系统消息

---

## 各 Phase 完成后的文档更新要求

| Phase | 需更新的文档 |
|-------|------------|
| 5 | `docs/22-recall-edit-read.md`（将"框架已就位"改为"完整闭环"） |
| 6 | `docs/22-recall-edit-read.md` 同上；如 dispatcher 改动较大补 ADR |
| 7 | 新建 `docs/14-ai-service.md`（开始前先写） |
| 8 | 更新 `docs/14-ai-service.md` 补充流式链路 |

---

## 关键约束提醒（改动前必看）

1. **Logic 任何业务变更 + 事件发布必须在同一事务**：先写主表，再写 Outbox，事务外异步发 MQ。参考 `mqpublish.PublishMessageToMQ`。
2. **Task 永远先写 Inbox，后推送**：存储失败 NAK 重试，推送失败只记指标，不 NAK。
3. **seq_id 必须由 Redis 原子递增**：通过 `sequencer.Next(ctx, session_id)` 获取，Recall/Edit 事件同样需要占一个 seq_id（保证 Inbox 有序）。
4. **身份来自 context，不来自 body**：`MustUsernameFromCtx(ctx)`，不接受 req 里的 username 字段。
5. **提交前必须通过**：`make format && make lint`。
