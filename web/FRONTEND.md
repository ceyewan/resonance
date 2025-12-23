# 🎯 Resonance IM 前端开发指南

## 📖 概述

本文档是 Resonance IM 系统的前端开发指南，涵盖技术选型、项目架构、开发规范和功能设计。前端采用 **React + TypeScript** 技术栈，与后端通过 **ConnectRPC (HTTP/JSON)** 和 **WebSocket (Protobuf)** 进行通信。

---

## 🛠️ 技术栈

### 核心框架

| 类别 | 技术 | 版本 | 说明 |
|-----|------|-----|------|
| **框架** | React | 18.x | 主框架 |
| **语言** | TypeScript | 5.x | 类型安全 |
| **构建工具** | Vite | 5.x | 极速 HMR，原生 ESM |
| **状态管理** | Zustand | 4.x | 轻量级状态管理 |
| **路由** | React Router | 7.x | 声明式路由 |

### UI 相关

| 类别 | 技术 | 说明 |
|-----|------|------|
| **组件库** | Shadcn/ui | 无依赖锁定，可定制 |
| **样式** | Tailwind CSS | 原子化 CSS |
| **图标** | Lucide React | 轻量图标库 |

### 通信层

| 类别 | 技术 | 说明 |
|-----|------|------|
| **HTTP API** | @connectrpc/connect-web | 类型安全的 RPC 调用 |
| **Protobuf** | @bufbuild/protobuf | 消息序列化 |
| **WebSocket** | 原生 WebSocket + Protobuf | 实时消息通信 |

### 开发工具

| 类别 | 技术 | 说明 |
|-----|------|------|
| **代码规范** | ESLint + Prettier | 代码质量保障 |
| **Git Hooks** | Husky + lint-staged | 提交前检查 |
| **测试** | Vitest | 单元测试 |

---

## 📁 项目结构

```
web/
├── FRONTEND.md              # 本开发指南
├── AGENTS.md                # AI 开发助手指引
├── package.json             # 项目依赖
├── vite.config.ts           # Vite 配置
├── tsconfig.json            # TypeScript 配置
├── tailwind.config.js       # Tailwind 配置
├── index.html               # 入口 HTML
├── .env.example             # 环境变量示例
├── .env.local               # 本地环境变量 (git ignored)
│
├── public/                  # 静态资源
│   └── favicon.ico
│
└── src/
    ├── main.tsx             # 应用入口
    ├── App.tsx              # 根组件
    ├── vite-env.d.ts        # Vite 类型声明
    │
    ├── api/                 # API 通信层
    │   ├── client.ts        # ConnectRPC 客户端配置
    │   ├── auth.ts          # 认证 API
    │   ├── session.ts       # 会话 API
    │   └── ws/              # WebSocket 模块
    │       ├── connection.ts    # 连接管理
    │       ├── protocol.ts      # 协议处理
    │       └── types.ts         # 类型定义
    │
    ├── stores/              # Zustand 状态管理
    │   ├── auth.ts          # 认证状态
    │   ├── session.ts       # 会话状态
    │   ├── message.ts       # 消息状态
    │   └── ui.ts            # UI 状态
    │
    ├── hooks/               # 自定义 Hooks
    │   ├── useAuth.ts       # 认证 Hook
    │   ├── useWebSocket.ts  # WebSocket Hook
    │   ├── useSession.ts    # 会话 Hook
    │   └── useMessage.ts    # 消息 Hook
    │
    ├── components/          # UI 组件
    │   ├── ui/              # Shadcn 基础组件
    │   │   ├── button.tsx
    │   │   ├── input.tsx
    │   │   ├── avatar.tsx
    │   │   └── ...
    │   ├── layout/          # 布局组件
    │   │   ├── Header.tsx
    │   │   ├── Sidebar.tsx
    │   │   └── Layout.tsx
    │   └── chat/            # 聊天业务组件
    │       ├── SessionList.tsx
    │       ├── SessionItem.tsx
    │       ├── MessageList.tsx
    │       ├── MessageItem.tsx
    │       ├── MessageInput.tsx
    │       └── ChatWindow.tsx
    │
    ├── pages/               # 页面组件
    │   ├── Login.tsx        # 登录页
    │   ├── Register.tsx     # 注册页
    │   ├── Chat.tsx         # 聊天主页
    │   └── NotFound.tsx     # 404 页
    │
    ├── lib/                 # 工具库
    │   ├── utils.ts         # 通用工具函数
    │   ├── storage.ts       # 本地存储封装
    │   ├── time.ts          # 时间处理
    │   └── cn.ts            # className 合并
    │
    ├── types/               # 类型定义
    │   └── index.ts         # 全局类型
    │
    └── gen/                 # 生成的代码 (软链接或复制)
        └── ...              # 指向 im-api/gen/ts/
```

