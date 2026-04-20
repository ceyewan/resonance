# 08 · 前端架构重构规划

> 本篇只定"骨架"与"边界",不画像素级的 UI。视觉与交互一律**参考 Telegram Desktop / Web A**。
> UI 组件的具体用法、样式细节、布局尺寸等,都在落地过程中按 Telegram 的观感决定,不在本文约束。

---

## 0. 当前进度

| Step | 状态 | 备注 |
|------|:----:|------|
| S0 脚手架 | ✅ | Vite 6 + React 19 + TS 5.9 + Tailwind 4 + ESLint 9 flat;`@gen/*` alias 通过 symlink 指向 `api/gen/ts`(`preserveSymlinks: true`) |
| S1 ConnectRPC 底座 | ✅ | `api/transport.ts` + `api/clients.ts` + `lib/id.ts` 已落地;Connect-ES v2(`bufbuild/es:v2.11.0`,service 定义并入 `*_pb.ts`,已废弃 `connectrpc/es`);runtime config 链路(`/runtime-config.js` → `window.__RESONANCE_RUNTIME_CONFIG__`);`vite.config.ts` 配 dev middleware + `server.proxy` 兜底跨域;`createClient`(v2)替换 v1 `createPromiseClient` |
| S2 Dexie + Applier | ✅ | `db/schema.ts` + `db/repo.ts` + `sync/applier.ts` + `applier` 单测已落地,覆盖幂等/乱序/pending 覆盖/全部 oneof 分支 |
| S3 WebSocket 骨架 | ✅ | `api/ws/client.ts` + `api/ws/dispatcher.ts` + `stores/connection.ts` 已落地,包含心跳/指数退避重连/oneof 分发 |
| S4 Outbox + ACK 状态机 | ✅ | `api/ws/outbox.ts` 已落地，包含 5s 超时、3 次重发、failed 终态与 Dexie outbox 双写；仅对"已实际发出"的包启动 ACK 计时，连接不可用时保留 pending 待重连 flush |
| S5 Inbox 同步循环 | ✅ | `sync/inbox.ts` + `sync/reconcile.ts` 已落地；`runInboxSync` 分页串行拉取，失败不前移 cursor，并发 run 合并；WS/Inbox 合流复用 applier 幂等，并补齐 runtime/bootstrap、`services/*`、`hooks/*` 供 S6/S7 直接消费 |
| S6 鉴权页 + 路由 | ✅ | TanStack Router 路由树落地(`/login` `/register` `/chat` `/chat/$sessionId` `/contacts` `/settings`);`features/auth/{Login,Register}Page.tsx` + `useAuthGuard` hook;`App.tsx` 精简为仅挂载路由(smoke demo 已删除) |
| S7 三栏聊天 MVP | ✅ | `features/chat/{ChatLayout,ChatRoom,SessionRail,MessageList,MessageBubble,Composer}.tsx` + `features/session-detail/SessionDetailPanel.tsx` 落地;`ChatRoom` 以组件组合方式消费 `MessageList + Composer`;发送链路贯通 outbox,失败可重试;`useSessionListLive` / `useSessionTimeline` 订阅 Dexie;`useAutoMarkRead` 节流触发 `UpdateReadPosition`,使未读角标随浏览自动清零 |
| S8 会话管理(对齐现有后端) | 🟡 部分 | 已完成:联系人列表、搜索、创建单聊/群聊(`features/contact/ContactsPage.tsx` + `useContactDirectory`);未读角标 UI;基础消息气泡(文本、时间戳、发送状态、重试入口);Settings 页展示 profile/连接状态/登出。**暂停**:撤回/编辑 UI(依赖 Phase 5)、多端已读同步 UI(依赖 Phase 6)、@提及高亮、回复引用折行 |
| S9 AI 流式 + 打磨 | ⏸ 暂停 | 依赖后端 Phase 7/8(AI Service 尚未实现);TanStack Virtual 虚拟列表、骨架屏、E2E 同步推迟 |

**S6/S7/S8 落地细节补充**:

