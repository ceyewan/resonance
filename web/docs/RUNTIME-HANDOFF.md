# Runtime 接手说明

本文档面向维护 `web/` 前端业务逻辑、数据流和运行时的同学。

目标不是教页面怎么写，而是明确：

- 前端 runtime 分几层
- 每层负责什么
- UI 层与 runtime 层的接口应该怎么扩

## 1. 当前分层

```text
features / components
        │
        ▼
hooks / services
        │
        ▼
app/runtime + sync
        │
        ▼
api(ws/connect) + db(Dexie) + stores
```

### 各层职责

- `features/*`
  - 页面与业务组件组合
- `hooks/*`
  - 给页面层提供稳定读接口
- `services/*`
  - 给页面层提供稳定动作接口
- `app/runtime.ts`
  - 应用启动、WS 生命周期、Inbox Sync、Outbox flush 的总装配
- `sync/*`
  - 事件对账、Inbox 增量同步、`ChatEvent -> Dexie` 落库
- `api/*`
  - ConnectRPC client、WebSocket client、outbox 协议层
- `db/*`
  - Dexie schema 与本地 CRUD
- `stores/*`
  - 少量瞬时内存状态，不承担事件主数据源角色

## 2. 稳定接口给谁用

### 给页面层的稳定接口

优先暴露在：

- `src/hooks/index.ts`
- `src/services/index.ts`

当前页面层可依赖的稳定能力：

- `useAuthState()`
- `useConnectionState()`
- `useSessionListLive()`
- `useSessionTimeline(sessionId)`
- `useLoadHistory()`
- `useSendMessage()`
- `login()`
- `logout()`
- `restoreAuthSession()`
- `loadHistory()`
- `sendTextMessage()`
- `retryPendingMessage()`
- `markSessionRead()`
- `syncSessionList()`

### 不直接暴露给页面层的模块

- `Dexie`
- `db/repo.ts`
- `WsClient`
- `OutboxManager`
- `authClient` / `sessionClient`
- `sync/applier.ts`
- `sync/reconcile.ts`
- `api/ws/dispatcher.ts`

## 3. 现在的关键运行时链路

### 启动链路

1. `restoreAuthSession()`
2. `AppRuntime.start(token)`
3. `syncSessionList()`
4. 建立 WebSocket
5. `runInboxSyncThenFlushOutbox()`

### 发送链路

1. `sendTextMessage()`
2. 先写本地 pending event
3. 进入 `OutboxManager`
4. 通过 WS 发送
5. 收 ACK 更新 outbox 状态
6. 收正式 `ChatEvent` 后以服务端事件为准

### 收消息链路

1. `WsClient` 收包
2. `dispatchWsPacket()`
3. `reconcileWsEvent()`
4. `applyEvent()`
5. Dexie 更新后页面自动刷新

## 4. 扩展规则

新增前端能力时，优先按下面的方向扩展：

### 场景 1：只是多一个页面展示

直接复用现有 `hook / service`，不要改 runtime。

### 场景 2：页面需要一个新读模型

做法：

1. 先判断能否在现有 `hook` 上组合
2. 不行就新增一个 `hook`
3. 页面仍然不要直接读取 `db/api`

例子：

- 需要“会话在线状态摘要”
- 应新增 `useSessionPresence(sessionId)`
- 不要在页面里直接查 Dexie 或直接调 RPC

### 场景 3：页面需要一个新动作

做法：

1. 优先加到 `services/*`
2. 如果需要按钮态或提交流程，再在 `hooks/*` 包一层

例子：

- 撤回消息
- 新增 `services/chat.ts -> recallMessage(...)`
- 页面通过 `useMessageActions()` 或直接通过 service 调用

### 场景 4：后端新增事件类型

通常要改这些层：

1. `sync/applier.ts`
2. `db/schema.ts`
3. `services/session.ts` 或相关派生逻辑
4. `hooks/*` 的读模型聚合
5. 最后才是 `features/*` 的 UI

顺序不要反过来。

## 5. UI / Runtime 的接口边界

可以把现在的前端理解成两套协作面：

### A. UI/UX 面

负责：

- 页面结构
- 样式系统
- 交互细节
- 组件组合

依赖：

- `hooks`
- `services`
- 少量只读 `store`

### B. Runtime / Logic 面

负责：

- ConnectRPC
- WebSocket
- Dexie
- Outbox / ACK
- Inbox 增量同步
- 事件落库与幂等
- 状态机与容错

输出给 UI 的是：

- 稳定的读接口
- 稳定的动作接口
- 明确的状态语义

## 6. 目前最值得继续补的接口

如果后续还要继续补 runtime 文档，我建议优先补这三块：

1. `事件类型接入清单`
   - 新增 `Recall / Edit / Reaction / AI Stream` 时要改哪些文件
2. `前端测试说明`
   - Vitest、Dexie、WS、outbox 状态机怎么测
3. `故障排查说明`
   - runtime-config、WS 连接、Inbox 卡住、ACK 超时怎么查

## 7. 参考文件

- `src/app/runtime.ts`
- `src/api/ws/client.ts`
- `src/api/ws/outbox.ts`
- `src/sync/inbox.ts`
- `src/sync/reconcile.ts`
- `src/sync/applier.ts`
- `src/services/*.ts`
- `src/hooks/*.ts`
