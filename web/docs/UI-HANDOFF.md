# UI/UX 接手说明

本文档面向接手 `web/` 前端页面开发的同学。
目标是让页面层直接消费稳定的 hook / service，不需要理解 Dexie、WebSocket、ConnectRPC 的底层细节。

如果你负责的是前端运行时、数据流、WS、Dexie、Outbox、Inbox Sync，请看 [RUNTIME-HANDOFF.md](./RUNTIME-HANDOFF.md)。

## 1. 当前状态

当前前端已补齐核心运行时，包含：

- 登录恢复与本地认证状态
- 会话快照同步
- WebSocket 连接管理
- Inbox 增量同步
- Outbox 发送与 ACK 状态机
- 本地 Dexie 数据源
- Timeline 的 pending / retry 状态聚合

当前前端已经切到正式路由结构，`src/App.tsx` 现在只是路由容器。
页面层的主要入口应直接看 `src/router.tsx` 与 `src/features/*`。

## 2. 页面层应该依赖什么

### 2.1 读状态：优先用 hooks

统一从 `src/hooks/index.ts` 导入：

- `useAuthState()`
  - 当前 token
  - 当前用户
  - 登录恢复状态
- `useConnectionState()`
  - WS 连接状态
  - Inbox 同步状态
  - 最近错误
- `useSessionListLive()`
  - Dexie 中的会话列表，已按最近时间排序
- `useSessionTimeline(sessionId)`
  - 当前会话的消息时间线
  - 已聚合 outbox 发送状态：`sending / retrying / acked / failed`
- `useLoadHistory()`
  - 拉取历史消息
- `useSendMessage()`
  - 发送消息
  - 重试失败消息

### 2.2 执行动作：优先用 services

统一从 `src/services/index.ts` 导入：

- `login(username, password)`
- `logout()`
- `restoreAuthSession()`
- `sendTextMessage({ sessionId, content, replyToEventId?, mentionedUsernames? })`
- `retryPendingMessage(sessionId, clientMsgId)`
- `loadHistory(sessionId, { limit?, beforeSeqId? })`
- `markSessionRead(sessionId, seqId)`
- `syncSessionList()`

## 3. 页面层禁止做什么

以下依赖不应该直接出现在 `features/*`、`routes/*`、页面组件里：

- `Dexie` / `db.*`
- `WsClient`
- `OutboxManager`
- `sessionClient` / `authClient`
- `dispatchWsPacket`
- `reconcile*`
- `applyEvent`

这些都属于运行时和数据层。
页面层应只消费 hook / service / store 的稳定接口。

## 4. 推荐页面接入顺序

### 4.1 鉴权页（S6）

登录页只需要：

- 提交时调用 `login(username, password)`
- 页面状态用 `useAuthState()`
- 登录成功后跳到 `/chat`

登出按钮只需要：

- 调用 `logout()`

### 4.2 应用入口

应用入口初始化时：

- 调用一次 `restoreAuthSession()`
- 使用 `useAuthState()` 判断是否已登录
- 未登录显示 `/login`
- 已登录显示 `/chat`

不需要页面层手动创建 WS 或做 Inbox Sync。
这些已经在 runtime 内完成。

### 4.3 会话列表（S7 左栏）

左栏只需要：

- `const sessions = useSessionListLive()`
- 展示：`name`、`lastEventPreview`、`unreadCount`
- 点击后切换 `sessionId`

页面层不需要自己调 `GetSessionList`。

### 4.4 消息列表（S7 中栏）

中栏只需要：

- `const timeline = useSessionTimeline(sessionId)`
- 渲染 `event.content`
- 如果 `sendState !== null`，显示发送态
  - `sending`
  - `retrying`
  - `failed`
- `failed` 时调用 `retry(sessionId, clientMsgId)`

### 4.5 输入框（Composer）

发送按钮只需要：