- `App.tsx` 已由 smoke demo 收敛为纯路由容器(`App.tsx` 仅挂载 `LiquidGlassShaderPool` + `RouterProvider`);原 smoke demo 中的 auth/bootstrap/ws 验证逻辑由 `app/runtime.ts` + `useAuthGuard` 承接。
- 消息组件遵循 § 3 规定的 `MessageList / MessageBubble / Composer` 拆分,`ChatRoom` 只组合不写业务细节,符合 § 10 硬约束#10(组件纯度)。
- `useAutoMarkRead`(`hooks/useAutoMarkRead.ts`):500ms 防抖,页面隐藏时不触发,`lastDispatchedRef` 防重复,失败回滚到 0 允许重试。
- `features/session-list/` 旧版在重构中删除,其角色由 `features/chat/SessionRail.tsx` 取代。
- **已知技术债(不阻塞 S7/S8)**:`tsc -b`(即 `npm run build`)目前仍存在 10 处 pre-existing 类型错误(`outbox.test.ts`、`GlassCard.tsx` 的陈旧 `@ts-expect-error`、`db/schema.ts` EventRow 主键类型、`LoginPage.tsx` `GlassButton` props、`services/mock.ts`、`services/session.ts:54` 的 exhaustive switch),需在后续清理;`npm run type-check`、ESLint、Vitest 全部全绿(31 tests)。

版本基线(Protobuf 插件 + 前端运行时库)统一 pin 在 `api/README.md §"开发注意事项"`,升级时同步两处。

---

## 1. 目标与非目标

### 目标

1. **对齐事件驱动后端**:前端以 `ChatEvent` 为核心数据模型,和 `docs/architecture/00-overview.md` / `04-flows.md` 同构。
2. **本地优先 (local-first)**:消息与会话全量缓存在 IndexedDB,UI 从本地读,网络只负责同步增量。
3. **低延迟 + 可靠**:WebSocket 走快路径,`PullInboxDelta` 做离线补偿,两条路径共用同一套事件落库逻辑。
4. **类型安全到端**:ConnectRPC 生成的 TS 代码是唯一 DTO,业务层不手写接口类型,`any` 在 `api/` `sync/` 目录禁用。
5. **可维护**:分层 + feature 混合组织,状态与副作用集中,组件内不出现 WS / fetch / 本地存储细节。

### 非目标

- **不**自研 UI 组件库,也不追求和 Telegram 像素级一致。
- **不**做多端(移动原生 / 桌面 Electron),只做 Web。
- **不**引入微前端 / monorepo / SSR。

---

## 2. 技术栈

| 层 | 选型 | 说明 |
|----|------|------|
| 构建 | **Vite 6 + TypeScript 5** | 与后端 `make gen` 产物无缝衔接 |
| 框架 | **React 19** | 并发渲染、`use` hook,对流式/Suspense 友好 |
| 路由 | **TanStack Router** | 类型安全路由,天然支持 loader + search params |
| 服务端状态 | **TanStack Query**(+ `@connectrpc/connect-query` 待 S5+ 按需引入) | 封装 ConnectRPC 请求的缓存、重试、失效;暂未安装,S1 demo 直接裸调 `authClient` |
| 客户端状态 | **Zustand** | 连接状态、当前会话、草稿等瞬时 UI 状态 |
| 本地持久化 | **Dexie 4** (IndexedDB) + `dexie-react-hooks` | 会话列表、事件流、Inbox 游标;`useLiveQuery` 订阅 |
| WebSocket | 自写薄封装 | 断线重连、心跳、`client_seq` 追踪、ACK 超时重发 |
| 样式 | **Tailwind CSS 4** + **shadcn/ui** | 组件用 shadcn 复制入库,自由改,不锁依赖 |
| 图标 | **lucide-react** | |
| 表单 | **react-hook-form** + **zod** | 登录注册、创建群聊 |
| 虚拟列表 | **TanStack Virtual** | 消息列表、会话列表 |
| Markdown / 富文本 | **react-markdown** + `remark-gfm` | AI 流式输出渲染 |
| 时间 | **date-fns** | tree-shake 友好 |
| 测试 | **Vitest** + **Playwright** | 单测 + E2E;WebSocket/Dexie 走真实实现 |
| Lint / 格式 | **ESLint 9 (flat)** + **Prettier** | 已有 `make lint/format` 链路兼容 |

> **保留项**:`api/gen/ts/`(Protobuf 生成的 TS)是前后端契约唯一来源,前端通过 `tsconfig` path 直接引用,不复制、不改动。

