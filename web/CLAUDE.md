# Web Frontend Agent Guide

本文件面向在 `web/` 目录内工作的 agent / 开发者。

## 交流与目标

- 全程使用中文交流
- 目标是维护 Resonance 的 Web 前端
- 当前前端已完成运行时重建，重点是继续沿着既有分层扩展，而不是把页面逻辑重新写散

## 先看哪些文档

进入 `web/` 开发前，优先看：

1. [README.md](./README.md)
2. [docs/README.md](./docs/README.md)
3. [docs/UI-HANDOFF.md](./docs/UI-HANDOFF.md)
4. [docs/RUNTIME-HANDOFF.md](./docs/RUNTIME-HANDOFF.md)
5. [CLAUDE.md](./CLAUDE.md)

## 当前技术栈

- React 19
- TypeScript 5
- Vite 6
- Tailwind CSS 4
- TanStack Router
- ConnectRPC Web
- WebSocket
- Dexie
- Zustand
- Vitest

## 分层边界

前端当前有两条清晰协作面：

### 1. UI / UX 层

负责：

- 页面结构
- 视觉样式
- 交互细节
- 组件组合

应依赖：

- `src/hooks/*`
- `src/services/*`
- 少量只读 `src/stores/*`

不应直接依赖：

- `Dexie`
- `db/repo.ts`
- `WsClient`
- `OutboxManager`
- `authClient` / `sessionClient`
- `sync/applier.ts`
- `sync/reconcile.ts`
- `api/ws/dispatcher.ts`

### 2. Runtime / Logic 层

负责：

- ConnectRPC
- WebSocket
- Dexie
- Outbox / ACK
- Inbox 增量同步
- `ChatEvent` 落库与幂等
- 状态机与容错

对 UI 层输出：

- 稳定的读接口
- 稳定的动作接口
- 明确的状态语义

## 目录认知

```text
src/
├── api/         # ConnectRPC transport / clients / ws / outbox
├── app/         # runtime 总装配
├── components/  # 通用玻璃组件与背景
├── db/          # Dexie schema 与 repo
├── features/    # auth / chat / contact / settings / session-detail
├── hooks/       # 页面层稳定读接口
├── services/    # 页面层稳定动作接口
├── stores/      # 少量瞬时状态
├── sync/        # inbox / reconcile / applier
├── styles/      # design tokens 与全局样式
└── router.tsx
```

## 开发约束

### 页面层

- 优先补 `hook / service`，不要让页面直接下钻到底层
- 新页面优先复用已有 `useAuthState`、`useConnectionState`、`useSessionListLive`、`useSessionTimeline`、`useLoadHistory`、`useSendMessage`
- 不要在页面中自己生成 `client_msg_id`
- 不要在页面中自己写 pending event 到 Dexie

### 运行时层

- `sync/applier.applyEvent()` 是事件落库唯一入口
- 新增事件类型时，优先改 `sync/`、`db/`、`services/`，最后再改 UI
- 不要把事件主数据源放进 Zustand
- BigInt / string ID 转换统一走 `src/lib/id.ts`

### Zustand

- Zustand v5 取对象字面量时要注意稳定引用
- 需要对象聚合时优先用浅比较，避免 `Maximum update depth exceeded`

## 常用命令

```bash
cd web
npm ci
npm run dev
npm run type-check
npm run lint
npm run build
```

如果在仓库根目录工作：

```bash
make lint-web
```

## 修改前的判断原则

- 如果只是页面展示变化，尽量不要动 runtime
- 如果页面缺少一个读模型，优先新增 `hook`
- 如果页面缺少一个动作，优先新增 `service`
- 如果后端新增事件类型，先收口 runtime，再接 UI

## 当前已知状态

- 登录 / 注册 / 三栏聊天 / 联系人 / 设置页已落地
- Inbox 增量同步、Outbox ACK 重试、自动已读已接通
- Recall / Edit / 多端已读 UI / AI Stream 仍应等待后端对应接口或事件链路落地后再继续推进