---

## 🔧 环境配置

### 环境变量

在 `web/` 目录下创建 `.env.local` 文件：

```bash
# API 基础地址 (ConnectRPC)
VITE_API_BASE_URL=http://localhost:8080

# WebSocket 地址
VITE_WS_URL=ws://localhost:8080/ws

# 应用环境
VITE_APP_ENV=development
```

> **注意**: Vite 环境变量必须以 `VITE_` 开头才能在客户端代码中访问。

### 开发环境配置

```bash
# 进入前端目录
cd web

# 安装依赖
npm install

# 启动开发服务器
npm run dev

# 构建生产版本
npm run build

# 预览生产版本
npm run preview
```

---

## 📡 API 通信

### ConnectRPC 客户端配置

```typescript
// src/api/client.ts
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { AuthService, SessionService } from "@/gen/gateway/v1/api_connect";

const transport = createConnectTransport({
  baseUrl: import.meta.env.VITE_API_BASE_URL,
});

// 认证服务客户端
export const authClient = createClient(AuthService, transport);

// 会话服务客户端
export const sessionClient = createClient(SessionService, transport);
```

### 认证 API 封装

```typescript
// src/api/auth.ts
import { authClient } from "./client";
import type { LoginRequest, RegisterRequest } from "@/gen/gateway/v1/api_pb";

export async function login(username: string, password: string) {
  return authClient.login({ username, password });
}

export async function register(username: string, password: string, nickname: string) {
  return authClient.register({ username, password, nickname });
}

export async function logout(accessToken: string) {
  return authClient.logout({ accessToken });
}
```

### 会话 API 封装

```typescript
// src/api/session.ts
import { sessionClient } from "./client";

export async function getSessionList(accessToken: string) {
  return sessionClient.getSessionList({ accessToken });
}

export async function createSession(
  accessToken: string,
  members: string[],
  name: string,
  type: number
) {
  return sessionClient.createSession({ accessToken, members, name, type });
}

export async function getRecentMessages(
  accessToken: string,
  sessionId: string,
  limit: bigint,
  beforeSeq?: bigint
) {
  return sessionClient.getRecentMessages({
    accessToken,
    sessionId,
    limit,
    beforeSeq: beforeSeq ?? 0n,
  });
}

export async function getContactList(accessToken: string) {
  return sessionClient.getContactList({ accessToken });
}

export async function searchUser(accessToken: string, query: string) {
  return sessionClient.searchUser({ accessToken, query });
}
```

---

## 🔌 WebSocket 通信

### 连接管理