### 2.1 运行时配置链路

Web 镜像**一次构建、多环境复用**,不把 API / WS 地址打进 bundle。配置在容器启动时注入,链路如下:

```
.env (RESONANCE_WEB_API_BASE_URL / RESONANCE_WEB_WS_BASE_URL)
  │
  ▼ docker-compose env_file 注入到 web 容器
web 容器环境变量
  │
  ▼ webserver/web.go 的 GET /runtime-config.js 端点响应
window.__RESONANCE_RUNTIME_CONFIG__ = { apiBaseUrl, wsBaseUrl }
  │
  ▼ index.html 的 <script src="/runtime-config.js"> 在 main.tsx 之前同步执行
api/transport.ts / api/ws/client.ts 读取 window 全局,构造 transport / WebSocket
```

**约束**:

- `index.html` 里 `<script src="/runtime-config.js"></script>` 必须放在 module 脚本之前。module 脚本天然 `defer`,会等经典脚本执行完 + HTML 解析完再跑,顺序由浏览器保证。
- `vite-env.d.ts` 声明 `Window.__RESONANCE_RUNTIME_CONFIG__` 类型,业务代码不得直接 `as any` 读 window。
- 开发期由 `vite.config.ts` 的 dev middleware 兜底 `/runtime-config.js` 返回 `window.__RESONANCE_RUNTIME_CONFIG__ = {};`,此时 API base / WS base 都是空串,依靠 `server.proxy` 把 `/resonance.*` 和 `/ws` 打到 `localhost:8080`。
- 前端**不使用** `import.meta.env.VITE_*`:环境变量是 build-time 的,不符合"一次构建多环境复用"目标。

---

## 3. 目录结构

```
web/
├── src/
│   ├── api/                      # 网络层
│   │   ├── transport.ts          # ConnectRPC transport (createConnectTransport + JWT 拦截器)
│   │   ├── clients.ts            # AuthClient / SessionClient
│   │   ├── ws/
│   │   │   ├── client.ts         # WebSocket 封装:连接/重连/心跳
│   │   │   ├── dispatcher.ts     # WsPacket → 内部事件总线
│   │   │   └── outbox.ts         # 发送队列 + ACK 追踪
│   │   └── hooks.ts              # useLogin / useSessionList / useHistory ...
│   ├── db/
│   │   ├── schema.ts             # Dexie 表:sessions / events / outbox / meta
│   │   ├── repo.ts               # CRUD:applyEvent / upsertSession / ...
│   │   └── migrations.ts
│   ├── sync/
│   │   ├── applier.ts            # ChatEvent → Dexie 的纯函数应用逻辑
│   │   ├── inbox.ts              # PullInboxDelta 增量拉取循环
│   │   └── reconcile.ts          # WS 事件 / Inbox 事件合流去重
│   ├── stores/
│   │   ├── auth.ts               # currentUser / token
│   │   ├── connection.ts         # WS 状态机:idle/connecting/open/offline
│   │   └── ui.ts                 # 当前会话、侧栏折叠、主题
│   ├── features/
│   │   ├── auth/                 # 登录、注册页
│   │   ├── session-list/         # 左栏会话列表 + 搜索
│   │   ├── chat/                 # 中栏消息流 + 输入框
│   │   │   ├── MessageList.tsx
│   │   │   ├── MessageBubble.tsx
│   │   │   ├── Composer.tsx
│   │   │   └── TypingIndicator.tsx
│   │   ├── contact/              # 联系人、创建会话
│   │   ├── ai-chat/              # AI 会话的流式渲染增强
│   │   └── session-detail/       # 右栏会话信息
│   ├── components/               # 纯展示组件(Avatar / EmptyState / ...)
│   ├── hooks/                    # 跨 feature 复用的 hook
│   ├── lib/                      # 工具(formatTime / parseMention / logger)
│   ├── routes/                   # TanStack Router 路由树
│   ├── styles/                   # tailwind.css + 主题变量
│   ├── App.tsx
│   └── main.tsx
├── public/
├── index.html
├── tsconfig.json                 # paths: "@gen/*" → "../api/gen/ts/*"
├── vite.config.ts
├── tailwind.config.ts
└── package.json
```

**约束**:

- `features/*` 只依赖 `api/`、`db/`、`stores/`、`components/`、`lib/`。
- `api/` 不依赖 `features/`,不依赖 `db/`。
- `db/` 不依赖 `api/`(数据结构与网络解耦)。
- `sync/` 是 `api/` 与 `db/` 的唯一桥梁。

**当前已落地快照**(更新至 S6/S7/S8 部分落地):

```
web/src/
├── api/
│   ├── transport.ts          # ✅ createConnectTransport + JWT 拦截器 + runtime config baseUrl
│   ├── clients.ts            # ✅ createClient(AuthService/SessionService)
│   └── ws/
│       ├── client.ts         # ✅ WebSocket 封装：连接/重连/心跳
│       ├── dispatcher.ts     # ✅ WsPacket oneof 分发
│       └── outbox.ts         # ✅ 5s 超时 / 3 次重发 / failed 终态
├── app/
│   └── runtime.ts            # ✅ bootstrap + WS/Inbox/Outbox 接线
├── db/
│   ├── schema.ts             # ✅ Dexie 四表(sessions/events/outbox/meta)
│   └── repo.ts               # ✅ CRUD 封装(不暴露 Dexie.Table)
├── features/
│   ├── auth/
│   │   ├── LoginPage.tsx     # ✅ 登录表单 + token 写入
│   │   └── RegisterPage.tsx  # ✅ 注册表单(含昵称)
│   ├── chat/
│   │   ├── ChatLayout.tsx    # ✅ 左栏 SessionRail + 中/右栏 Outlet
│   │   ├── SessionRail.tsx   # ✅ 会话列表 + 搜索 + 未读角标(Dexie live)
│   │   ├── ChatRoom.tsx      # ✅ 头部 + MessageList + Composer + SessionDetailPanel
│   │   ├── MessageList.tsx   # ✅ Dexie 订阅 + 自动滚底 + 空态
│   │   ├── MessageBubble.tsx # ✅ 气泡 / 时间戳 / 发送状态 / 失败重试
│   │   ├── Composer.tsx      # ✅ textarea 多行 + Enter/Shift+Enter + 发送
│   │   └── EmptyChat.tsx     # ✅ /chat 首页占位
│   ├── contact/
│   │   └── ContactsPage.tsx  # ✅ 联系人列表 / 搜索用户 / 创建单聊或群聊
│   ├── session-detail/
│   │   └── SessionDetailPanel.tsx # ✅ 会话元信息 + 状态 + 预留操作按钮
│   └── settings/
│       └── SettingsPage.tsx  # ✅ Profile / 连接状态 / 登出
├── hooks/
│   ├── useAuthGuard.ts       # ✅ 未登录跳 /login,可选 bootstrap
│   ├── useAuthState.ts       # ✅ Zustand auth 浅订阅
│   ├── useAutoMarkRead.ts    # ✅ 节流调用 markSessionRead,未读角标自动清零
│   ├── useConnectionState.ts # ✅ 连接 + inboxSyncing 浅订阅
│   ├── useContactDirectory.ts # ✅ 联系人 + 搜索 + 创建会话业务 hook
│   ├── useLoadHistory.ts     # ✅ 按会话拉历史(beforeSeq 翻页)
│   ├── useSendMessage.ts     # ✅ 发送 / 重试包装
│   ├── useSessionListLive.ts # ✅ Dexie sessions 表实时订阅
│   └── useSessionTimeline.ts # ✅ Dexie 事件 + outbox 状态聚合
├── services/
│   ├── auth.ts               # ✅ 登录恢复 / 登出 / runtime 启停
│   ├── chat.ts               # ✅ pending 写入 / 发送 / 重试
│   ├── contact.ts            # ✅ GetContactList / SearchUser / CreateSession
│   └── session.ts            # ✅ session list / history / markSessionRead
├── sync/
│   ├── applier.ts            # ✅ ChatEvent 按 oneof 分发入库
│   ├── inbox.ts              # ✅ PullInboxDelta 分页同步(cursor + 可重入)
│   └── reconcile.ts          # ✅ WS/Inbox 统一入口,复用 applier 幂等
├── stores/
│   ├── auth.ts               # ✅ token/user/bootstrap 状态
│   └── connection.ts         # ✅ 连接状态机(idle/connecting/open/offline)
├── components/               # ✅ GlassCard / WallpaperBackground / LiquidGlassShaderPool ...
├── lib/
│   └── id.ts                 # ✅ bigint ↔ string 转换
├── styles/index.css          # ✅ Tailwind 4 + 主题 CSS 变量(light/dark)
├── gen/                      # 🔗 软链接 → ../../api/gen/ts(生成物,不改)
├── router.tsx                # ✅ TanStack Router 路由树
├── App.tsx                   # ✅ 纯路由容器(原 smoke demo 已删)
├── main.tsx
└── vite-env.d.ts             # ✅ Window.__RESONANCE_RUNTIME_CONFIG__ 类型
```

