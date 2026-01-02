# Resonance

一个高性能即时通讯 (IM) 系统，**专注于 IM 业务逻辑**，合理利用 [Genesis](https://github.com/ceyewan/genesis) 组件库解决基础架构问题。

## 特性

- **IM 业务优先**: 专注于核心 IM 功能实现，包括实时消息、在线状态、离线处理
- **高性能**: 基于 Go 1.25+，支持高并发连接和消息处理
- **松耦合架构**: Gateway、Logic、Task 服务按职责分离，支持独立部署和扩展
- **可靠的投递**: 消息不丢失、不重复、保证时序，支持离线消息和重试机制
- **现代化技术栈**: WebSocket 长连接、gRPC 服务间通信、Protobuf 协议定义
- **Web 前端**: React + TypeScript + ConnectRPC，参考 Telegram UI 设计

## 架构设计

```
                    消息流向与服务通信

┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│     Web 前端     │    │   Gateway 网关   │    │    Logic 逻辑    │
│                 │    │                 │    │                 │
│  • React + TS   │◄───┤  • ConnectRPC   │◄───┤  • gRPC Server  │
│  • WebSocket    │    │  • WebSocket    │    │  • 消息路由      │
│                 │    │  • 长连接管理    │    │  • 会话管理      │
└─────────────────┘    └────────┬────────┘    └────────┬────────┘
                                │                       │
                                │                       │ MQ Publish
                                │                       │
                            ┌───▼──────┐          ┌─────▼─────┐
                            │  Task    │◄─────────┤   NATS    │
                            │          │ Subscribe│    MQ     │
                            │ • 离线消息│          └───────────┘
                            │ • 持久化  │
                            │ • gRPC推送│
                            └──────────┘

                    ┌───────┴─────────────────────┐
                    │      Genesis 组件层          │
                    │  connector / db / mq / ...  │
                    └─────────────────────────────┘
```

### 服务职责

| 服务 | 职责 | 端口 |
|------|------|------|
| **Gateway** | WebSocket 长连接、心跳检测、消息推送、协议解析 | 8080 |
| **Logic** | 消息路由、权限验证、会话管理、用户认证 | - |
| **Task** | 离线消息、持久化、统计分析 | - |

### 通信方式

- **Web → Gateway**: ConnectRPC (HTTP)、WebSocket
- **Gateway → Logic**: gRPC
- **Logic → NATS → Task**: MQ (异步消息)
- **Task → Gateway → Web**: gRPC 推送 → WebSocket

## 项目结构

```
resonance/
├── main.go                 # 统一入口 (go run main.go -module logic)
├── Makefile               # 构建脚本
├── CLAUDE.md              # AI 助手开发指南
├── README.md              # 本文档
│
├── api/                   # Protobuf 协议定义
│   ├── proto/             # .proto 原文件
│   │   ├── gateway/       # Gateway 服务协议
│   │   ├── logic/         # Logic 服务协议
│   │   └── common/        # 通用类型
│   └── gen/               # 生成代码 (Go/TS)
│
├── internal/              # 公共 SDK
│   ├── model/             # 数据模型
│   └── repo/              # 业务接口定义
│
├── logic/                 # Logic 服务
│   ├── config/            # 配置加载
│   ├── server/            # gRPC 服务器
│   ├── service/           # 业务逻辑
│   └── logic.go           # 生命周期管理
│
├── gateway/               # Gateway 服务
│   ├── config/            # 配置加载
│   ├── server/            # HTTP/WebSocket 服务器
│   ├── handler/           # ConnectRPC 处理器
│   └── gateway.go         # 生命周期管理
│
├── task/                  # Task 服务
│   ├── config/
│   ├── consumer/          # MQ 消费者
│   └── task.go
│
└── web/                   # React 前端
    ├── src/
    │   ├── api/           # ConnectRPC 客户端
    │   ├── hooks/         # WebSocket Hook
    │   ├── stores/        # Zustand 状态
    │   └── pages/         # 页面组件
    ├── FRONTEND.md        # 前端开发指南
    └── package.json
```

## 快速开始

### 前置要求

- Go 1.25+
- Node.js 18+
- Redis 6.0+
- MySQL 8.0+
- Buf (Protobuf 代码生成)

### 安装运行

**1. 克隆项目**

```bash
git clone https://github.com/ceyewan/resonance.git
cd resonance
```

**2. 生成协议代码**

```bash
make gen
```

**3. 整理依赖**

```bash
make tidy
```

**4. 启动基础设施 (可选)**

```bash
# 使用 Docker 启动 Redis/MySQL
make up
```

**5. 启动后端服务**

```bash
# 终端 1: 启动 Gateway
make run-gateway

# 终端 2: 启动 Logic
make run-logic

# 终端 3: 启动 Task
make run-task
```

**6. 启动前端**

```bash
cd web
npm install
npm run dev
```

访问 http://localhost:5173

## 常用命令

### 后端

```bash
# 代码生成
make gen                  # 生成 Protobuf 代码
make tidy                 # 整理 Go 依赖

# 开发运行
make run-gateway          # 运行网关服务
make run-logic            # 运行逻辑服务
make run-task             # 运行任务服务

# 生产构建
make build-gateway        # 编译网关服务
make build-logic          # 编译逻辑服务
make build-task           # 编译任务服务
```

### 前端

```bash
cd web
npm run dev              # 开发服务器
npm run build            # 生产构建
npm run type-check       # 类型检查
```

## 技术栈

### 后端

| 类别 | 技术 |
|------|------|
| 语言 | Go 1.25+ |
| 组件库 | [Genesis](https://github.com/ceyewan/genesis) |
| 服务间通信 | gRPC |
| 消息队列 | NATS |
| 协议 | Protobuf |

### 前端

| 类别 | 技术 |
|------|------|
| 框架 | React 18 |
| 语言 | TypeScript 5.6+ |
| 构建 | Vite |
| 状态 | Zustand |
| 样式 | Tailwind CSS |
| 通信 | ConnectRPC + WebSocket |

## 开发指南

- **后端开发**: 参考 [CLAUDE.md](./CLAUDE.md)
- **前端开发**: 参考 [web/FRONTEND.md](./web/FRONTEND.md)

## 设计原则

1. **简单优于复杂**: 优先实现核心功能，避免过度设计
2. **可靠优于完美**: 确保消息投递的可靠性，再考虑性能优化
3. **业务优先**: IM 业务逻辑的价值高于基础架构的完美性
4. **渐进式演进**: 支持从简单到复杂的渐进式功能扩展

## License

[MIT](LICENSE)
