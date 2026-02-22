# Resonance Web

Resonance IM 系统的 Web 前端，采用 **React + TypeScript** 技术栈，融合 **Telegram 布局** 与 **Liquid Glass 设计语言**。

## 概述

Resonance Web 是 Resonance IM 系统的前端应用，通过 **ConnectRPC (HTTP)** 和 **WebSocket (Protobuf)** 与 Gateway 服务通信。

**设计目标**：打造现代、流畅的即时通讯体验，采用 Liquid Glass 设计语言，创造具有光学玻璃质感和流动交互的界面。

---

## 技术栈

| 类别     | 技术         | 版本  | 用途                |
| -------- | ------------ | ----- | ------------------- |
| 框架     | React        | 18.3+ | UI 框架             |
| 语言     | TypeScript   | 5.6+  | 类型安全            |
| 构建     | Vite         | 5.4+  | 开发服务器与打包    |
| 状态     | Zustand      | 4.5+  | 轻量状态管理        |
| 样式     | Tailwind CSS | 3.4+  | 原子化 CSS          |
| 设计语言 | Liquid Glass | -     | 液态玻璃风格        |
| API      | ConnectRPC   | 1.4+  | 类型安全的 RPC 调用 |
| 实时通信 | WebSocket    | -     | Protobuf 消息推送   |

---

## 项目结构

```
web/
├── src/
│   ├── api/                     # API 通信层
│   │   └── client.ts            # ConnectRPC 客户端（带认证拦截器）
│   ├── components/              # React 组件
│   │   ├── Chat/                # 聊天相关组件（ChatHeader, ChatArea, SessionSidebar）
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
├── package.json
├── vite.config.ts               # Vite 配置（代理 → Gateway）
├── tailwind.config.ts           # Tailwind 配置
├── tsconfig.json                # TypeScript 配置
├── README.md                    # 本文档
└── CLAUDE.md                    # AI 开发助手指引
```

---

## 配置速查

| 文件 | 一句话职责 |
| --- | ---|
| `package.json` | 定义前端依赖与脚本入口（`dev/build/type-check`）。 |
| `vite.config.ts` | 控制开发服务器与构建行为（别名 `@`、`src/gen` 软链解析）。 |
| `tsconfig.json` | 定义业务代码的 TypeScript 编译规则（严格模式、路径别名）。 |
| `tailwind.config.ts` | 定义 Tailwind 扫描范围、暗色模式策略与主题扩展。 |
| `.gitignore` | 约束前端目录下不入库文件（`node_modules`、`dist` 等）。 |

---

## 快速开始

### 1. 安装依赖

```bash
cd web
npm install
```

### 2. 配置环境变量

开发模式创建 `.env.local` 文件：

```bash
# Gateway API 地址（ConnectRPC）
VITE_API_BASE_URL=http://localhost:8080

# Gateway WebSocket 地址
VITE_WS_BASE_URL=ws://localhost:8080/ws
```

生产（容器）模式支持运行时配置，无需重建前端包：
- `RESONANCE_WEB_API_BASE_URL`：覆盖 API 地址
- `RESONANCE_WEB_WS_BASE_URL`：覆盖 WebSocket 地址

### 3. 确保协议代码已生成

```bash
# 从项目根目录执行
cd .. && make gen
```

### 4. 启动开发服务器

```bash
npm run dev
```

访问 http://localhost:5173

---

## 常用命令

```bash
npm run dev          # 开发服务器（5173 端口）
npm run build        # 生产构建
npm run preview      # 预览构建产物
npm run type-check   # TypeScript 类型检查
```

---

## 通信架构

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

- **ConnectRPC**: 登录、注册、获取会话列表等 API
- **WebSocket**: 实时消息推送，Protobuf 二进制格式

---

## 组件架构

### 页面级组件

| 组件 | 路径 | 职责 |
| ---- | ---- | ---- |
| LoginPage | `pages/LoginPage.tsx` | 登录/注册 |
| ChatPage | `pages/ChatPage.tsx` | 聊天主界面 |

### 聊天组件（`components/Chat/`）

| 组件 | 职责 |
| ---- | ---- |
| ChatHeader | 顶部导航栏（Logo、用户信息、登出） |
| ChatArea | 聊天区域（消息列表 + 输入框） |
| SessionSidebar | 会话侧边栏（会话列表 + 新建按钮） |

### 通用组件

| 组件 | 职责 |
| ---- | ---- |
| ChatInput | 消息输入框（支持多行、发送） |
| ConnectionStatus | 连接状态指示器（已连接/连接中/断开） |
| ErrorBoundary | 错误边界（捕获子组件错误） |
| MessageBubble | 消息气泡（支持不同消息类型） |
| NewChatModal | 新建聊天弹窗 |
| SessionItem | 会话列表项（显示头像、名称、最后消息） |

---

## 状态管理

### AuthStore (`stores/auth.ts`)

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
}
```

### SessionStore (`stores/session.ts`)

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

### MessageStore (`stores/message.ts`)

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

## 样式系统

### Liquid Glass CSS 类

| 类名 | 用途 |
| ---- | ---- |
| `lg-glass` | 基础玻璃效果（一级模糊） |
| `lg-glass-strong` | 强玻璃效果（二级模糊） |
| `lg-btn-primary` | 主按钮样式 |
| `lg-btn-secondary` | 次按钮样式 |
| `lg-input` | 输入框样式 |
| `lg-bubble-own` | 己方消息气泡 |
| `lg-bubble-other` | 对方消息气泡 |
| `lg-bubble-system` | 系统消息气泡 |
| `lg-session-item` | 会话列表项 |
| `lg-session-item-active` | 激活的会话列表项 |
| `lg-modal-overlay` | 模态框遮罩 |
| `lg-modal-content` | 模态框内容 |
| `lg-status-badge` | 状态徽章 |
| `lg-animate-in` | 入场动画 |

---

## 待实现功能

- [ ] 消息长按菜单（回复/转发/删除）
- [ ] 消息回复链
- [ ] 消息编辑/撤回
- [ ] 图片发送与预览
- [ ] 文件传输
- [ ] 群组管理
- [ ] 搜索功能
- [ ] 消息已读状态（双勾）
- [ ] 暗色主题切换按钮
- [ ] 消息表情反应

---

## 相关文档

- [CLAUDE.md](./CLAUDE.md) - AI 开发助手指引
- [../README.md](../README.md) - 项目整体文档