```typescript
// src/api/ws/connection.ts
import { create, toBinary, fromBinary } from "@bufbuild/protobuf";
import {
  WsPacketSchema,
  PulseSchema,
  ChatRequestSchema,
  AckSchema,
  type WsPacket,
  type ChatRequest,
  type PushMessage,
} from "@/gen/gateway/v1/packet_pb";

export type MessageHandler = (message: PushMessage) => void;
export type ConnectionStateHandler = (connected: boolean) => void;

export class WebSocketManager {
  private ws: WebSocket | null = null;
  private url: string;
  private token: string;
  private reconnectAttempts = 0;
  private maxReconnectAttempts = 5;
  private reconnectDelay = 1000;
  private heartbeatInterval: number | null = null;
  private messageHandlers: Set<MessageHandler> = new Set();
  private stateHandlers: Set<ConnectionStateHandler> = new Set();

  constructor(url: string, token: string) {
    this.url = url;
    this.token = token;
  }

  // 建立连接
  connect(): void {
    if (this.ws?.readyState === WebSocket.OPEN) return;

    // 将 token 作为查询参数传递
    const wsUrl = `${this.url}?token=${encodeURIComponent(this.token)}`;
    this.ws = new WebSocket(wsUrl);
    this.ws.binaryType = "arraybuffer";

    this.ws.onopen = () => {
      console.log("[WS] Connected");
      this.reconnectAttempts = 0;
      this.startHeartbeat();
      this.notifyStateChange(true);
    };

    this.ws.onmessage = (event) => {
      this.handleMessage(event.data);
    };

    this.ws.onclose = () => {
      console.log("[WS] Disconnected");
      this.stopHeartbeat();
      this.notifyStateChange(false);
      this.scheduleReconnect();
    };

    this.ws.onerror = (error) => {
      console.error("[WS] Error:", error);
    };
  }

  // 断开连接
  disconnect(): void {
    this.stopHeartbeat();
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
  }

  // 发送聊天消息
  sendChat(chat: Partial<ChatRequest>): void {
    const packet = create(WsPacketSchema, {
      seq: this.generateSeq(),
      payload: {
        case: "chat",
        value: create(ChatRequestSchema, chat),
      },
    });
    this.sendPacket(packet);
  }

  // 发送心跳
  private sendPulse(): void {
    const packet = create(WsPacketSchema, {
      seq: this.generateSeq(),
      payload: {
        case: "pulse",
        value: create(PulseSchema, {}),
      },
    });
    this.sendPacket(packet);
  }

  // 发送确认
  sendAck(refSeq: string): void {
    const packet = create(WsPacketSchema, {
      seq: this.generateSeq(),
      payload: {
        case: "ack",
        value: create(AckSchema, { refSeq }),
      },
    });
    this.sendPacket(packet);
  }

  // 注册消息处理器
  onMessage(handler: MessageHandler): () => void {
    this.messageHandlers.add(handler);
    return () => this.messageHandlers.delete(handler);
  }

  // 注册连接状态处理器
  onStateChange(handler: ConnectionStateHandler): () => void {
    this.stateHandlers.add(handler);
    return () => this.stateHandlers.delete(handler);
  }

  // 发送数据包
  private sendPacket(packet: WsPacket): void {
    if (this.ws?.readyState !== WebSocket.OPEN) {
      console.warn("[WS] Cannot send: not connected");
      return;
    }
    const data = toBinary(WsPacketSchema, packet);
    this.ws.send(data);
  }

  // 处理接收的消息
  private handleMessage(data: ArrayBuffer): void {
    try {
      const packet = fromBinary(WsPacketSchema, new Uint8Array(data));
      
      switch (packet.payload.case) {
        case "push":
          const pushMessage = packet.payload.value;
          this.messageHandlers.forEach((handler) => handler(pushMessage));
          // 自动发送确认
          if (packet.seq) {
            this.sendAck(packet.seq);
          }
          break;
        case "pulse":
          // 心跳响应，无需处理
          break;
        case "ack":
          // 消息确认，可用于更新消息发送状态
          console.log("[WS] Ack received:", packet.payload.value.refSeq);
          break;
      }
    } catch (error) {
      console.error("[WS] Failed to parse message:", error);
    }
  }

  // 心跳机制
  private startHeartbeat(): void {
    this.heartbeatInterval = window.setInterval(() => {
      this.sendPulse();
    }, 30000); // 30秒心跳间隔
  }

  private stopHeartbeat(): void {
    if (this.heartbeatInterval) {
      clearInterval(this.heartbeatInterval);
      this.heartbeatInterval = null;
    }
  }

  // 重连机制
  private scheduleReconnect(): void {
    if (this.reconnectAttempts >= this.maxReconnectAttempts) {
      console.error("[WS] Max reconnect attempts reached");
      return;
    }

    const delay = this.reconnectDelay * Math.pow(2, this.reconnectAttempts);
    console.log(`[WS] Reconnecting in ${delay}ms...`);
    
    setTimeout(() => {
      this.reconnectAttempts++;
      this.connect();
    }, delay);
  }

  // 生成序列号
  private generateSeq(): string {
    return `${Date.now()}-${Math.random().toString(36).substr(2, 9)}`;
  }

  // 通知状态变更
  private notifyStateChange(connected: boolean): void {
    this.stateHandlers.forEach((handler) => handler(connected));
  }
}
```

