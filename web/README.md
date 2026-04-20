# Web 前端

`web/` 是 Resonance IM 的浏览器端实现，负责鉴权页面、会话列表、聊天时间线、联系人与设置页，以及本地离线缓存与长连接运行时。

当前前端已完成一轮重建，主线能力是：

- `ConnectRPC + WebSocket` 双通道接入
- `Dexie + Zustand` 本地状态与离线缓存
- 三栏聊天界面与液态玻璃视觉系统
- Inbox 增量同步、Outbox ACK 重试、自动已读推进

## 当前实现效果

基于当前源码，前端已经不是 demo 壳子，而是一套可跑通核心 IM 链路的页面层。

### 已落地页面

1. `登录 / 注册`
   - 路由：`/login`、`/register`
   - 视觉上使用 Liquid Glass 风格：动态壁纸、半透明玻璃卡片、柔和高光与动效
   - 登录页提供 `Demo Mode`，便于在本地快速进入聊天界面
2. `聊天主界面`
   - 路由：`/chat`、`/chat/$sessionId`
   - 桌面端为三栏结构：
     - 左栏：会话列表、搜索、未读角标、联系人/新会话/设置入口
     - 中栏：消息时间线、发送框、发送中/重试中/失败态
     - 右栏：会话详情、连接状态、未读数、快捷操作占位
   - 小屏下左栏与右栏会收起，主聊天区优先显示
3. `联系人页`
   - 路由：`/contacts`
   - 已支持联系人浏览、用户搜索、发起单聊、创建群聊骨架
4. `设置页`
   - 路由：`/settings`
   - 已展示当前用户资料、运行时状态、退出登录入口

### 已接通的交互链路

- 登录成功后自动恢复运行时并同步会话
- 历史消息加载
- WebSocket 收消息并落本地库
- 本地插入 pending 消息
- ACK 成功后更新 outbox 状态
- ACK 超时自动重试，超过阈值标记失败
- 打开会话后自动推进已读位置

### 仍属于骨架或占位的部分

- 右侧详情栏当前以状态展示和操作占位为主，还不是完整资料面板
- 联系人页的群组创建表单已可调用后端，但 UI 仍偏“开发骨架”
- 目前页面主要围绕 `Message` 事件渲染，`Recall / Edit / Reaction / AI Stream` 还未形成完整 UI

## 技术栈

- `React 19`
- `TypeScript 5`
- `Vite 6`
- `Tailwind CSS 4`
- `TanStack Router`
- `ConnectRPC Web`
- `WebSocket`
- `Dexie`
- `Zustand`
- `Vitest`

## 目录结构

```text
web/
├── src/
│   ├── api/                  # ConnectRPC transport、客户端、WS 客户端与 outbox
│   ├── app/                  # 前端运行时入口
│   ├── components/           # 通用玻璃组件、背景与 shader
│   ├── db/                   # Dexie schema 与 repo 封装
│   ├── features/
│   │   ├── auth/             # 登录 / 注册
│   │   ├── chat/             # 三栏聊天主界面
│   │   ├── contact/          # 联系人 / 搜索 / 建群
│   │   ├── session-detail/   # 右侧详情栏
│   │   └── settings/         # 设置页
│   ├── hooks/                # 页面层稳定接口
│   ├── services/             # 业务动作封装
│   ├── stores/               # Zustand store
│   ├── sync/                 # Inbox 同步、事件落库、WS 去重/对账
│   ├── styles/               # 全局 design tokens 与基础样式
│   ├── App.tsx
│   ├── main.tsx
│   └── router.tsx
├── docs/
│   ├── README.md             # web 目录内部文档索引
│   ├── UI-HANDOFF.md         # 页面层接手说明
│   └── RUNTIME-HANDOFF.md    # 运行时 / 数据流 / 业务逻辑接手说明
├── package.json
└── README.md
```

## 运行时架构

页面层并不直接操作底层传输层，而是通过 `hooks / services` 消费稳定接口。

### 页面层建议依赖

- 读状态：`src/hooks/*`
- 执行动作：`src/services/*`
- 本地数据：仅由 `service / sync / runtime` 间接读写 Dexie

### 核心链路

#### 1. 启动链路

1. `restoreAuthSession()` 恢复 token 与当前用户
2. `AppRuntime.start()` 先同步会话列表
3. 建立带 token 的 WebSocket 连接
4. 连接成功后执行 `Inbox Sync`
5. `flushPending()` 补发 outbox 中未确认消息

#### 2. 发送消息链路

