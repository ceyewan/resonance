# Resonance Web - AI 开发助手指引

此文件用于指导 AI 开发助手在 Resonance Web 前端项目中的工作方式。

**语言**: 全程使用中文交流

---

## 角色设定

你是一位精通 **React + TypeScript** 的前端开发专家，专注于 IM（即时通讯）应用开发。

**核心能力**:

- 深入理解 React 18 特性：Hooks、并发模式
- 精通 TypeScript 类型系统（避免使用 `as any`）
- 熟悉 IM 应用前端架构：实时通信、状态同步、消息渲染
- 了解 ConnectRPC 和 Protobuf 在 Web 端的使用

**设计参考**: Telegram UI/UX

---

## 技术栈

| 类别    | 技术         | 版本               |
| ------- | ------------ | ------------------ |
| 框架    | React        | 18.3+              |
| 语言    | TypeScript   | 5.6+               |
| 构建    | Vite         | 5.4+               |
| 状态    | Zustand      | 4.5+               |
| 样式    | Tailwind CSS | 3.4+               |
| API     | ConnectRPC   | 1.4+               |
| 协议    | Protobuf     | @bufbuild/protobuf |

---

## 项目结构

```
web/
├── src/
│   ├── api/                     # API 通信层
│   │   └── client.ts            # ConnectRPC 客户端（带认证拦截器）
│   ├── components/              # React 组件
│   │   ├── Chat/                # 聊天相关组件
│   │   │   ├── ChatHeader.tsx    # 顶部导航栏
│   │   │   ├── ChatArea.tsx     # 聊天区域
│   │   │   └── SessionSidebar.tsx # 会话侧边栏
│   │   ├── ChatInput.tsx        # 消息输入框
│   │   ├── ConnectionStatus.tsx # 连接状态指示器
│   │   ├── ErrorBoundary.tsx    # 错误边界
│   │   ├── MessageBubble.tsx    # 消息气泡
│   │   ├── NewChatModal.tsx     # 新建聊天弹窗
│   │   └── SessionItem.tsx      # 会话列表项
│   ├── config/                  # 运行时配置
│   │   └── runtime.ts           # 动态配置加载
│   ├── constants/               # 常量定义
│   │   └── index.ts             # 消息类型、错误信息等
│   ├── gen/                     # Protobuf 生成代码（软链接 → ../api/gen/ts）
│   ├── hooks/                   # 自定义 Hooks
│   │   ├── useAuth.ts           # 认证 Hook
│   │   ├── useSession.ts        # 会话管理 Hook
│   │   ├── useWebSocket.ts      # WebSocket Hook（心跳/重连）
│   │   └── useWsMessageHandler.ts # WebSocket 消息处理
│   ├── lib/                     # 工具库
│   │   ├── avatar.ts            # 头像颜色生成
│   │   ├── cn.ts                # className 合并工具
│   │   └── time.ts              # 时间格式化工具
│   ├── pages/                   # 页面组件
│   │   ├── ChatPage.tsx         # 聊天主界面
│   │   └── LoginPage.tsx        # 登录/注册页
│   ├── stores/                  # Zustand 状态管理
│   │   ├── auth.ts              # 认证状态（持久化）
│   │   ├── session.ts           # 会话状态
│   │   └── message.ts           # 消息状态
│   ├── styles/                  # 全局样式
│   │   └── globals.css          # Tailwind + Liquid Glass 设计 tokens
│   ├── App.tsx                  # 应用入口
│   └── main.tsx                 # React 挂载
```

---

## 通信架构

### 整体架构

```
┌─────────────────┐         ┌─────────────────┐
│     Browser     │         │    Gateway      │
│                 │         │   (localhost    │
│  ┌───────────┐  │         │    :8080)       │
│  │   React   │  │         │                 │
│  │ ┌─────┐  │  │         │  ┌───────────┐  │
│  │ │ API │◄─┼─┼─────────┼─►│ ConnectRPC │  │
│  │ └─────┘  │  │ HTTP    │  │   HTTP    │  │
│  │ ┌─────┐  │  │         │  └───────────┘  │
│  │ │  WS │◄─┼─┼─────────┼─►│ WebSocket  │  │
│  │ └─────┘  │  │ WS      │  │ (Protobuf)│  │
│  └───────────┘  │         │  └───────────┘  │
└─────────────────┘         └─────────────────┘
```

- **ConnectRPC (HTTP)**: 登录、注册、获取会话列表等 API
- **WebSocket (Protobuf)**: 实时消息推送，二进制格式

### API 调用（ConnectRPC）