### WebSocket Hook

```typescript
// src/hooks/useWebSocket.ts
import { useEffect, useRef, useCallback } from "react";
import { WebSocketManager } from "@/api/ws/connection";
import { useAuthStore } from "@/stores/auth";
import { useMessageStore } from "@/stores/message";
import type { PushMessage, ChatRequest } from "@/gen/gateway/v1/packet_pb";

export function useWebSocket() {
  const wsRef = useRef<WebSocketManager | null>(null);
  const { accessToken, isAuthenticated } = useAuthStore();
  const { addMessage, setConnected } = useMessageStore();

  useEffect(() => {
    if (!isAuthenticated || !accessToken) {
      return;
    }

    const wsUrl = import.meta.env.VITE_WS_URL;
    const manager = new WebSocketManager(wsUrl, accessToken);
    wsRef.current = manager;

    // 注册消息处理器
    const unsubMessage = manager.onMessage((message: PushMessage) => {
      addMessage(message);
    });

    // 注册状态处理器
    const unsubState = manager.onStateChange((connected: boolean) => {
      setConnected(connected);
    });

    // 建立连接
    manager.connect();

    return () => {
      unsubMessage();
      unsubState();
      manager.disconnect();
    };
  }, [isAuthenticated, accessToken, addMessage, setConnected]);

  const sendMessage = useCallback((chat: Partial<ChatRequest>) => {
    wsRef.current?.sendChat(chat);
  }, []);

  return { sendMessage };
}
```

---

## 📦 状态管理

### 认证状态

```typescript
// src/stores/auth.ts
import { create } from "zustand";
import { persist } from "zustand/middleware";
import type { User } from "@/gen/common/v1/types_pb";

interface AuthState {
  accessToken: string | null;
  user: User | null;
  isAuthenticated: boolean;
  
  // Actions
  setAuth: (token: string, user: User) => void;
  logout: () => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      accessToken: null,
      user: null,
      isAuthenticated: false,

      setAuth: (token, user) =>
        set({
          accessToken: token,
          user,
          isAuthenticated: true,
        }),

      logout: () =>
        set({
          accessToken: null,
          user: null,
          isAuthenticated: false,
        }),
    }),
    {
      name: "auth-storage",
      partialize: (state) => ({
        accessToken: state.accessToken,
        user: state.user,
        isAuthenticated: state.isAuthenticated,
      }),
    }
  )
);
```

### 会话状态

```typescript
// src/stores/session.ts
import { create } from "zustand";
import type { SessionInfo } from "@/gen/gateway/v1/api_pb";

interface SessionState {
  sessions: SessionInfo[];
  activeSessionId: string | null;
  
  // Actions
  setSessions: (sessions: SessionInfo[]) => void;
  setActiveSession: (sessionId: string | null) => void;
  updateSession: (sessionId: string, updates: Partial<SessionInfo>) => void;
}

export const useSessionStore = create<SessionState>((set) => ({
  sessions: [],
  activeSessionId: null,

  setSessions: (sessions) => set({ sessions }),

  setActiveSession: (sessionId) => set({ activeSessionId: sessionId }),

  updateSession: (sessionId, updates) =>
    set((state) => ({
      sessions: state.sessions.map((session) =>
        session.sessionId === sessionId
          ? { ...session, ...updates }
          : session
      ),
    })),
}));
```

### 消息状态

```typescript
// src/stores/message.ts
import { create } from "zustand";
import type { PushMessage } from "@/gen/gateway/v1/packet_pb";

interface MessageState {
  // 按会话分组的消息
  messagesBySession: Record<string, PushMessage[]>;
  // WebSocket 连接状态
  connected: boolean;
  
  // Actions
  addMessage: (message: PushMessage) => void;
  setMessages: (sessionId: string, messages: PushMessage[]) => void;
  prependMessages: (sessionId: string, messages: PushMessage[]) => void;
  setConnected: (connected: boolean) => void;
}

export const useMessageStore = create<MessageState>((set) => ({
  messagesBySession: {},
  connected: false,

  addMessage: (message) =>
    set((state) => {
      const sessionId = message.sessionId;
      const existing = state.messagesBySession[sessionId] || [];
      return {
        messagesBySession: {
          ...state.messagesBySession,
          [sessionId]: [...existing, message],
        },
      };
    }),

  setMessages: (sessionId, messages) =>
    set((state) => ({
      messagesBySession: {
        ...state.messagesBySession,
        [sessionId]: messages,
      },
    })),

  prependMessages: (sessionId, messages) =>
    set((state) => {
      const existing = state.messagesBySession[sessionId] || [];
      return {
        messagesBySession: {
          ...state.messagesBySession,
          [sessionId]: [...messages, ...existing],
        },
      };
    }),

  setConnected: (connected) => set({ connected }),
}));
```