- `const { send } = useSendMessage()`
- 调 `send({ sessionId, content })`

不要自己生成 `client_msg_id`。
不要自己写 Dexie pending event。

### 4.6 历史消息上翻

打开会话或上翻加载时：

- `const { load } = useLoadHistory()`
- 初次可调 `load(sessionId)`
- 上翻时传 `beforeSeqId`

不要自己调 `sessionClient.getHistoryEvents()`。

## 5. 发送态约定

`useSessionTimeline(sessionId)` 返回的每条 timeline item 结构：

- `event`: Dexie 里的事件行
- `sendState`: 发送状态，可能为 `null`

当前可依赖的状态语义：

- `sendState === null`
  - 说明这是普通已落库事件，不是本端 pending 发送态
- `sendState.status === "sending"`
  - 已进入 outbox，等待发送/ACK
- `sendState.status === "retrying"`
  - ACK 超时后重试中
- `sendState.status === "failed"`
  - 本地保留失败态，可显示“重试”按钮
- `sendState.status === "acked"`
  - 已收到 ACK，但最终正式事件仍以服务端下发 `ChatEvent` 为准

## 6. 页面层可以依赖的 store

### `useAuthStore`

通常页面不需要直接用，优先走 `useAuthState()`。

### `useConnectionStore`

通常页面不需要直接用，优先走 `useConnectionState()`。
可用于：

- 顶部离线横幅
- 同步中指示
- 错误提示条

## 7. UI / Runtime 协作边界

当前前端其实分成两块协作面：

- `UI/UX`
  - 负责页面、布局、样式、组件组合、交互体验
- `Runtime / Logic`
  - 负责 ConnectRPC、WebSocket、Dexie、Outbox、Inbox Sync、状态机

页面层和 runtime 层之间的接口，统一收敛到：

- `hooks/*`
- `services/*`

如果你在页面开发时发现还缺接口，应该先补 `hook / service`，不要直接绕过接口层。

## 8. 运行时链路（页面只需知道，不需要自己实现）

当前运行时已经内置以下顺序：

1. 登录恢复或登录成功
2. `syncSessionList()`
3. 建立带 token 的 WebSocket
4. WS `open`
5. `runInboxSync()`
6. `outbox.flushPending()`
7. 收到新的 WS `event` 后自动落库

因此页面层无需自己管理以下问题：

- 何时拉 Inbox
- 何时 flush outbox
- WS 重连后如何补数据
- WS 和 Inbox 的去重

## 9. 建议的正式目录落点

后续 S6/S7 页面建议按以下方式组织：

- `src/features/auth/`
  - 登录 / 注册页
- `src/features/chat/`
  - 左栏会话列表、中栏消息区、输入框
- `src/features/session-detail/`
  - 右栏详情
- `src/components/`
  - 通用展示组件

页面层通过 hooks / services 调业务，不向下穿透到底层模块。

## 10. 当前可直接参考的文件

核心运行时：

- `src/app/runtime.ts`
- `src/services/auth.ts`
- `src/services/session.ts`
- `src/services/chat.ts`
- `src/sync/inbox.ts`
- `src/sync/reconcile.ts`

UI 接口层：

- `src/hooks/index.ts`
- `src/services/index.ts`

应用入口：

- `src/App.tsx`
- `src/router.tsx`

## 11. 接手建议

接手时建议顺序：

1. 先从 `src/router.tsx` 和对应 `src/features/*` 页面开始接手
2. 保留 `restoreAuthSession()` 作为应用启动动作
3. 登录页先接 `login()` / `logout()`
4. 左栏先接 `useSessionListLive()`
5. 中栏先接 `useSessionTimeline(sessionId)` 和 `useSendMessage()`
6. 再补历史上翻、已读、联系人等页面能力

如果页面开发过程中感觉需要直接 import Dexie、WS、ConnectRPC，通常说明应该先补 hook/service，而不是在页面里下钻。
