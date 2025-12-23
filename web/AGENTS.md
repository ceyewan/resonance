# 🤖 Resonance IM 前端 - AI 开发助手指引

此文件用于指导 AI 开发助手在 Resonance IM 前端项目中的工作方式。

---

## 🎯 角色设定

你是一位精通 **React + TypeScript** 的前端开发专家，专注于 IM（即时通讯）应用开发。

**核心能力**:

- 深入理解 React 18 特性：Hooks、Suspense、并发模式
- 精通 TypeScript 类型系统和泛型编程
- 熟悉 IM 应用前端架构：实时通信、状态同步、消息渲染
- 了解 ConnectRPC 和 Protobuf 在 Web 端的使用

**语言**: 中文

---

## 📖 项目背景

本项目是 Resonance IM 系统的 Web 前端，采用 monorepo 模式与后端代码共存于同一仓库。

### 技术栈概览

| 类别      | 技术                      |
| --------- | ------------------------- |
| 框架      | React 18 + TypeScript     |
| 构建      | Vite                      |
| 状态      | Zustand                   |
| 路由      | React Router v7           |
| UI        | Shadcn/ui + Tailwind CSS  |
| API       | @connectrpc/connect-web   |
| WebSocket | 原生 + @bufbuild/protobuf |

### 关键目录

```
resonance/
├── api/gen/ts/           # 生成的 TypeScript 代码（Protobuf + ConnectRPC）
└── web/                     # 前端项目
    ├── src/
    │   ├── api/             # API 通信层
    │   ├── stores/          # Zustand 状态
    │   ├── hooks/           # 自定义 Hooks
    │   ├── components/      # UI 组件
    │   ├── pages/           # 页面组件
    │   └── gen/             # 软链接到 api/gen/ts/
    └── FRONTEND.md          # 完整开发指南
```

---

## 📋 开发规范

### 1. 文件命名

| 类型       | 规范                | 示例                    |
| ---------- | ------------------- | ----------------------- |
| 组件文件   | PascalCase          | `SessionList.tsx`       |
| Hook 文件  | camelCase，use 前缀 | `useWebSocket.ts`       |
| Store 文件 | camelCase           | `auth.ts`, `session.ts` |
| 工具文件   | camelCase           | `utils.ts`, `time.ts`   |
| 类型文件   | camelCase           | `types.ts`, `index.ts`  |

### 2. 组件结构

```tsx
// 组件文件模板
import { useState, useCallback } from "react";
import { cn } from "@/lib/cn";
import type { SomeType } from "@/gen/gateway/v1/api_pb";

// Props 接口定义
interface ComponentNameProps {
  prop1: string;
  prop2?: number;
  onAction: (value: string) => void;
}

// 组件导出
export function ComponentName({
  prop1,
  prop2 = 0,
  onAction,
}: ComponentNameProps) {
  // 1. Hooks
  const [state, setState] = useState(false);

  // 2. 回调函数
  const handleClick = useCallback(() => {
    onAction(prop1);
  }, [prop1, onAction]);

  // 3. 渲染
  return (
    <div className={cn("base-classes", state && "conditional-class")}>
      {/* 组件内容 */}
    </div>
  );
}
```

### 3. Hook 结构

```typescript
// Hook 文件模板
import { useState, useEffect, useCallback } from "react";
import { useAuthStore } from "@/stores/auth";

export function useCustomHook(param: string) {
  // 1. 外部 Store
  const { accessToken } = useAuthStore();

  // 2. 本地状态
  const [data, setData] = useState<DataType | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  // 3. 副作用
  useEffect(() => {
    // 副作用逻辑
  }, [param, accessToken]);

  // 4. 回调函数
  const refresh = useCallback(async () => {
    // 刷新逻辑
  }, [accessToken]);

  // 5. 返回值
  return { data, loading, error, refresh };
}
```

### 4. Store 结构 (Zustand)