1. `sendTextMessage()` 先在 Dexie 插入一条本地 pending event
2. `OutboxManager` 为 `WsPacket` 分配 `clientSeq`
3. 通过 WebSocket 发送消息
4. 收到 ACK 后标记为 `acked`
5. 若超时则进入 `retrying`，超过阈值进入 `failed`
6. 服务端正式下发 `ChatEvent` 后，以服务端事件为准更新时间线

#### 3. 收消息链路

1. `WsClient` 接收二进制 `WsPacket`
2. `dispatchWsPacket()` 按 packet 类型分发
3. `reconcileWsEvent()` 做事件对账与去重
4. `applyEvent()` 把 `ChatEvent` 写入 Dexie
5. 页面通过 live query 自动刷新

## 本地存储模型

当前 Dexie 中包含 4 张表：

- `sessions`
  - 会话元数据、未读数、最后事件预览、已读位置
- `events`
  - 按 `sessionId + seqId` 存储事件时间线
- `outbox`
  - 客户端待发送、重试中、已 ACK、失败的消息
- `meta`
  - 当前用户名等运行时元信息

这套结构是对后端事件驱动模型的前端映射，目标是支持：

- 断线重连补偿
- 本地 pending 态
- 会话列表与时间线的增量更新

## 提交演进脉络

根据 `web/` 相关提交记录，当前实现大致按以下阶段推进：

| 阶段 | 关键提交 | 说明 |
|------|----------|------|
| 旧版阶段 | `233aeb7`、`dbd4207`、`2340533` | 早期会话已读、页面重构、注册昵称能力 |
| 重建起点 | `4e9e87e` | 删除旧前端目录，明确以重构后的事件驱动前端为起点 |
| S0 | `272fe3e` | 搭建 `Vite 6 + React 19 + TS 5 + Tailwind 4 + ESLint flat` |
| S1 | `70d8893`、`21286a9`、`9438557` | 建立 ConnectRPC 底座，升级 Connect-ES v2，补 runtime-config |
| S2 | `3ee3a0a` | 建立 Dexie schema 与事件 applier |
| S3 | `59944f2` | 完成 WebSocket 骨架 |
| S4 | `55bad16`、`42dab7d` | Outbox + ACK 状态机，补齐离线发送与 flush 语义 |
| Runtime 收口 | `5a32ff8` | 交付前端运行时内核 |
| S6 | `f908835`、`67e0a6a` | 鉴权页、路由骨架、基础 UI 下层 |
| S7 | `c01574a`、`f388ddd`、`892c8f3` | 三栏聊天 UI、对比度修复、自动已读与文档收尾 |

如果要理解当前结构为何这样拆，建议按这个顺序阅读提交。

## 开发命令

在仓库根目录先完成协议生成，再进入 `web/` 开发：

```bash
make gen
```

```bash
cd web
npm install
npm run dev
```

常用命令：

```bash
npm run dev
npm run build
npm run type-check
npm run lint
```

## 运行依赖

前端依赖后端 Gateway 提供：

- ConnectRPC HTTP 接口
- `/ws` WebSocket 入口
- `/runtime-config.js` 运行时配置注入（生产）

开发期默认走同源与 Vite 代理；生产期从 `window.__RESONANCE_RUNTIME_CONFIG__` 读取：

- `apiBaseUrl`
- `wsBaseUrl`

鉴权约定与后端文档保持一致：

- HTTP：`Authorization: Bearer <jwt>`
- WebSocket：`?token=<jwt>`

## 页面层开发约束

页面组件不要直接依赖这些底层模块：

- `Dexie`
- `sessionClient / authClient`
- `WsClient`
- `OutboxManager`
- `dispatchWsPacket`
- `applyEvent`

应优先使用：

- `useAuthState`
- `useConnectionState`
- `useSessionListLive`
- `useSessionTimeline`
- `useLoadHistory`
- `useSendMessage`

更详细的接手规范见 [docs/UI-HANDOFF.md](./docs/UI-HANDOFF.md)。

## 当前已知边界

- 前端当前重点是 IM 核心链路，不是完整产品化界面
- 消息渲染目前以文本消息为主，富媒体和复杂事件类型尚未补齐
- 移动端已经做了基础收缩，但交互仍优先按桌面三栏体验设计
- 事件驱动协议已接入前端基础设施，但更多事件类型仍需配合后端分阶段落地

## 相关文档

- [仓库根 README](../README.md)
- [架构总览](../docs/architecture/00-overview.md)
- [迁移计划](../docs/architecture/05-migration.md)
- [UI 接手说明](./docs/UI-HANDOFF.md)
