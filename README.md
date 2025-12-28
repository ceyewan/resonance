# Resonance

一个高性能即时通讯 (IM) 系统，**专注于 IM 业务逻辑**，合理利用 [Genesis](https://github.com/ceyewan/genesis) 组件库解决基础架构问题。

## ✨ 特性

- **IM 业务优先**: 专注于核心 IM 功能实现，包括实时消息、在线状态、离线处理
- **高性能**: 基于 Go 1.25+，支持高并发连接和消息处理
- **松耦合架构**: Gateway、Logic、Task 服务按职责分离，支持独立部署和扩展
- **可靠的投递**: 消息不丢失、不重复、保证时序，支持离线消息和重试机制
- **现代化技术栈**: WebSocket/TCP 长连接，gRPC 服务间通信，Protobuf 协议定义

## 🏗️ 架构设计

Resonance 采用经典的三层微服务架构：

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Client Apps   │    │   Client Apps   │    │   Client Apps   │
└─────────┬───────┘    └─────────┬───────┘    └─────────┬───────┘
          │                      │                      │
          └──────────────────────┼──────────────────────┘
                                 │
                    ┌─────────────▼─────────────┐
                    │      Gateway Layer        │
                    │  • WebSocket/TCP 连接     │
                    │  • 协议解析和路由          │
                    │  • 消息推送和鉴权          │
                    └─────────────┬─────────────┘
                gRPC │           ↑ │            ↑ gRPC
                     │           │ │            │
         ┌───────────▼─────┐   MQ │     ┌──────▼───────┐
         │   Logic Layer   │─────┼─────│  Task Layer   │
         │ • 业务逻辑处理   │     │     │ • 异步任务处理 │
         │ • 单聊/群聊管理  │     │     │ • 离线消息存储 │
         │ • 用户认证和会话 │     │     │ • 推送通知     │
         └─────────────────┘     │     └──────────────┘
                                 │
                           ┌─────▼─────┐
                           │    MQ     │
                           │ (NATS)   │
                           └──────────┘
```

### 服务通信流程

- **Gateway**: 网关服务，负责长连接维护、协议解析、消息推送和鉴权
  - 接收客户端消息，通过 gRPC 转发给 Logic 服务
  - 接收 Task 服务的推送请求，向客户端发送消息

- **Logic**: 逻辑服务，处理核心业务逻辑（单聊、群聊、用户认证等）
  - 通过 gRPC 接收 Gateway 的消息请求
  - 将异步任务（离线消息、通知等）通过 MQ 发送给 Task 服务

- **Task**: 任务服务，处理异步任务（离线消息、推送通知等）
  - 通过 MQ 接收 Logic 服务的异步任务
  - 处理完成后，通过 gRPC 调用 Gateway 进行消息推送

### 数据存储

- **Redis**: 缓存用户在线状态、会话路由信息和热点数据
- **MySQL**: 持久化存储用户信息、消息历史、群组数据
- **NATS**: 消息队列，处理异步任务和服务间通信

## 🚀 快速开始

### 前置要求

- Go 1.25+
- Redis 6.0+
- MySQL 8.0+
- Buf (Protobuf 代码生成工具)

### 安装运行

1. **克隆项目**

   ```bash
   git clone https://github.com/ceyewan/resonance.git
   cd resonance
   ```

2. **生成协议代码**

   ```bash
   make gen
   ```

3. **整理依赖**

   ```bash
   make tidy
   ```

4. **启动服务**

   ```bash
   # 启动网关服务
   make dev-gateway

   # 启动逻辑服务
   make dev-logic

   # 启动任务服务
   make dev-task
   ```

### 构建部署

```bash
# 编译所有服务
make build-gateway
make build-logic
make build-task

# 构建产物位于 bin/ 目录
```

## 📁 项目结构

```
resonance/
├── main.go                 # 统一程序入口
├── Makefile               # 构建和运行脚本
├── go.mod                 # Go 模块定义
├── api/                # API 协议定义
│   ├── proto/             # Protobuf 原文件
│   │   ├── gateway/       # 网关服务协议
│   │   ├── logic/         # 逻辑服务协议
│   │   ├── common/        # 通用类型定义
│   │   └── mq/            # 消息队列协议
│   ├── gen/               # 生成的代码
│   └── buf.gen.*.yaml     # 代码生成配置
├── internal/                # 公共 SDK
│   ├── model/             # 数据模型
│   └── repo/              # 仓储层封装
├── gateway/               # 网关服务实现
├── logic/                 # 逻辑服务实现
├── task/                  # 任务服务实现
├── deploy/                # 部署配置 (Docker/Compose)
├── web/                   # 前端演示项目
└── CLAUDE.md              # AI 助手开发指南
```

## 🛠️ 开发指南

### 核心命令

```bash
# 代码生成
make gen                   # 基于 api 生成代码
make tidy                  # 整理 Go 依赖

# 开发运行
make dev-gateway           # 运行网关服务
make dev-logic             # 运行逻辑服务
make dev-task              # 运行任务服务
make up                    # 启动基础设施

# 生产构建
make build-gateway         # 编译网关服务
make build-logic           # 编译逻辑服务
make build-task            # 编译任务服务

# 一键操作
make all                   # 执行代码生成和依赖整理
```

### 开发理念

**业务优先，工具为辅**：

- **IM 业务为核心**: 专注于消息路由、连接管理、状态同步等核心 IM 功能
- **合理使用 Genesis**: 按需使用基础组件，避免框架锁定和过度工程化
- **业务接口封装**: 通过业务接口隐藏底层实现，保持代码简洁和可测试性

详细的开发指南请参考 [CLAUDE.md](./CLAUDE.md)。

## 📊 协议接口

项目使用 Protobuf 定义服务接口，支持：

- **Gateway API**: 连接管理、消息收发、推送服务
- **Logic API**: 用户认证、聊天、会话管理
- **Common Types**: 通用数据类型和错误码定义
- **MQ Events**: 异步事件消息定义

## 🔧 系统要求

- **操作系统**: Linux/macOS/Windows
- **Go 版本**: 1.25 或更高版本
- **内存**: 最小 2GB，推荐 4GB+
- **存储**: 最小 10GB 可用空间

## 📝 许可证

本项目采用 [MIT License](LICENSE) 开源协议。

## 🎯 设计原则

- **简单优于复杂**: 优先实现核心功能，避免过度设计
- **可靠优于完美**: 确保消息投递的可靠性，再考虑性能优化
- **业务优先级高**: IM 业务逻辑的价值高于基础架构的完美性
- **渐进式演进**: 支持从简单到复杂的渐进式功能扩展

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！请重点关注：

- **IM 业务逻辑的完整性**
- **消息投递的可靠性**
- **连接管理的稳定性**
- **代码的可读性和可维护性**

在贡献代码前，请先阅读 [CLAUDE.md](./CLAUDE.md) 了解项目的开发理念和规范。
