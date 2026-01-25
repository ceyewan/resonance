# Task 服务

Task 是 Resonance IM 系统的异步任务处理服务，负责消息的写扩散和推送分发。

## 📐 架构设计

### 核心职责

**双消费者模式**:

1. **Storage Consumer** - 消费 MQ，执行消息写扩散（Inbox 落库）
2. **Push Consumer** - 消费 MQ，查询用户路由并推送到 Gateway

### 目录结构

```
task/
├── config/              # 配置管理
│   └── config.go        # 配置加载
├── task.go              # 主服务入口
├── README.md            # 服务文档
├── observability/       # 可观测性
│   ├── observability.go # Trace & Metrics
│   └── config.go        # 可观测性配置
├── consumer/            # MQ 消费者
│   └── consumer.go      # 通用消费者，支持依赖注入处理函数
├── dispatcher/          # 消息分发器
│   └── dispatcher.go    # DispatchStorage (写扩散) / DispatchPush (推送)
└── pusher/              # Gateway 推送客户端
    ├── manager.go       # 连接管理器（gatewayID -> RPC Client）
    ├── client.go        # 单个 Gateway 的推送客户端（队列 + Loop）
    └── interface.go     # PusherManager 接口
```

## 🔄 消息流转

### 完整流程

```
Logic (MQ Publish)
  ↓
NATS (PushEvent with trace_headers)
  ↓
┌─────────────────────────────────────┐
│         Task 双消费者模式              │
├─────────────────────────────────────┤
│                                     │
│  ┌────────────────┐  ┌────────────┐ │
│  │ Storage Consumer│  │Push Consumer│ │
│  └────────┬───────┘  └──────┬─────┘ │
│           │                  │       │
│           ▼                  ▼       │
│    DispatchStorage    DispatchPush   │
│    (写扩散落库)       (查询路由)      │
│           │                  │       │
│           ▼                  ▼       │
│      Inbox 表         RouterRepo     │
│                       (GatewayID)    │
│                           │          │
│                           ▼          │
│                    按 Gateway 分组   │
│                           │          │
│                           ▼          │
│                    ┌──────────────┐  │
│                    │ Pusher Queue │  │
│                    │  (异步持久化)  │  │
│                    └──────┬───────┘  │
│                           │          │
│                           ▼          │
│                    ┌──────────────┐  │
│                    │  pushLoop()  │  │
│                    │  (goroutine) │  │
│                    └──────┬───────┘  │
│                           │          │
│                           ▼          │
│                    Unary RPC Push   │
│                           │          │
└───────────────────────────┼──────────┘
                            ▼
                    Gateway PushService
                            ▼
                      WebSocket Client
```

### 设计优势

**职责分离**:
- Storage Consumer 专注消息落库，失败可重试
- Push Consumer 专注在线推送，解耦存储和推送

**异步持久化**:
- 每个 Gateway 维护独立队列和推送 Loop
- MQ 消费不阻塞推送，提高吞吐量
- Gateway 重启不影响队列中待推送消息

**资源隔离**:
- 两个消费者独立配置 Worker 数
- 存储慢不影响推送，推送慢不影响存储

## 🔍 可观测性

### Trace 支持

Task 服务支持 OpenTelemetry 分布式追踪，Trace Context 通过以下方式传播：

1. **PushEvent.trace_headers** - protobuf 字段（主要）
2. **NATS Message Headers** - MQ 原生 Headers（兜底）

**Trace 链路**:
```
Logic → MQ → Task.Consumer → Task.Dispatcher → Gateway
   (inject)   (extract)     (child span)      (propagate)
```

### Metrics 指标

| 指标名称 | 类型 | 说明 |
|---------|------|------|
| `task_storage_process_duration_seconds` | Histogram | Storage 处理耗时 |
| `task_push_enqueue_total` | Counter | Push 入队成功数 |
| `task_push_enqueue_failed_total` | Counter | Push 入队失败数 |
| `task_push_process_duration_seconds` | Histogram | Push 处理耗时 |
| `task_gateway_queue_depth` | Gauge | Gateway 队列深度 |
| `task_gateway_connected_total` | Gauge | Gateway 连接数 |

**配置示例**:
```yaml
observability:
  trace:
    endpoint: localhost:4317  # OTLP Collector
    sampler: 1.0               # 采样率
    insecure: true             # 非加密连接
  metrics:
    port: 9090                 # Prometheus 端口
    path: /metrics
    enable_runtime: true       # Go Runtime 指标
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

    // 可观测性配置
    Observability struct {
        Trace   TraceConfig   // Trace 配置
        Metrics MetricsConfig  // Metrics 配置
    }

    // Gateway 服务配置
    GatewayServiceName string // Gateway 服务名称（默认: gateway-service）
    GatewayQueueSize   int    // 每个 Gateway 的推送队列大小（默认: 1000）
    GatewayPusherCount int    // 每个 Gateway 的并发推送协程数（默认: 3）

    // 消费者配置（双消费者）
    StorageConsumer ConsumerConfig // 存储消费者
    PushConsumer    ConsumerConfig // 推送消费者
}

type ConsumerConfig struct {
    Topic         string // 订阅的主题
    QueueGroup    string // 队列组名称
    WorkerCount   int    // 并发处理协程数
    MaxRetry      int    // 最大重试次数
    RetryInterval int    // 重试间隔（秒）
}
```