```typescript
// src/api/client.ts
import { createConnectTransport } from "@connectrpc/connect-web";
import { createPromiseClient } from "@connectrpc/connect";
import { AuthService, SessionService } from "@/gen/gateway/v1/api_connect";

// 带认证拦截器的 transport
const transport = createConnectTransport({
    baseUrl: import.meta.env.VITE_API_BASE_URL,
    interceptors: [
        (next) => async (req) => {
            const token = useAuthStore.getState().accessToken;
            if (token) req.header.set("Authorization", token);
            return await next(req);
        },
    ],
});

export const authClient = createPromiseClient(AuthService, transport);
export const sessionClient = createPromiseClient(SessionService, transport);
```

### WebSocket 消息

```typescript
// src/hooks/useWebSocket.ts
export function useWebSocket({ token, onMessage }: UseWebSocketOptions) {
    // 自动连接/断开
    // 心跳保活（30s）
    // 二进制 Protobuf 消息
}
```

---

## 状态管理

### 认证状态 (`stores/auth.ts`)

```typescript
interface User {
  username: string;
  nickname?: string;
  avatarUrl?: string;
}

interface AuthState {
  user: User | null;
  accessToken: string | null;
  isAuthenticated: boolean;
  isLoading: boolean;
  error: string | null;
  // Actions: setUser, setAccessToken, setAuth, logout
}
```

- 使用 `persist` 中间件持久化到 localStorage
- 登录/注册成功后存储 token 和用户信息

### 会话状态 (`stores/session.ts`)

```typescript
interface SessionInfo {
  sessionId: string;
  name: string;
  type: 1 | 2; // 1-单聊, 2-群聊
  avatarUrl?: string;
  unreadCount: number;
  lastReadSeq: number;
  maxSeqId: number;
  lastMessage?: {
    msgId: bigint;
    seqId: bigint;
    content: string;
    type: string;
    timestamp: bigint;
  };
}
```

### 消息状态 (`stores/message.ts`)

```typescript
interface ChatMessage {
  msgId: string;
  sessionId: string;
  fromUsername: string;
  content: string;
  msgType: "text" | "image" | "file" | "audio" | "video" | "system";
  timestamp: bigint;
  status: "sending" | "sent" | "failed";
  isOwn: boolean;
}
```

---

## UI 设计规范

### 设计语言：Liquid Glass

本项目采用 **Liquid Glass** 设计语言，结合 Telegram 的布局结构。

**核心特征**：
- **光学玻璃效果**：多层透明、模糊、饱和度增强
- **流动交互**：平滑的动画过渡和动态反馈
- **深度层次**：通过玻璃叠加创造空间感
- **明暗双主题**：完整支持明暗模式切换

### 布局结构

```
┌─────────────────────────────────────────────────────────────┐
│                        顶部导航栏                            │
│  [Logo]  Resonance  [状态]              [用户]  [登出]      │
├──────────────┬──────────────────────────────────────────────┤
│              │                                              │
│   会话列表   │              聊天区域                         │
│              │  ┌─────────────────────────────────────────┐│
│  [头像] 名字 │  │              消息列表                    ││
│  最后消息... │  │  ┌─────────────────────────────────┐   ││
│  [头像] 名字 │  │  │  对方: 你好！                   │   ││
│  最后消息... │  │  └─────────────────────────────────┘   ││
│              │  │  ┌─────────────────────────────────┐   ││
│   ...更多    │  │  │          我: 好的               │   ││
│              │  │  └─────────────────────────────────┘   ││
│              │  └─────────────────────────────────────────┘│
│              │                                              │
│              │  ┌─────────────────────────────────────────┐│
│              │  │ [输入消息...]                   [发送]  ││
│              │  └─────────────────────────────────────────┘│
└──────────────┴──────────────────────────────────────────────┘
```

### CSS 类命名规范

所有液态玻璃相关类以 `lg-` 前缀命名：

| 类名前缀 | 用途           | 示例                  |
| -------- | -------------- | --------------------- |
| `lg-glass` | 基础玻璃背景   | `lg-glass`            |
| `lg-glass-strong` | 强玻璃效果 | `lg-glass-strong`     |
| `lg-btn-*`   | 按钮样式       | `lg-btn-primary`      |
| `lg-input`    | 输入框样式     | `lg-input`            |
| `lg-bubble-*` | 消息气泡       | `lg-bubble-own`       |
| `lg-modal-*`  | 模态框         | `lg-modal-content`    |
| `lg-animate-*`| 动画效果       | `lg-animate-in`       |

---

## 开发规范

### 文件命名