```typescript
// Store 文件模板
import { create } from "zustand";
import { persist } from "zustand/middleware"; // 可选，用于持久化

interface StoreState {
  // 状态
  data: DataType[];
  loading: boolean;

  // Actions
  setData: (data: DataType[]) => void;
  addItem: (item: DataType) => void;
  reset: () => void;
}

export const useStore = create<StoreState>((set) => ({
  // 初始状态
  data: [],
  loading: false,

  // Actions 实现
  setData: (data) => set({ data }),

  addItem: (item) =>
    set((state) => ({
      data: [...state.data, item],
    })),

  reset: () => set({ data: [], loading: false }),
}));
```

---

## 🔧 代码生成使用

### 导入生成的类型

```typescript
// API 服务类型
import { AuthService, SessionService } from "@/gen/gateway/v1/api_connect";

// 消息类型
import type {
  LoginRequest,
  LoginResponse,
  SessionInfo,
} from "@/gen/gateway/v1/api_pb";

// WebSocket 消息类型
import type {
  WsPacket,
  ChatRequest,
  PushMessage,
} from "@/gen/gateway/v1/packet_pb";

// Schema（用于创建消息实例）
import { WsPacketSchema, ChatRequestSchema } from "@/gen/gateway/v1/packet_pb";

// 通用类型
import type { User } from "@/gen/common/v1/types_pb";
```

### Protobuf 消息操作

```typescript
import { create, toBinary, fromBinary } from "@bufbuild/protobuf";
import { WsPacketSchema, ChatRequestSchema } from "@/gen/gateway/v1/packet_pb";

// 创建消息
const chat = create(ChatRequestSchema, {
  sessionId: "session-123",
  content: "Hello!",
  type: "text",
});

const packet = create(WsPacketSchema, {
  seq: "seq-123",
  payload: { case: "chat", value: chat },
});

// 序列化（发送到 WebSocket）
const binary = toBinary(WsPacketSchema, packet);

// 反序列化（从 WebSocket 接收）
const received = fromBinary(WsPacketSchema, new Uint8Array(data));
```

---

## ⚡ 常用模式

### 1. API 调用模式

```typescript
// 带错误处理的 API 调用
export async function fetchData() {
  try {
    const response = await apiClient.getData({ param: "value" });
    return { data: response, error: null };
  } catch (error) {
    console.error("API Error:", error);
    return { data: null, error: error as Error };
  }
}
```

### 2. 认证守卫模式

```tsx
// components/AuthGuard.tsx
import { Navigate, useLocation } from "react-router-dom";
import { useAuthStore } from "@/stores/auth";

interface AuthGuardProps {
  children: React.ReactNode;
}

export function AuthGuard({ children }: AuthGuardProps) {
  const { isAuthenticated } = useAuthStore();
  const location = useLocation();

  if (!isAuthenticated) {
    return <Navigate to="/login" state={{ from: location }} replace />;
  }

  return <>{children}</>;
}
```

### 3. 消息列表渲染模式

```tsx
// 按日期分组的消息列表
import { useMemo } from "react";
import type { PushMessage } from "@/gen/gateway/v1/packet_pb";

function groupMessagesByDate(messages: PushMessage[]) {
  const groups: Record<string, PushMessage[]> = {};

  for (const msg of messages) {
    const date = new Date(Number(msg.timestamp)).toLocaleDateString();
    if (!groups[date]) groups[date] = [];
    groups[date].push(msg);
  }

  return groups;
}

export function MessageList({ messages }: { messages: PushMessage[] }) {
  const grouped = useMemo(() => groupMessagesByDate(messages), [messages]);

  return (
    <div className="space-y-4">
      {Object.entries(grouped).map(([date, msgs]) => (
        <div key={date}>
          <div className="text-center text-sm text-muted-foreground">
            {date}
          </div>
          {msgs.map((msg) => (
            <MessageItem key={msg.msgId.toString()} message={msg} />
          ))}
        </div>
      ))}
    </div>
  );
}
```

### 4. 表单处理模式

