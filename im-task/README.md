# Task 服务框架

Task 是 Resonance IM 系统的异步任务处理服务，负责消息的写扩散和推送分发。

## 📐 架构设计

### 核心职责

**消息处理流程**:
1. **消费 MQ** - 订阅 NATS 的 PushEvent 消息
2. **写扩散** - 查询会话成员，为每个在线用户生成推送任务
3. **推送到 Gateway** - 通过 gRPC 调用 Gateway 的 PushService，将消息推送给在线用户

### 目录结构

```
im-task/
├── config.go              # 配置管理
├── task.go                # 主服务入口
├── README.md              # 服务文档
├── consumer/              # MQ 消费者
│   └── consumer.go        # 消费 PushEvent，带重试机制
├── dispatcher/            # 消息分发器
│   └── dispatcher.go      # 写扩散逻辑，推送给会话成员
└── pusher/                # Gateway 推送客户端
    └── gateway_pusher.go  # 管理 Gateway gRPC 连接和推送
```

## 🔄 消息流转

### 完整流程

```
Logic (MQ Publish)
  ↓
NATS (PushEvent)
  ↓
Task Consumer (订阅消费)
  ↓
Dispatcher (写扩散)
  ↓ 查询会话成员
SessionRepository
  ↓ 检查在线状态
Redis (user:online:{username})
  ↓ 推送给在线用户
Gateway Pusher (gRPC)
  ↓
Gateway (PushService)
  ↓
WebSocket Client
```

### 写扩散策略

**什么是写扩散？**

当用户 A 在群聊中发送一条消息时，Task 服务会：
1. 查询群聊的所有成员（假设有 100 人）
2. 检查每个成员的在线状态
3. 为每个在线成员推送一份消息副本

**优点**:
- 读取快：用户打开聊天直接看到消息，无需查询
- 实时性好：消息立即推送到客户端

**缺点**:
- 写入慢：群聊成员越多，推送次数越多
- 适合中小型群聊（< 500 人）

**优化方向**:
- 大群聊（> 500 人）可以改用读扩散（用户打开聊天时才查询）
- 离线用户不推送，等上线后拉取离线消息

## ⚙️ 配置说明

```go
type Config struct {
    Log   clog.Config           // 日志配置
    MySQL connector.MySQLConfig // MySQL 配置
    Redis connector.RedisConfig // Redis 配置
    NATS  connector.NATSConfig  // NATS 配置

    GatewayAddrs []string // Gateway 服务地址列表

    ConsumerConfig ConsumerConfig // 消费者配置
}

type ConsumerConfig struct {
    Topic         string // 订阅的主题 (默认: resonance.push.event.v1)
    QueueGroup    string // 队列组名称 (默认: task-service)
    WorkerCount   int    // 并发处理协程数 (默认: 10)
    MaxRetry      int    // 最大重试次数 (默认: 3)
    RetryInterval int    // 重试间隔（秒）(默认: 5)
}
```

## 🚀 使用示例

```go
package main

import (
    "os"
    "os/signal"
    "syscall"

    "github.com/ceyewan/resonance/im-task"
    "github.com/ceyewan/resonance/im-sdk/repo"
)

func main() {
    // 创建配置
    cfg := task.DefaultConfig()
    cfg.GatewayAddrs = []string{
        "gateway-1:8080",
        "gateway-2:8080",
    }

    // 创建 Task 实例
    t, err := task.New(cfg)
    if err != nil {
        panic(err)
    }

    // 注入 Repo 实现
    t.SetRepositories(sessionRepo) // repo.SessionRepository

    // 启动服务
    go func() {
        if err := t.Run(); err != nil {
            panic(err)
        }
    }()

    // 等待退出信号
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    <-sigChan

    // 优雅关闭
    t.Close()
}
```

## 🔑 关键组件

### 1. Consumer (MQ 消费者)

**职责**:
- 订阅 NATS 的 `resonance.push.event.v1` 主题
- 启动多个 worker 并发处理消息
- 解析 PushEvent 并调用 Dispatcher
- 处理成功后 Ack，失败后 Nak 重新入队

**特性**:
- 队列组订阅（多个 Task 实例负载均衡）
- 带重试机制（最多重试 3 次）
- 优雅关闭（等待正在处理的消息完成）

### 2. Dispatcher (消息分发器)

**职责**:
- 查询会话成员列表（SessionRepository）
- 检查每个成员的在线状态（Redis）
- 调用 Pusher 推送给在线用户

**写扩散逻辑**:
```go
// 1. 获取会话成员
members := sessionRepo.GetSessionMembers(sessionID)

// 2. 遍历成员
for _, username := range members {
    // 跳过发送者自己
    if username == fromUsername {
        continue
    }

    // 检查在线状态
    online, gatewayAddr := cache.Get("user:online:" + username)
    if !online {
        continue // 离线用户跳过
    }

    // 推送到对应的 Gateway
    pusher.PushToUser(gatewayAddr, username, message)
}
```

### 3. GatewayPusher (Gateway 推送客户端)

**职责**:
- 管理多个 Gateway 的 gRPC 连接
- 为每个 Gateway 维护一个双向流（PushService.PushMessage）
- 接收推送响应并记录日志

**特性**:
- 连接池管理（每个 Gateway 一个连接）
- 双向流复用（避免频繁建立连接）
- 自动重连（连接断开后自动重建）

**推送流程**:
```go
// Task 调用
pusher.PushToUser("gateway-1:8080", "user123", message)
  ↓
// 找到对应的 Gateway 客户端
client := clients["gateway-1:8080"]
  ↓
// 通过双向流发送
stream.Send(PushMessageRequest{
    ToUsername: "user123",
    Message: message,
})
  ↓
// Gateway 返回响应
stream.Receive() -> PushMessageResponse
```

## 📊 性能考虑

### 并发处理

- **Worker 数量**: 默认 10 个，可根据消息量调整
- **推送并发**: 每个会话的成员推送是串行的，可优化为并发

### 重试策略

- **最大重试**: 3 次
- **重试间隔**: 5 秒
- **失败处理**: 重试失败后 Nak，消息重新入队

### 在线状态缓存

- **Redis 查询**: 每个用户查询一次在线状态
- **优化方向**: 批量查询（Pipeline）减少 RTT

## 📝 待完善功能

- [ ] 配置文件加载
- [ ] 离线消息存储（当前离线用户直接跳过）
- [ ] 推送优先级（重要消息优先推送）
- [ ] 推送去重（避免重复推送）
- [ ] 推送统计（成功率、延迟监控）
- [ ] 大群聊优化（读扩散策略）
- [ ] 推送失败告警
- [ ] 性能监控和指标上报
- [ ] 单元测试和集成测试