---

## 4. 数据流

```
           ┌────────── ConnectRPC (HTTP) ──────────┐
           │  login / session list / history      │
           │  createSession / search / inbox pull │
           ▼                                       ▼
     ┌──────────┐                           ┌──────────┐
     │ Features │                           │ sync/    │
     │   (UI)   │◀──── useLiveQuery ────────│ applier  │
     └─────▲────┘                           └────▲─────┘
           │                                      │
           │                                      │ applyEvent(ChatEvent)
     ┌─────┴─────┐  ┌──────────────┐        ┌─────┴─────┐
     │ Composer  │─▶│ api/ws/outbox│─WS────▶│  Dexie    │
     │  (send)   │  │  (client_seq)│◀───────│ (IDB)     │
     └───────────┘  └──────────────┘ event  └───────────┘
```

**四个要点**:

1. **UI 只读 Dexie,不读网络**。`useLiveQuery` 自动响应本地写入。
2. **事件入库有且仅有一条路径**:`sync/applier.applyEvent`,无论来自 WS 还是 `PullInboxDelta`。
3. **发送走 outbox**:Composer 先写本地 pending 事件(`client_msg_id` 占位) → WS 发 `ChatRequest` → 收到 `Ack` 后用正式 `event_id`/`seq_id` 覆盖。ACK 5s 未到自动重发,3 次后标 `failed` 并暴露重试入口。**只有成功调用 `ws.send()` 后才启动 ACK 计时**;断网或连接未就绪时 pending 事件只留在 `outbox` 表,等待重连后 flush。重复发送的去重依赖后端基于 `client_msg_id` 提供幂等保证。
4. **Inbox 拉取**:启动时 / 重连时,以本地 `meta.inbox_cursor_id` 起点循环 `PullInboxDelta` 直到 `has_more=false`。

---

## 5. Dexie 表结构

| 表 | 主键 | 索引 | 说明 |
|----|------|------|------|
| `sessions` | `session_id` | `[type]`, `[last_event_ts]` | 会话列表,字段对齐 `common.v1.SessionInfo` + 本地草稿 |
| `events` | `[session_id+seq_id]` | `event_id`, `[session_id+timestamp_ms]`, `client_msg_id` | `ChatEvent` 全量;pending 事件 `seq_id` 用负数占位 |
| `outbox` | `client_seq` | `session_id`, `status` | 待 ACK 的发送记录,与 WS outbox 双写 |
| `meta` | `key` | — | `inbox_cursor_id` / `me_username` / `last_ws_token` 等单值 |

**应用事件的不变式**(`sync/applier.applyEvent`):

- `Message` → 插入 `events`,更新会话 `last_event` / `unread_count`(若非本人且 `seq_id > last_read_seq`)。
- `MessageRecall` → 标记目标事件 `recalled=true`(软删,保留空间)。
- `MessageEdit` → 覆盖目标事件 `content`,保留 `edited=true`。
- `ReadReceipt` → 更新对方在本会话的 `read_upto_seq_id`(群聊用 map)。
- `SessionUpdate` → 更新 `sessions` 元信息。
- 同一 `event_id` 二次到达直接忽略(幂等)。

---

## 6. 核心流程

### 6.1 启动 / 登录后

1. Auth 恢复:从 `localStorage` 读 token → 调 `/me`(若无则重登录)。
2. HTTP 加载 `GetSessionList` → 写 Dexie `sessions`(覆盖式)。
3. 建 WebSocket(`?token=<jwt>` query 参数鉴权,浏览器原生 WS 不支持自定义 Header)。
4. 触发 `sync/inbox.run()`:以 `meta.inbox_cursor_id` 分页拉取,直到 `has_more=false`。
5. UI 订阅 Dexie,直接渲染。

