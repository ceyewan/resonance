# Task 服务

Task 是 Resonance IM 系统的异步任务处理服务，负责消息的写扩散和推送分发。

## 📐 架构设计

### 核心职责

**消息处理流程**:
1. **消费 MQ** - 订阅 NATS 的 PushEvent 消息
2. **写扩散** - 查询会话成员，为每个用户生成推送任务
3. **服务发现** - 通过 Registry 查找用户连接的 Gateway 实例
4. **推送到 Gateway** - 通过 gRPC 双向流推送消息

### 目录结构

```
task/
├── config.go              # 配置管理
├── task.go                # 主服务入口
├── README.md              # 服务文档
├── consumer/              # MQ 消费者
│   └── consumer.go        # 消费 PushEvent，带重试机制
├── dispatcher/            # 消息分发器
│   └── dispatcher.go      # 写扩散逻辑，查询用户路由
└── pusher/                # Gateway 推送客户端
    ├── gateway_pusher.go  # GatewayPusher 对外接口
    └── connection_manager.go  # 连接管理器（gatewayID -> gRPC 连接）
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
SessionRepo.GetMembers()
  ↓ 查询用户路由（GatewayID）
RouterRepo.GetUserGateway()
  ↓ 服务发现，查找 Gateway 实例
Registry.GetService("gateway-service")
  ↓ 匹配 instance.Metadata["gateway_id"]
ConnectionManager.getOrCreateConn()
  ↓ 推送消息
Gateway PushService (gRPC 双向流)
  ↓
WebSocket Client
```

### 服务发现机制

**GatewayID 是逻辑标识符**（如 `gateway-001`），存储在：
- Registry 的 ServiceInstance.Metadata 中：`metadata["gateway_id"] = "gateway-001"`
- Router 表中：`router.gateway_id` 记录用户连接的 Gateway

**查找流程**:
```go
// 1. RouterRepo 获取用户的 GatewayID
router, _ := routerRepo.GetUserGateway(ctx, username)
// router.GatewayID == "gateway-001"

// 2. Registry 查找所有 Gateway 实例
instances, _ := registry.GetService(ctx, "gateway-service")

// 3. 匹配 gateway_id
for _, inst := range instances {
    if inst.Metadata["gateway_id"] == "gateway-001" {
        return inst, nil  // 找到目标实例
    }
}
```

## ⚙️ 配置说明

### 配置结构

```go
type Config struct {
    // 基础组件配置
    Log     clog.Config           // 日志配置
    MySQL   connector.MySQLConfig // MySQL 配置
    Redis   connector.RedisConfig // Redis 配置
    NATS    connector.NATSConfig  // NATS 配置
    Etcd    connector.EtcdConfig  // Etcd 配置
    Registry RegistryConfig       // Registry 配置

    // Gateway 服务配置
    GatewayServiceName string // Gateway 服务名称（默认: gateway-service）

    // 消费者配置
    ConsumerConfig ConsumerConfig
}

type RegistryConfig struct {
    Namespace       string        // 服务命名空间（默认: /resonance/services）
    DefaultTTL      time.Duration // 默认租约（默认: 30s）
    EnableCache     bool          // 是否启用缓存
    CacheExpiration time.Duration // 缓存过期时间（默认: 10s）
}

type ConsumerConfig struct {
    Topic         string // 订阅的主题 (默认: resonance.push.event.v1)
    QueueGroup    string // 队列组名称 (默认: task-service)
    WorkerCount   int    // 并发处理协程数 (默认: 10)
    MaxRetry      int    // 最大重试次数 (默认: 3)
    RetryInterval int    // 重试间隔（秒）(默认: 5)
}
```

### 配置文件示例

```yaml
# config/task.yaml
log:
  level: debug
  format: console

mysql:
  host: 127.0.0.1
  port: 3306
  database: resonance

redis:
  addr: 127.0.0.1:6379

nats:
  url: nats://127.0.0.1:4222

etcd:
  endpoints:
    - 127.0.0.1:2379

registry:
  namespace: /resonance/services
  default_ttl: 30s
  enable_cache: true

gateway_service_name: gateway-service

consumer:
  topic: resonance.push.event.v1
  queue_group: task-service
  worker_count: 10
  max_retry: 3
  retry_interval: 5
```

## 🚀 使用示例