---

## 🎨 UI 组件规范

### 组件命名

- **文件名**: PascalCase，如 `SessionList.tsx`
- **组件名**: 与文件名一致
- **样式**: 使用 Tailwind CSS 类名

### 组件结构模板

```tsx
// src/components/chat/SessionItem.tsx
import { cn } from "@/lib/cn";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import type { SessionInfo } from "@/gen/gateway/v1/api_pb";

interface SessionItemProps {
  session: SessionInfo;
  isActive: boolean;
  onClick: () => void;
}

export function SessionItem({ session, isActive, onClick }: SessionItemProps) {
  return (
    <div
      className={cn(
        "flex items-center gap-3 p-3 cursor-pointer rounded-lg transition-colors",
        isActive ? "bg-accent" : "hover:bg-muted"
      )}
      onClick={onClick}
    >
      <Avatar>
        <AvatarImage src={session.avatarUrl} alt={session.name} />
        <AvatarFallback>{session.name[0]?.toUpperCase()}</AvatarFallback>
      </Avatar>
      
      <div className="flex-1 min-w-0">
        <div className="flex items-center justify-between">
          <span className="font-medium truncate">{session.name}</span>
          {session.unreadCount > 0n && (
            <span className="bg-primary text-primary-foreground text-xs px-2 py-0.5 rounded-full">
              {session.unreadCount.toString()}
            </span>
          )}
        </div>
        <p className="text-sm text-muted-foreground truncate">
          {session.lastMessage?.content}
        </p>
      </div>
    </div>
  );
}
```

---

## 🧪 MVP 功能清单

### P0 - 核心功能

- [ ] **认证模块**
  - [ ] 登录页面
  - [ ] 注册页面
  - [ ] Token 持久化存储
  - [ ] 登出功能
  - [ ] 认证状态守卫

- [ ] **会话模块**
  - [ ] 会话列表展示
  - [ ] 会话切换
  - [ ] 未读消息计数显示
  - [ ] 创建私聊会话

- [ ] **消息模块**
  - [ ] WebSocket 连接管理
  - [ ] 实时接收消息
  - [ ] 发送文本消息
  - [ ] 消息列表展示
  - [ ] 加载历史消息

### P1 - 增强功能

- [ ] **用户体验**
  - [ ] 加载状态展示
  - [ ] 错误处理与提示
  - [ ] 消息发送状态（发送中、已发送、失败）
  - [ ] 新消息提示音

- [ ] **联系人**
  - [ ] 联系人列表
  - [ ] 搜索用户

### P2 - 优化功能

- [ ] **性能优化**
  - [ ] 消息虚拟滚动
  - [ ] 图片懒加载
  
- [ ] **离线支持**
  - [ ] 离线消息队列
  - [ ] 断线重连优化

---

## 🚀 快速开始

### 1. 安装依赖

```bash
cd web
npm install
```

### 2. 配置环境变量

```bash
cp .env.example .env.local
# 编辑 .env.local 配置后端地址
```

### 3. 链接生成的代码

```bash
# 创建软链接指向生成的 TypeScript 代码
ln -s ../im-api/gen/ts src/gen
```

### 4. 启动开发服务器

```bash
npm run dev
```

### 5. 访问应用

打开 http://localhost:5173

---

## 📚 相关文档

- [AGENTS.md](./AGENTS.md) - AI 开发助手指引
- [im-api/ARCHITECTURE.md](../im-api/ARCHITECTURE.md) - API 架构说明
- [ConnectRPC 文档](https://connectrpc.com/docs/web/getting-started)
- [Zustand 文档](https://docs.pmnd.rs/zustand)
- [Shadcn/ui 文档](https://ui.shadcn.com)