### 6.2 发送消息

1. Composer 生成 `client_msg_id`,在 Dexie 插入 pending event(负数 `seq_id`,`status=sending`)。
2. WS 发 `WsPacket{ chat_request: { session_id, message }, client_seq }`。
3. 收到 `Ack{ ref_client_seq, event_id, seq_id }` → 用正式值覆盖 pending event;超时 (>5s) 标记 `failed`,UI 显示重试。
4. 之后可能再收到一条同 `event_id` 的 `ChatEvent`(写扩散/广播),`applier` 幂等忽略。

### 6.3 接收消息

- WS `ChatEvent` → `applier.applyEvent` → Dexie → UI 自动更新。
- 当前会话 + 可见 → 自动调 `UpdateReadPosition` 更新已读。

### 6.4 AI 流式

1. 收到 `StreamBegin{ parent_event_id, session_id }` → Dexie 插入一条占位消息 `status=streaming`。
2. 每条 `StreamChunk` → 追加 `delta` 到该消息(用 Dexie 事务保证顺序)。
3. `StreamEnd` → 标记 `status=done`,随后会收到正式的 `ChatEvent.Message` 覆盖占位。

### 6.5 离线 / 重连

- WS 断开 → 连接状态机进入 `offline`,UI 顶部提示。
- 重连成功 → 先跑一次 `sync/inbox.run()`,再 flush 本地 `outbox`。
- 网络稳定前,Composer 仍可写入,消息留在 `outbox` 等重发。

---

## 7. 路由与页面骨架

```
/                       → 重定向到 /chat 或 /login
/login                  → 登录
/register               → 注册
/chat                   → 三栏布局(左:会话列表;中:当前会话;右:详情抽屉,可折叠)
/chat/$sessionId        → 同上,定位到具体会话
/contacts               → 联系人 + 搜索 + 新建会话
/settings               → 个人设置(昵称/头像/登出)
```

三栏布局、顶部搜索、左下角头像菜单、消息气泡左右对齐、@提及高亮、回复引用折行——**全部按 Telegram Web A 的观感**。

---

## 8. 与后端协议的映射

| 后端概念 | 前端落点 |
|---------|---------|
| `AuthService.Login/Register/Logout` | `api/clients.ts` + `features/auth/` |
| `SessionService.GetSessionList` | 启动时全量加载 → Dexie `sessions` |
| `SessionService.GetHistoryEvents` | 打开会话时按需向上翻页(`before_seq`)|
| `SessionService.PullInboxDelta` | `sync/inbox.ts`,用 `meta.inbox_cursor_id` 作游标 |
| `SessionService.UpdateReadPosition` | 可见时节流调用 |
| `SessionService.GetContactList / SearchUser` | `features/contact/` |
| `WsPacket.chat_request` | Composer → `api/ws/outbox` |
| `WsPacket.event` | `applier.applyEvent` |
| `WsPacket.ack` | outbox 清 pending |
| `WsPacket.pulse` | 客户端 20s 心跳 |
| `WsPacket.stream_*` | `features/ai-chat/` |
| `WsPacket.typing` | `features/chat/TypingIndicator` |

**原则**:业务层只操作 `common.v1` 的类型,不要自定义镜像 DTO。

---

## 9. 关键决策与取舍

1. **React 19 而非 Solid/Svelte**:团队熟悉度 + ConnectRPC / TanStack 生态成熟度优先。
2. **Zustand + TanStack Query,不上 Redux**:IM 状态按"缓存 + 瞬时"分治,够用。
3. **Dexie 作为 UI 数据源,而不是 Zustand**:事件量大、需要持久化、支持复杂查询,内存态不合适。
4. **发送路径走 WS 而非 HTTP**:与 Gateway 现有 `ChatRequest` 协议一致,且可共享 ACK 机制。
5. **ConnectRPC 用 HTTP/1.1 + JSON**:浏览器直连简单,gRPC-Web 无必要。
6. **不做 SSR**:IM 是强登录态 + WS,SSR 收益极低。
7. **shadcn/ui 而非 Ant/Mantine**:样式完全可控,贴合 Telegram 观感更自由。

---

## 10. 工程硬约束

以下规则在代码评审中视为强约束,不符合直接退回:

