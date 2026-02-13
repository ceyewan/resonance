# Resonance Web

Resonance IM 系统的 Web 前端，采用 **React + TypeScript** 技术栈，参考 Telegram UI 设计。

## 概述

Resonance Web 是 Resonance IM 系统的前端应用，通过 **ConnectRPC (HTTP)** 和 **WebSocket (Protobuf)** 与 Gateway 服务通信。

**设计目标**：参考 Telegram 的 UI/UX 设计，打造简洁、高效的即时通讯体验。

---

## 技术栈

| 类别     | 技术         | 版本  | 用途                |
| -------- | ------------ | ----- | ------------------- |
| 框架     | React        | 18.3+ | UI 框架             |
| 语言     | TypeScript   | 5.6+  | 类型安全            |
| 构建     | Vite         | 5.4+  | 开发服务器与打包    |
| 状态     | Zustand      | 4.5+  | 轻量状态管理        |
| 样式     | Tailwind CSS | 3.4+  | 原子化 CSS          |
| API      | ConnectRPC   | 1.4+  | 类型安全的 RPC 调用 |
| 实时通信 | WebSocket    | -     | Protobuf 消息推送   |

---

## 项目结构

```
web/
├── src/
│   ├── api/                 # API 通信层
│   │   └── client.ts        # ConnectRPC 客户端（带认证拦截器）
│   ├── gen/                 # Protobuf 生成代码（软链接 → ../api/gen/ts）
│   ├── hooks/               # 自定义 Hooks
│   │   ├── useAuth.ts       # 认证 Hook
│   │   └── useWebSocket.ts  # WebSocket Hook（心跳/重连）
│   ├── lib/                 # 工具库
│   │   └── cn.ts            # className 合并工具
│   ├── pages/               # 页面组件
│   │   ├── LoginPage.tsx    # 登录/注册页
│   │   └── ChatPage.tsx     # 聊天主界面
│   ├── stores/              # Zustand 状态管理
│   │   ├── auth.ts          # 认证状态（持久化）
│   │   ├── session.ts       # 会话状态
│   │   └── message.ts       # 消息状态
│   ├── styles/              # 全局样式
│   │   └── globals.css      # Tailwind + 设计 tokens
│   ├── App.tsx              # 应用入口
│   └── main.tsx             # React 挂载
├── .env.local               # 环境变量（本地，不提交）
├── package.json
├── vite.config.ts           # Vite 配置（代理 → Gateway）
├── tailwind.config.ts       # Tailwind 配置
├── AGENTS.md                # AI 助手开发指引（类似 CLAUDE.md）
└── README.md                # 本文档
```

---

## 配置速查（每个文件一句话）

| 文件 | 一句话职责 |
| --- | --- |
| `package.json` | 定义前端依赖与脚本入口（`dev/build/lint/type-check`）。 |
| `vite.config.ts` | 控制开发服务器与构建行为（别名 `@`、`src/gen` 软链解析、打包告警策略）。 |
| `tsconfig.json` | 定义业务代码的 TypeScript 编译规则（严格模式、路径别名、React JSX）。 |
| `tsconfig.node.json` | 给 Node 侧配置文件（主要是 `vite.config.ts`）提供独立 TS 类型环境。 |
| `tailwind.config.ts` | 定义 Tailwind 扫描范围、暗色模式策略与主题扩展。 |
| `postcss.config.js` | 把 Tailwind 和 Autoprefixer 挂进 CSS 构建管道。 |
| `eslint.config.js` | 定义 TS/TSX 的静态检查规则与忽略目录。 |
| `index.html` | 声明浏览器入口 HTML（`#root` 挂载点与基础 meta）。 |
| `src/styles/globals.css` | 放全局样式与设计 token（当前含 Telegram + 液态玻璃基底）。 |
| `.gitignore` | 约束前端目录下不入库文件（`node_modules`、`dist`、缓存文件等）。 |

## 目录清理说明

- 可随时删除（构建会再生成）：`dist/`、`*.tsbuildinfo`、`vite.config.js`、`vite.config.d.ts`
- 一般不入库：`node_modules/`
- 业务源码与配置（`src/`、`package.json`、`vite.config.ts` 等）应保留

---

## 快速开始

### 1. 安装依赖

```bash
cd web
npm install
```

### 2. 配置环境变量

创建 `.env.local` 文件：

```bash
# Gateway API 地址（ConnectRPC）
VITE_API_BASE_URL=http://localhost:8080

# Gateway WebSocket 地址
VITE_WS_BASE_URL=ws://localhost:8080/ws
```

### 3. 确保协议代码已生成

```bash
# 确保软链接存在
cd src
ln -s ../../api/gen/ts gen

# 或从项目根目录执行
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
npm run lint         # ESLint 检查
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

- **ConnectRPC**: 用于登录、注册、获取会话列表等 RESTful API
- **WebSocket**: 用于实时消息推送，Protobuf 二进制格式

---

## 页面说明

### LoginPage (`pages/LoginPage.tsx`)

- 登录/注册模式切换
- 表单验证
- 错误提示
- 调用 `useAuth` hook 处理认证

### ChatPage (`pages/ChatPage.tsx`)

- 左侧会话列表（可切换）
- 右侧聊天区域（消息展示 + 输入框）
- 连接状态显示
- 乐观更新消息发送

---

## 待实现功能（Telegram 参考）

- [ ] 消息长按菜单（回复/转发/删除）
- [ ] 消息回复链
- [ ] 消息编辑/撤回
- [ ] 图片发送与预览
- [ ] 文件传输
- [ ] 群组管理
- [ ] 搜索功能
- [ ] 消息已读状态（双勾）
- [ ] 在线状态指示器
- [ ] 暗色主题切换
- [ ] 消息表情反应
- [ ] 消息转发
- [ ] 跳转到未读消息

---

## 相关文档

- [AGENTS.md](./AGENTS.md) - AI 开发助手指引（详细开发规范、技术实现）
- [../README.md](../README.md) - 项目整体文档
