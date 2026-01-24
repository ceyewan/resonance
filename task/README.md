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
├── consumer/            # MQ 消费者
│   └── consumer.go      # 通用消费者，支持依赖注入处理函数
├── dispatcher/          # 消息分发器
│   └── dispatcher.go    # DispatchStorage (写扩散) / DispatchPush (推送)
└── pusher/              # Gateway 推送客户端
    ├── manager.go       # 连接管理器（gatewayID -> RPC Client）
    └── client.go        # 单个 Gateway 的推送客户端（队列 + Loop）
```

## 🔄 消息流转

### 完整流程

```
Logic (MQ Publish)
  ↓
NATS (PushEvent)
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
    GatewayQueueSize   int    // 每个 Gateway 的推送队列大小（默认: 1000）

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

gateway_service_name: gateway-service
gateway_queue_size: 1000

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

```go
type HandlerFunc func(context.Context, *mqv1.PushEvent) error

func NewConsumer(
    mqClient mq.Client,
    handler  HandlerFunc,
    config   config.ConsumerConfig,
    logger   clog.Logger,
) *Consumer
```

**双消费者初始化**:

```go
// Storage Consumer - 处理写扩散
storageConsumer := consumer.NewConsumer(
    mqClient,
    dispatcher.DispatchStorage,
    cfg.StorageConsumer,
    logger,
)

// Push Consumer - 处理推送
pushConsumer := consumer.NewConsumer(
    mqClient,
    dispatcher.DispatchPush,
    cfg.PushConsumer,
    logger,
)
```

### 2. Dispatcher (消息分发器)

**职责分离**:

- `DispatchStorage` - 执行写扩散落库
- `DispatchPush` - 查询路由，投递推送任务到队列

```go
// DispatchStorage - 写扩散
func (d *Dispatcher) DispatchStorage(ctx context.Context, event *mqv1.PushEvent) error {
    // 1. 获取会话成员
    members, _ := d.sessionRepo.GetMembers(ctx, event.SessionId)

    // 2. 构造 Inbox 记录
    inboxes := make([]*model.Inbox, len(members))
    for i, m := range members {
        inboxes[i] = &model.Inbox{
            OwnerUsername: m.Username,
            SessionID:     event.SessionId,
            MsgID:         event.MsgId,
            SeqID:         event.SeqId,
        }
    }

    // 3. 批量落库
    return d.messageRepo.SaveInbox(ctx, inboxes)
}

// DispatchPush - 推送
func (d *Dispatcher) DispatchPush(ctx context.Context, event *mqv1.PushEvent) error {
    // 1. 获取会话成员（排除发送者）
    // 2. 批量获取用户路由 (GatewayID)
    // 3. 按 GatewayID 分组
    // 4. 投递到各 Gateway 的推送队列
}
```

### 3. Pusher.Manager (连接管理器)

**职责**:

- 管理所有 Gateway 的 RPC Client
- 通过 Etcd Registry 发现 Gateway 实例
- 为每个 Gateway 创建独立队列和推送 Loop

```go
type Manager struct {
    registry   registry.Registry
    queueSize  int              // 每个 Gateway 的队列大小
    clients    map[string]*GatewayClient  // gatewayID -> Client
}

// 每个 GatewayClient 有独立的推送队列和 Loop
type GatewayClient struct {
    client    gatewayv1.PushServiceClient
    pushQueue chan *PushTask   // 推送队列
    logger    clog.Logger
    ctx       context.Context
    cancel    context.CancelFunc
    wg        *sync.WaitGroup
}
```

**推送流程**:

```
DispatchPush
    ↓
按 GatewayID 分组
    ↓
Manager.GetClient(gatewayID)
    ↓
GatewayClient.Enqueue(task)  // 非阻塞投递
    ↓
pushLoop() goroutine
    ↓
Unary RPC Push → Gateway
```

### 4. GatewayClient (单 Gateway 推送客户端)

**异步持久化模式**:

```go
// 每个 Gateway 独立的推送 Loop
func (c *GatewayClient) pushLoop() {
    for {
        select {
        case <-c.ctx.Done():
            return
        case task := <-c.pushQueue:
            c.doPush(task)  // 一元 RPC
        }
    }
}

func (c *GatewayClient) doPush(task *PushTask) {
    ctx, cancel := context.WithTimeout(c.ctx, 3*time.Second)
    defer cancel()

    resp, err := c.client.Push(ctx, &gatewayv1.PushRequest{
        ToUsernames: task.ToUsernames,
        Message:     task.Message,
    })
    // 错误处理...
}
```

**特性**:

- **独立队列**: 每个 Gateway 一个 buffered channel
- **独立 Loop**: 每个 Gateway 一个 goroutine 持续推送
- **非阻塞投递**: `Enqueue()` 队列满立即返回错误
- **优雅关闭**: `Close()` 等待队列清空

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
```

### 推送队列

- **队列大小**: 默认 1000，可按 Gateway 数量和消息量调整
- **监控**: `GatewayClient.QueueSize()` 可获取当前队列长度
- **非阻塞**: 队列满时 `Enqueue()` 返回错误，由 Consumer 重试

## 📝 待完善功能

- [ ] 推送失败重试（当前仅记录日志）
- [ ] 推送优先级队列
- [ ] 推送去重（避免重复推送）
- [ ] 推送统计（成功率、延迟监控）
- [ ] 大群聊优化（读扩散策略）
- [ ] 单元测试和集成测试