1. **类型边界**:`api/` `sync/` `db/` 目录禁止出现 `any` / `as unknown as ...`;WS payload 一律用 `WsPacket.payload.case` 做 discriminated union 分发。
2. **发送可靠性**:`api/ws/outbox.send()` 返回 `Promise<Ack>`;ACK 超时 **5s**,自动重发 **3 次**,最终 `status=failed` 并对外暴露重试入口;不允许忽略返回值。
3. **pending 状态机**:`sending → acked`(正常)或 `sending → retrying → failed`(异常),不存在无主 pending。
4. **乐观更新规则**:要么操作本身幂等(如已读位点、单调递增),要么乐观写入时同步登记回滚闭包,失败分支必须执行。
5. **事件入库唯一通道**:`sync/applier.applyEvent(ChatEvent)` 是所有持久化事件的唯一入口,WS 与 Inbox 拉取共用,业务代码禁止直接 `db.events.put`。
6. **消息标识**:客户端用 `client_msg_id` 占位,服务端 `event_id` 覆盖;禁止多键拼凑去重。
7. **会话预览字段派生**:`sessions.last_event` 由 `applier` 从最新 `ChatEvent` 推导,业务代码不得直接写入。
8. **BigInt 封装**:`lib/id.ts` 是 bigint ↔ string 转换的唯一出口,其他地方不得直接 `.toString()`/`BigInt(...)`。
9. **sync 状态位置**:同步进度 / 重试计数存 Dexie `meta` 表或 `stores/connection.ts`,禁止模块级 `Map` / `let` 存跨调用状态。
10. **组件纯度**:`features/*/components/*.tsx` 不得出现 `fetch` / `connect-web` / `dexie` / WebSocket 引用,只消费 hook 与 store。

---

## 11. 落地 Step 与模型分派

从零开始,共 10 个 Step。每步独立交付、独立验收,前一步完成后再进入下一步。推荐模型一栏是建议,实际可替换。

> **总体策略**:涉及 Protobuf 契约、状态机、并发/可靠性的"契约型业务代码"走 Codex;涉及视觉、布局、动效、组件组合的"交互型 UI 代码"走 Gemini;疑难 bug(WS 抖动、Dexie 并发、BigInt 精度、oneof 分发出错)直接上 gpt-5.4 xhigh。