```go
package main

import (
    "os"
    "os/signal"
    "syscall"

    "github.com/ceyewan/resonance/task"
    "github.com/ceyewan/resonance/im-sdk/repo"
)

func main() {
    // 创建配置
    cfg := task.DefaultConfig()

    // 创建 Task 实例
    t, err := task.New(cfg)
    if err != nil {
        panic(err)
    }

    // 注入 Repo 实现（必须）
    t.SetRepositories(routerRepo, sessionRepo)

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
- 使用 Handler 模式处理消息
- 解析 PushEvent 并调用 Dispatcher
- 处理成功后 Ack，失败后 Nak 重新入队

**特性**:
- 队列组订阅（多个 Task 实例负载均衡）
- 带重试机制（最多重试 3 次，间隔 5 秒）
- 优雅关闭（等待正在处理的消息完成）

```go
// Handler 签名
func (c *Consumer) handleMessage(ctx context.Context, msg mq.Message) error {
    // 1. 解析 PushEvent
    event := &mqv1.PushEvent{}
    proto.Unmarshal(msg.Data(), event)

    // 2. 调用 Dispatcher（带重试）
    return c.processWithRetry(event)
}
```

### 2. Dispatcher (消息分发器)

**职责**:
- 查询会话成员列表（SessionRepo）
- 查询每个成员的路由信息（RouterRepo）
- 调用 Pusher 推送消息

**写扩散逻辑**:
```go
func (d *Dispatcher) Dispatch(ctx context.Context, event *mqv1.PushEvent) error {
    // 1. 获取会话成员
    members, _ := d.sessionRepo.GetMembers(ctx, event.SessionId)

    // 2. 遍历成员推送
    for _, member := range members {
        // 获取用户的 GatewayID
        router, _ := d.routerRepo.GetUserGateway(ctx, member.Username)
        if router == nil {
            continue // 用户离线或无路由
        }

        // 构造推送消息
        pushMsg := &gatewayv1.PushMessage{
            MsgId:   event.MsgId,
            SeqId:   event.SeqId,
            From:    event.From,
            Type:    event.Type,
            Content: event.Content,
        }

        // 推送到指定 Gateway
        d.pusher.Push(ctx, router.GatewayID, member.Username, pushMsg)
    }
}
```

### 3. ConnectionManager (连接管理器)

**职责**:
- 管理 gatewayID → gRPC 连接的映射
- 通过 Registry 查找 Gateway 实例
- 为每个 Gateway 维护一个双向流
- 连接健康检查和自动重连

**核心方法**:
```go
type ConnectionManager struct {
    registry registry.Registry                  // 服务发现
    service  string                             // Gateway 服务名
    clients  map[string]*GatewayConn            // gatewayID -> 连接
    mu       sync.RWMutex
    logger   clog.Logger
}

// Push 推送消息到指定 Gateway
func (cm *ConnectionManager) Push(ctx context.Context, gatewayID, username string, msg *gatewayv1.PushMessage) error

// findGatewayInstance 在注册中心查找指定 gatewayID 的实例
func (cm *ConnectionManager) findGatewayInstance(ctx context.Context, gatewayID string) (*registry.ServiceInstance, error)
```

**连接特性**:
- **懒加载连接**: 首次使用时创建连接
- **连接复用**: 后续推送复用已有连接
- **健康检查**: 5 分钟未使用的连接被视为不健康
- **自动重连**: 流断开后自动重建

### 4. GatewayPusher (Gateway 推送客户端)

**职责**:
- 封装 ConnectionManager，提供简洁的推送接口

```go
type GatewayPusher struct {
    connMgr *ConnectionManager
    logger  clog.Logger
}

// Push 推送消息到指定 Gateway 的指定用户
func (p *GatewayPusher) Push(ctx context.Context, gatewayID, username string, msg *gatewayv1.PushMessage) error
```

## 📊 性能考虑

### 并发处理

- **Worker 数量**: 默认 10 个，可根据消息量调整
- **推送并发**: 每个会话的成员推送可优化为并发（当前串行）

### 连接管理

- **连接池**: 每个 GatewayID 一个 gRPC 连接
- **健康检查**: 5 分钟未使用的连接关闭
- **双向流复用**: 单个连接处理所有推送请求

### 重试策略

- **消费者重试**: 最大 3 次，间隔 5 秒
- **失败处理**: 重试失败后 Nak，消息重新入队
- **连接重试**: 流断开后自动重连

## 📝 待完善功能

- [ ] 离线消息存储（当前离线用户直接跳过）
- [ ] 推送优先级（重要消息优先推送）
- [ ] 推送去重（避免重复推送）
- [ ] 推送统计（成功率、延迟监控）
- [ ] 大群聊优化（读扩散策略）
- [ ] 推送失败告警
- [ ] 性能监控和指标上报
- [ ] 单元测试和集成测试
