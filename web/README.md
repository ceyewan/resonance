# Resonance IM - Web Frontend

Resonance IM 前端应用，基于 React 18 + TypeScript + Vite + Zustand。

## 🚀 快速开始

### 环境要求

- Node.js 18+
- npm 或 yarn

### 安装依赖

```bash
cd web
npm install
```

### 开发运行

```bash
npm run dev
```

应用将在 http://localhost:5173 自动打开。

### 构建生产版本

```bash
npm run build
```

### 类型检查

```bash
npm run type-check
```

## 📁 项目结构

```
web/
├── src/
│   ├── api/              # API 客户端和通信层
│   │   └── client.ts     # ConnectRPC 客户端初始化
│   ├── gen/              # 生成的代码（软链接到 im-api/gen/ts）
│   ├── hooks/            # 自定义 Hooks
│   │   ├── useAuth.ts    # 认证 Hook
│   │   └── useWebSocket.ts # WebSocket Hook
│   ├── lib/              # 工具函数
│   │   └── cn.ts         # Tailwind CSS 类名合并工具
│   ├── pages/            # 页面组件
│   │   ├── LoginPage.tsx # 登录页面
│   │   └── ChatPage.tsx  # 聊天页面
│   ├── stores/           # Zustand 状态管理
│   │   ├── auth.ts       # 认证状态
│   │   ├── session.ts    # 会话状态
│   │   └── message.ts    # 消息状态
│   ├── styles/           # 全局样式
│   │   └── globals.css   # Tailwind CSS 和自定义样式
│   ├── App.tsx           # 根组件
│   ├── main.tsx          # 应用入口
│   └── vite-env.d.ts     # Vite 环境变量类型定义
├── index.html            # HTML 模板
├── package.json          # 项目依赖
├── tsconfig.json         # TypeScript 配置
├── tailwind.config.ts    # Tailwind CSS 配置
├── postcss.config.js     # PostCSS 配置
└── vite.config.ts        # Vite 配置
```

## 🔧 技术栈

- **框架**: React 18
- **语言**: TypeScript
- **构建**: Vite
- **状态管理**: Zustand
- **样式**: Tailwind CSS + Shadcn/ui
- **API**: ConnectRPC (@connectrpc/connect-web)
- **实时通信**: WebSocket + Protobuf (@bufbuild/protobuf)

## 📚 开发指南

### API 通信

使用 ConnectRPC 与后端通信：

```typescript
import { authClient, sessionClient } from '@/api/client'

// 登录
const response = await authClient.login({
  username: 'user',
  password: 'pass',
})

// 获取会话列表
const sessions = await sessionClient.getSessionList({
  accessToken: token,
})
```

### 状态管理

使用 Zustand 管理应用状态：

```typescript
import { useAuthStore } from '@/stores/auth'
import { useSessionStore } from '@/stores/session'
import { useMessageStore } from '@/stores/message'

// 获取状态
const { user, isAuthenticated } = useAuthStore()
const { sessions, currentSession } = useSessionStore()
const { messages } = useMessageStore()

// 更新状态
const { setUser, logout } = useAuthStore()
const { setCurrentSession } = useSessionStore()
```

### WebSocket 连接

使用 useWebSocket Hook 管理 WebSocket 连接：

```typescript
import { useWebSocket } from '@/hooks/useWebSocket'

const { isConnected, send, connect, disconnect } = useWebSocket({
  onMessage: (packet) => {
    console.log('Received:', packet)
  },
})

// 连接
connect()

// 发送消息
send(packet)

// 断开连接
disconnect()
```

## 🎨 样式

使用 Tailwind CSS 进行样式开发。所有颜色和主题配置在 `tailwind.config.ts` 中定义。

使用 `cn` 工具函数合并 Tailwind CSS 类名：

```typescript
import { cn } from '@/lib/cn'

<div className={cn(
  'base-class',
  isActive && 'active-class',
  className // 允许外部覆盖
)} />
```

## 📝 环境变量

在 `.env.local` 中配置环境变量：

```env
VITE_API_BASE_URL=http://localhost:8080
VITE_WS_HOST=localhost
VITE_WS_PORT=8080
```

## 🔗 相关文档

- [FRONTEND.md](./FRONTEND.md) - 详细的前端开发指南
- [AGENTS.md](./AGENTS.md) - AI 开发助手指引
- [后端文档](../README.md)