| Step | 目标 | 关键产出 | 验收 | 推荐模型 |
|------|------|----------|------|---------|
| **S0** ✅ | 脚手架 | Vite 6 + TS 5 + Tailwind 4 + ESLint flat;`tsconfig paths: "@gen/*" → "../api/gen/ts/*"`;`/` 显示空白但能跑 | `npm run dev` 打开,`npm run type-check` 与 `npm run build` 均通过 | **Codex (gpt-5.3-codex)**:配置型,不容易发明 API |
| **S1** ✅ | ConnectRPC 底座 | `api/transport.ts`(`createConnectTransport` + JWT 拦截器,baseUrl 从 `window.__RESONANCE_RUNTIME_CONFIG__.apiBaseUrl` 读取) + `api/clients.ts`(`createClient(AuthService, transport)`,Connect-ES v2 起 service 定义并入 `*_pb.ts`);`lib/id.ts` bigint 封装;`src/gen` 软链接指向 `api/gen/ts`(解决 symlink 外依赖解析问题);`index.html` 加载 `/runtime-config.js`,`vite.config.ts` 配 dev middleware + dev proxy | 手写一个调用 `AuthService.Login` 的 demo 页,能跑通 | **Codex** |
| **S2** ✅ | Dexie + Applier | `db/schema.ts`(sessions/events/outbox/meta)+ `db/repo.ts` + `sync/applier.ts`;**单测**覆盖所有 `ChatEvent` oneof 分支 + 幂等 | `vitest` 跑 `applier` 单测全绿,包含重复 `event_id`、乱序 `seq_id`、pending 覆盖三种用例 | **Codex**:纯业务逻辑 + 强单测,最适合 |
| **S3** ✅ | WebSocket 骨架 | `api/ws/client.ts`(连接 / 心跳 / 指数退避重连 / 状态机) + `api/ws/dispatcher.ts`(oneof 分发) + `stores/connection.ts` | 断网重连、心跳保活可手工验证;dispatcher 对未知 case 编译报错 | **Codex** 主写,如遇重连抖动问题上 **gpt-5.4 xhigh** |
| **S4** ✅ | Outbox + ACK 状态机 | `api/ws/outbox.ts`:`send()` 返回 `Promise<Ack>`,5s 超时、3 次重发、`failed` 终态;与 Dexie `outbox` 表双写 | 单测:网络正常 ACK、ACK 超时、多次重发、最终失败四条路径 | **Codex**:这是整个系统最易错的部分,务必强约束 + 测试 |
| **S5** ✅ | Inbox 同步循环 | `sync/inbox.ts`:启动 / 重连时按 `meta.inbox_cursor_id` 分页拉到 `has_more=false`;`sync/reconcile.ts`:WS 与 Inbox 事件去重 | 单测模拟 Inbox 与 WS 同时到达,`events` 表无重复 | **Codex** |
| **S6** ✅ | 鉴权页 + 路由 | TanStack Router 路由树;`features/auth/` 登录注册页;`useAuthGuard` 守卫 | 能登录 → 跳 `/chat`;token 失效自动回 `/login` | **Gemini 2.5 Pro** |
| **S7** ✅ | 三栏聊天 MVP(视觉 + 交互) | `ChatLayout` + `SessionRail` + `ChatRoom(MessageList/MessageBubble/Composer)` + `SessionDetailPanel`;发送接收端到端贯通 Dexie 与 outbox;`useAutoMarkRead` 节流调用 `UpdateReadPosition` | 两个账号能互发文本,刷新消息仍在,未读角标自动清零,失败可重试 | **Gemini** |
| **S8** 🟡 | 会话管理(部分) | 已完成:联系人/搜索/创建单聊或群聊(`ContactsPage` + `useContactDirectory` + `services/contact.ts`)、未读角标 UI、基础消息气泡。**暂停**:回复、撤回(Phase 5)、编辑(Phase 5)、@ 提及高亮、多端已读 UI(Phase 6) | 当前可达:能创建新会话、发消息,收到对方消息未读自增 + 进入会话自动清零 | **Gemini**(UI 为主)+ **Codex**(撤回/编辑 applier 分支,待 Phase 5) |
| **S9** ⏸ | AI 流式 + 打磨 | `features/ai-chat` StreamBegin/Chunk/End 占位消息 + Markdown 渲染;TanStack Virtual 虚拟列表;骨架屏;错误边界;Playwright E2E 3~5 条主链路 | **推迟**:等后端 Phase 7/8(AI Service V1 + 流式协议)完成后再启动 | **Gemini**(AI 流式 UI + 动效)+ **Codex**(虚拟列表性能调校、E2E 脚本) |

> **S8/S9 暂停理由**:撤回/编辑/已读依赖后端 `docs/architecture/05-migration.md` 的 Phase 5/6,AI 流式依赖 Phase 7/8。前端 applier 与 WS dispatcher 的 `ChatEvent` oneof 已预留全部分支,后端一旦接通即可在 UI 层按需补齐,不会回改 `sync/` 与 `db/`。

### 分派速查

| 任务形态 | 模型 |
|---------|------|
| Tailwind 样式、Telegram 观感复刻、过渡动效、消息气泡排版 | Gemini |
| 契约代码(WS outbox、applier、sync、Dexie schema、ID 封装) | Codex |
| 偶发 bug(ACK 乱序、消息丢失、Dexie 并发写入、BigInt 精度) | gpt-5.4 xhigh |
| 组件组合 + 少量业务(登录表单、会话详情抽屉) | Gemini 或 Codex 均可 |

### 验收红线(每个 Step 通用)

- `npm run type-check` 全绿,无 `any`(除非注释说明原因)。
- ESLint 全绿,无 `eslint-disable` 堆积。
- 涉及 `api/ws/` 或 `sync/` 的改动必须配单测,覆盖正常 + 异常两条路径。
- 组件文件不得 import `fetch` / `@connectrpc/*` / `dexie` / WebSocket。

---

## 12. 参考

- 后端契约:`api/proto/**` 与生成的 `api/gen/ts/**`
- 事件模型:`docs/architecture/00-overview.md`、`01-protocol.md`
- 时序图:`docs/architecture/04-flows.md`
- UI 参考:Telegram Desktop / Telegram Web A(<https://web.telegram.org/a/>)