### 配置文件示例

```yaml
# configs/task.yaml
log:
  level: debug
  format: json

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
  poll_interval: 10s  # 服务发现轮询间隔

gateway_service_name: gateway-service
gateway_queue_size: 1000
gateway_pusher_count: 3

# 可观测性配置
observability:
  trace:
    endpoint: localhost:4317  # Tempo/Jaeger OTLP 端口
    sampler: 1.0
    insecure: true
  metrics:
    port: 9090
    path: /metrics
    enable_runtime: true

storage_consumer:
  topic: resonance.push.event.v1
  queue_group: resonance_group_storage
  worker_count: 20
  max_retry: 3
  retry_interval: 5

push_consumer:
  topic: resonance.push.event.v1
  queue_group: resonance_group_push
  worker_count: 50
  max_retry: 3
  retry_interval: 5
```

## 🔑 关键组件

### 1. Consumer (通用消费者)

**职责**:

- 订阅 NATS 主题（支持 Queue Group）
- Worker Pool 并发处理消息
- 依赖注入处理函数，支持不同业务逻辑
- 自动提取 Trace Context 并创建子 Span
- 记录处理耗时指标

```go
type HandlerFunc func(context.Context, *mqv1.PushEvent) error

func NewConsumer(
    mqClient mq.Client,
    handler  HandlerFunc,
    config   config.ConsumerConfig,
    logger   clog.Logger,
) *Consumer
```

### 2. Dispatcher (消息分发器)

**职责分离**:

- `DispatchStorage` - 执行写扩散落库
- `DispatchPush` - 查询路由，投递推送任务到队列

**特性**:
- 自动创建子 Span 用于追踪
- 记录推送入队/失败指标
- 更新 Gateway 队列深度指标

### 3. Pusher.Manager (连接管理器)

**职责**:

- 管理所有 Gateway 的 RPC Client
- 通过 Etcd Registry 轮询发现 Gateway 实例
- 为每个 Gateway 创建独立队列和推送 Loop

**服务发现**:
- 当前使用轮询模式（默认 10s）
- TODO: 考虑使用 registry.Watch 实现实时监听

### 4. GatewayClient (单 Gateway 推送客户端)

**异步持久化模式**:

```go
type GatewayClient struct {
    client      gatewayv1.PushServiceClient
    pushQueue   chan *PushTask    // 推送队列
    pusherCount int               // 并发推送协程数
}
```

**特性**:
- **独立队列**: 每个 Gateway 一个 buffered channel
- **并发推送**: 支持配置多个 pusher 并发处理
- **重试机制**: 推送失败自动重试 3 次
- **优雅关闭**: `Close()` 等待队列清空并 drain 剩余消息
- **指标上报**: 入队/消费时更新队列深度指标

## 📊 性能考虑

### 双消费者优势

| 场景 | 单消费者 | 双消费者 |
|------|---------|---------|
| 存储慢 | 阻塞推送 | 推送继续 |
| 推送慢 | 阻塞存储 | 存储继续 |
| Worker 配置 | 共享 | 独立配置 |
| 重试策略 | 统一 | 分离 |

### 并发配置

```yaml
storage_consumer:
  worker_count: 20   # 存储需要更多 Worker（数据库 IO）

push_consumer:
  worker_count: 50   # 推送需要更多 Worker（网络 IO）

gateway_pusher_count: 3  # 每个 Gateway 3 个并发推送协程
```

## 🔧 可靠性保障

### 消息处理可靠性

| 场景 | 处理方式 |
|------|---------|
| 处理失败 | Nak 重试（可配置重试次数） |
| 解析失败 | Ack + 日志记录（TODO: 死信队列） |
| 队列满 | 返回错误，由 Consumer 重试 |
| 网络超时 | 自动重试 3 次 |

### 优雅关闭

1. **Consumer**: 停止订阅 → 关闭通道 → drain 队列 → 等待 worker
2. **GatewayClient**: 关闭队列 → cancel context → drain 队列 → 等待 pusher → 关闭连接
3. **Task 资源**: 并发关闭 → 10s 超时控制

## 📝 待完善功能

- [ ] P0: 消息解析失败记录到死信队列（等 JetStream 支持）
- [ ] P2: 背压机制实现（当前队列满阻塞）
- [ ] P3: Watch 服务发现替代轮询
- [ ] 推送优先级队列
- [ ] 推送去重（避免重复推送）
- [ ] 大群聊优化（读扩散策略）
- [ ] 单元测试和集成测试