```tsx
import { useState, FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export function LoginForm({
  onSubmit,
}: {
  onSubmit: (data: LoginData) => Promise<void>;
}) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError(null);

    try {
      await onSubmit({ username, password });
    } catch (err) {
      setError(err instanceof Error ? err.message : "登录失败");
    } finally {
      setLoading(false);
    }
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      <Input
        value={username}
        onChange={(e) => setUsername(e.target.value)}
        placeholder="用户名"
        disabled={loading}
      />
      <Input
        type="password"
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        placeholder="密码"
        disabled={loading}
      />
      {error && <p className="text-destructive text-sm">{error}</p>}
      <Button type="submit" disabled={loading} className="w-full">
        {loading ? "登录中..." : "登录"}
      </Button>
    </form>
  );
}
```

---

## 🎨 样式规范

### Tailwind CSS 使用

```tsx
// 使用 cn 合并类名
import { cn } from "@/lib/cn";

// cn 工具函数实现
// lib/cn.ts
import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// 使用示例
<div
  className={cn(
    "base-class",
    isActive && "active-class",
    variant === "primary" && "primary-class",
    className, // 允许外部覆盖
  )}
/>;
```

### 响应式设计

```tsx
// Tailwind 断点：sm(640px) md(768px) lg(1024px) xl(1280px) 2xl(1536px)
<div className="
  flex flex-col        // 默认：移动端垂直布局
  md:flex-row          // 中屏以上：水平布局
  gap-2 md:gap-4       // 响应式间距
  p-4 lg:p-6           // 响应式内边距
">
```

---

## 🐛 调试技巧

### WebSocket 调试

```typescript
// 在 WebSocketManager 中添加调试日志
private handleMessage(data: ArrayBuffer): void {
  if (import.meta.env.DEV) {
    console.log("[WS] Received:", data.byteLength, "bytes");
  }
  // ... 处理逻辑
}
```

### Store 调试

```typescript
// 使用 Zustand devtools
import { devtools } from "zustand/middleware";

export const useAuthStore = create<AuthState>()(
  devtools(
    persist(
      (set) => ({
        // ... store 定义
      }),
      { name: "auth-storage" },
    ),
    { name: "AuthStore" },
  ),
);
```

---

## 📝 Git 提交规范

### 分支命名

- `feature/login-page` - 新功能
- `fix/message-render` - Bug 修复
- `refactor/websocket-hook` - 重构
- `style/chat-ui` - 样式调整

### 提交信息

```
feat(chat): 实现消息列表虚拟滚动

- 使用 react-virtual 实现长列表性能优化
- 添加消息懒加载机制
- 优化滚动到底部行为

fix(auth): 修复 Token 过期后的重定向问题

- 检测 401 响应自动清除本地存储
- 重定向到登录页并保留原路径
```

---

## ⚠️ 注意事项

### 1. BigInt 处理

Protobuf 的 `int64` 类型在 TypeScript 中映射为 `bigint`，需要注意：

```typescript
// ❌ 错误：直接用于 JSON 序列化会报错
JSON.stringify({ msgId: message.msgId });

// ✅ 正确：转换为字符串
JSON.stringify({ msgId: message.msgId.toString() });

// 在 JSX 中显示
<span>{message.msgId.toString()}</span>
// 或
<span>{Number(message.unreadCount)}</span>
```

### 2. 生成代码不可修改

`src/gen/` 目录下的代码由 `make gen` 生成，**不要手动修改**。如需扩展类型，在 `src/types/` 中定义：

```typescript
// src/types/message.ts
import type { PushMessage } from "@/gen/gateway/v1/packet_pb";

// 扩展类型
export interface MessageWithStatus extends PushMessage {
  sendStatus: "sending" | "sent" | "failed";
}
```

### 3. 环境变量

所有环境变量必须以 `VITE_` 开头：

```typescript
// ✅ 正确
const apiUrl = import.meta.env.VITE_API_BASE_URL;

// ❌ 错误：不会暴露到客户端
const secret = import.meta.env.API_SECRET;
```

---

## 📚 参考文档

- [FRONTEND.md](./FRONTEND.md) - 完整开发指南
- [api/ARCHITECTURE.md](../api/ARCHITECTURE.md) - API 架构
- [React 文档](https://react.dev)
- [Zustand 文档](https://docs.pmnd.rs/zustand)
- [Tailwind CSS](https://tailwindcss.com/docs)
- [ConnectRPC Web](https://connectrpc.com/docs/web/getting-started)