| 类型       | 规范                | 示例              |
| ---------- | ------------------- | ----------------- |
| 组件文件   | PascalCase          | `MessageBubble.tsx` |
| Hook 文件  | camelCase，use 前缀 | `useAuth.ts`      |
| Store 文件 | camelCase           | `auth.ts`         |
| 工具文件   | camelCase           | `time.ts`         |

### 组件模板

```tsx
import { cn } from "@/lib/cn";

interface Props {
    // 定义 props
}

export function ComponentName({ prop }: Props) {
    return <div className={cn("base-styles", "conditional-styles")}>{/* JSX */}</div>;
}
```

### Hook 模板

```typescript
import { useState, useCallback } from "react";
import { useAuthStore } from "@/stores/auth";

export function useCustomHook(param: string) {
    const { accessToken } = useAuthStore();
    const [data, setData] = useState<DataType | null>(null);
    const [loading, setLoading] = useState(false);

    const refresh = useCallback(async () => {
        // 刷新逻辑
    }, [accessToken]);

    return { data, loading, refresh };
}
```

### Store 模板

```typescript
import { create } from "zustand";
import { persist } from "zustand/middleware";

interface State {
    data: DataType[];
    loading: boolean;
    setData: (data: DataType[]) => void;
    reset: () => void;
}

export const useStore = create<State>()(
    persist(
        (set) => ({
            data: [],
            loading: false,
            setData: (data) => set({ data }),
            reset: () => set({ data: [], loading: false }),
        }),
        { name: "store-name" },
    ),
);
```

---

## Protobuf 类型导入

```typescript
// API 服务
import { AuthService, SessionService } from "@/gen/gateway/v1/api_connect";

// 消息类型
import type { LoginRequest, LoginResponse, SessionInfo } from "@/gen/gateway/v1/api_pb";

// WebSocket 消息类型
import { WsPacket, ChatRequest } from "@/gen/gateway/v1/packet_pb";
```

### Protobuf 消息创建

```typescript
// 使用 fromJsonString 创建（推荐，支持 oneof 展开）
const packet = WsPacket.fromJsonString(
    JSON.stringify({
        seq: `msg-${Date.now()}`,
        chat: {
            sessionId: "session-123",
            content: "Hello!",
            type: "text",
        },
    }),
);

// 二进制序列化
const binary = packet.toBinary();
```

---

## 注意事项

### 1. BigInt 处理

Protobuf 的 `int64` 在 TypeScript 中是 `bigint`：

```typescript
// ❌ 错误：JSON.stringify 会报错
JSON.stringify({ msgId: message.msgId });

// ✅ 正确：转换为字符串
JSON.stringify({ msgId: message.msgId.toString() });
```

### 2. 类型安全

**避免使用 `as any`**，使用正确的类型定义：

```typescript
// ❌ 错误
const format = TIME_FORMAT.MESSAGE_TIME as any;

// ✅ 正确
const format: Intl.DateTimeFormatOptions = {
  hour: "2-digit",
  minute: "2-digit",
};
```

### 3. 生成代码不可修改

`src/gen/` 目录由 `make gen` 生成，**不要手动修改**。如需扩展类型，在 `src/types/` 或 `src/lib/` 中定义。

### 4. 环境变量

必须以 `VITE_` 开头：

```typescript
// ✅ 正确
const apiUrl = import.meta.env.VITE_API_BASE_URL;
const wsUrl = import.meta.env.VITE_WS_BASE_URL;

// ❌ 错误
const apiUrl = import.meta.env.API_BASE_URL;
```

### 5. 消息预览更新

发送/接收消息后，必须调用 `updateLastMessage` 更新会话列表的消息预览：

```typescript
// 发送消息时，乐观更新会话预览
const mockPushMessage = {
  msgId: BigInt(pendingMessage.msgId),
  seqId: pendingMessage.seqId,
  content: pendingMessage.content,
  type: pendingMessage.msgType,
  timestamp: pendingMessage.timestamp,
};
updateLastMessage(currentSession.sessionId, mockPushMessage as any);
```

---

## Git 提交规范

### 分支命名

- `feature/chat-ui` - 新功能
- `fix/message-render` - Bug 修复
- `refactor/websocket` - 重构

### 提交格式

```
feat(web): 实现消息列表虚拟滚动

- 使用 react-virtual 优化性能
- 添加消息懒加载
```

---

## 相关文档

- [README.md](./README.md) - 项目概述和快速开始
- [../README.md](../README.md) - 项目整体文档
- [../CLAUDE.md](../CLAUDE.md) - 后端开发规范
